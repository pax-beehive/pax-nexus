package postgres_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

// deviceProvisioningStoreSuite exercises migration 019, which adds the
// device-credential columns and relaxed agent_id constraints described in
// docs/decisions/2026-07-24-device-scoped-agent-provisioning.md.
type deviceProvisioningStoreSuite struct {
	suite.Suite
	store        *postgres.Store
	userID       string
	membershipID string
}

func TestDeviceProvisioningStoreSuite(t *testing.T) {
	suite.Run(t, new(deviceProvisioningStoreSuite))
}

func (s *deviceProvisioningStoreSuite) SetupSuite() {
	store, err := postgres.Open(context.Background(), testDSN(s.T()))
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(context.Background()))
	s.store = store
}

func (s *deviceProvisioningStoreSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

// SetupTest seeds an active user + owner membership, mirroring the fixture
// idiom used by operations_test.go's TestAgentStatsAggregatesEventsNotesAndIdentity.
func (s *deviceProvisioningStoreSuite) SetupTest() {
	ctx := context.Background()
	userID := uniqueCredentialValue("device-provisioning-user")
	membershipID := uniqueCredentialValue("device-provisioning-membership")
	now := time.Now().UTC()
	_, err := s.store.Pool().Exec(ctx, `
		INSERT INTO onprem_users (user_id, display_name, identity_status, created_at, updated_at)
		VALUES ($1, 'Device Provisioning Owner', 'active', $2, $2)`, userID, now)
	s.Require().NoError(err)
	_, err = s.store.Pool().Exec(ctx, `
		INSERT INTO onprem_memberships (membership_id, user_id, role, status, joined_at, updated_at)
		VALUES ($1, $2, 'owner', 'active', $3, $3)`, membershipID, userID, now)
	s.Require().NoError(err)
	s.userID = userID
	s.membershipID = membershipID
}

func (s *deviceProvisioningStoreSuite) TestMigration019IsReplaySafeAndAddsDeviceColumns() {
	ctx := context.Background()
	// Migration already ran in SetupSuite; run it again here to prove
	// replay-safety of migration 019 on an already-populated database.
	s.Require().NoError(s.store.Migrate(ctx))

	for _, column := range []struct{ table, name string }{
		{"agent_credentials", "kind"},
		{"agent_credentials", "provisioned_by"},
		{"agent_credentials", "grantable_permissions"},
		{"agent_enrollments", "kind"},
		{"agent_enrollments", "grantable_permissions"},
		{"onprem_agents", "provisioned_by"},
	} {
		var dataType string
		err := s.store.Pool().QueryRow(ctx, `
			SELECT data_type FROM information_schema.columns
			WHERE table_name = $1 AND column_name = $2
		`, column.table, column.name).Scan(&dataType)
		s.Require().NoErrorf(err, "expected column %s.%s to exist", column.table, column.name)
	}

	// A device credential with an empty agent_id must be insertable.
	deviceCredentialID := uniqueCredentialValue("device-credential")
	// key_digest/token_digest are UNIQUE columns on a database shared across
	// this package's suites and across repeated test runs, so digests are
	// derived from unique IDs rather than fixed literals.
	_, err := s.store.Pool().Exec(ctx, `
		INSERT INTO agent_credentials (credential_id, key_digest, user_id, owner_membership_id,
			agent_id, label, permissions, created_at, kind, grantable_permissions)
		VALUES ($1, sha256(convert_to($1, 'UTF8')), 'usr-1', $2, '', 'todd-macbook-air',
			'{agent_provision}', now(), 'device', '{observe,search,get}')
	`, deviceCredentialID, s.membershipID)
	s.Require().NoError(err, "device credential row with empty agent_id should be insertable")

	// An agent credential (the default kind) with an empty agent_id must still be rejected.
	badCredentialID := uniqueCredentialValue("bad-credential")
	_, err = s.store.Pool().Exec(ctx, `
		INSERT INTO agent_credentials (credential_id, key_digest, user_id, owner_membership_id,
			agent_id, label, permissions, created_at)
		VALUES ($1, sha256(convert_to($1, 'UTF8')), 'usr-1', $2, '', '', '{observe}', now())
	`, badCredentialID, s.membershipID)
	s.Require().Error(err, "kind defaults to 'agent', so an empty agent_id must be rejected")

	// A device enrollment with an empty agent_id must also be insertable.
	deviceEnrollmentID := uniqueCredentialValue("device-enrollment")
	_, err = s.store.Pool().Exec(ctx, `
		INSERT INTO agent_enrollments (enrollment_id, token_digest, user_id, agent_id,
			permissions, created_at, expires_at, membership_id, kind, grantable_permissions)
		VALUES ($1, sha256(convert_to($1, 'UTF8')), 'usr-1', '', '{agent_provision}', now(),
			now() + interval '1 hour', $2, 'device', '{observe,search,get}')
	`, deviceEnrollmentID, s.membershipID)
	s.Require().NoError(err, "device enrollment row with empty agent_id should be insertable")

	// Audit events may now be recorded by a device credential actor.
	targetID := uniqueCredentialValue("device-audit-target")
	_, err = s.store.Pool().Exec(ctx, `
		INSERT INTO onprem_audit_events (actor_kind, action, target_kind, target_id, occurred_at)
		VALUES ('device', 'identity.agent.provisioned', 'credential', $1, now())
	`, targetID)
	s.Require().NoError(err, "audit events should accept a 'device' actor_kind")

	// Replay migration after device rows exist to prove 019 is truly replay-safe
	// on tables with device credential and enrollment rows.
	s.Require().NoError(s.store.Migrate(ctx), "migration 019 should replay safely with device rows present")
}

// TestDeviceEnrollmentCreateExchangeAuthenticateLoop exercises the full
// device-scoped provisioning loop described in Task 3: an Owner creates a
// device enrollment through RegistryService, the resulting token is
// exchanged for a credential through CredentialService, and the issued
// credential authenticates to a Principal carrying device semantics.
func (s *deviceProvisioningStoreSuite) TestDeviceEnrollmentCreateExchangeAuthenticateLoop() {
	ctx := context.Background()
	now := time.Now().UTC()
	registryService, err := onprem.NewRegistryService(s.store.Registry(), onprem.RegistryConfig{
		SecretPepper: "0123456789abcdef0123456789abcdef",
		MemberGrantablePermissions: []onprem.Permission{
			onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet,
		},
	}, onprem.WithRegistryClock(func() time.Time { return now }))
	s.Require().NoError(err)
	credentialService, err := onprem.NewCredentialService(s.store.Credentials(), onprem.CredentialConfig{
		RotationOverlap: time.Minute,
		SecretPepper:    "0123456789abcdef0123456789abcdef",
	}, onprem.WithClock(func() time.Time { return now }))
	s.Require().NoError(err)

	owner := onprem.HumanPrincipal{
		UserID: s.userID, MembershipID: s.membershipID, Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive,
	}
	enrollment, err := registryService.CreateDeviceEnrollment(ctx, owner, onprem.DeviceEnrollmentRequest{
		DeviceName: uniqueCredentialValue("todd-macbook-air"),
	})
	s.Require().NoError(err)
	s.NotEmpty(enrollment.Token)

	issued, err := credentialService.ExchangeEnrollment(ctx, enrollment.Token)
	s.Require().NoError(err)
	s.NotEmpty(issued.APIKey)

	principal, err := credentialService.Authenticate(ctx, issued.APIKey)
	s.Require().NoError(err)
	s.Equal(onprem.CredentialKindDevice, principal.Kind)
	s.True(principal.HasPermission(onprem.PermissionAgentProvision))
	s.False(principal.HasPermission(onprem.PermissionObserve))
	s.False(principal.HasPermission(onprem.PermissionSearch))
	s.False(principal.HasPermission(onprem.PermissionGet))
	s.Empty(principal.AgentID)
	s.Equal(
		[]onprem.Permission{onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet},
		principal.GrantablePermissions,
	)

	// Second exchange of the same token must fail: enrollments are one-time use.
	_, err = credentialService.ExchangeEnrollment(ctx, enrollment.Token)
	s.Require().ErrorIs(err, onprem.ErrEnrollmentInvalid)

	// Regression: an ordinary agent enrollment must still exchange successfully
	// and its Principal must carry agent kind semantics (not the empty zero
	// value observed by callers that don't know about device kinds).
	agentID := uniqueCredentialValue("regression-agent")
	_, err = registryService.CreateAgent(ctx, owner, onprem.CreateAgentRequest{
		AgentID: agentID, DisplayName: "Regression Agent",
	})
	s.Require().NoError(err)
	agentEnrollment, err := registryService.CreateEnrollment(ctx, owner, agentID, onprem.OwnerEnrollmentRequest{
		CredentialLabel: "regression-credential",
		Permissions:     []onprem.Permission{onprem.PermissionObserve},
	})
	s.Require().NoError(err)
	agentIssued, err := credentialService.ExchangeEnrollment(ctx, agentEnrollment.Token)
	s.Require().NoError(err)
	agentPrincipal, err := credentialService.Authenticate(ctx, agentIssued.APIKey)
	s.Require().NoError(err)
	s.Equal(onprem.CredentialKindAgent, agentPrincipal.Kind)
	s.Equal(agentID, agentPrincipal.AgentID)
}

// TestDeviceCredentialRotationPreservesKindAndGrantablePermissions exercises
// device credential rotation to verify that Kind and GrantablePermissions
// are preserved from the original principal through the rotation process,
// and that the rotated credential authenticates successfully.
func (s *deviceProvisioningStoreSuite) TestDeviceCredentialRotationPreservesKindAndGrantablePermissions() {
	ctx := context.Background()
	now := time.Now().UTC()
	registryService, err := onprem.NewRegistryService(s.store.Registry(), onprem.RegistryConfig{
		SecretPepper: "0123456789abcdef0123456789abcdef",
		MemberGrantablePermissions: []onprem.Permission{
			onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet,
		},
	}, onprem.WithRegistryClock(func() time.Time { return now }))
	s.Require().NoError(err)
	credentialService, err := onprem.NewCredentialService(s.store.Credentials(), onprem.CredentialConfig{
		RotationOverlap: time.Minute,
		SecretPepper:    "0123456789abcdef0123456789abcdef",
	}, onprem.WithClock(func() time.Time { return now }))
	s.Require().NoError(err)

	owner := onprem.HumanPrincipal{
		UserID: s.userID, MembershipID: s.membershipID, Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive,
	}

	// Create device enrollment and exchange for credential.
	enrollment, err := registryService.CreateDeviceEnrollment(ctx, owner, onprem.DeviceEnrollmentRequest{
		DeviceName: uniqueCredentialValue("device-rotation-test"),
	})
	s.Require().NoError(err)

	issued, err := credentialService.ExchangeEnrollment(ctx, enrollment.Token)
	s.Require().NoError(err)

	// Authenticate to get the original principal.
	originalPrincipal, err := credentialService.Authenticate(ctx, issued.APIKey)
	s.Require().NoError(err)
	s.Equal(onprem.CredentialKindDevice, originalPrincipal.Kind)
	s.True(originalPrincipal.HasPermission(onprem.PermissionAgentProvision))
	s.Equal(
		[]onprem.Permission{onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet},
		originalPrincipal.GrantablePermissions,
	)

	// Rotate the credential.
	rotated, err := credentialService.RotateCredential(ctx, originalPrincipal)
	s.Require().NoError(err)
	s.NotEmpty(rotated.APIKey)

	// Authenticate with the new rotated credential.
	rotatedPrincipal, err := credentialService.Authenticate(ctx, rotated.APIKey)
	s.Require().NoError(err)

	// Verify that Kind and GrantablePermissions are preserved.
	s.Equal(onprem.CredentialKindDevice, rotatedPrincipal.Kind, "Kind should be preserved as device")
	s.True(rotatedPrincipal.HasPermission(onprem.PermissionAgentProvision), "Permissions should be preserved")
	s.Equal(
		[]onprem.Permission{onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet},
		rotatedPrincipal.GrantablePermissions,
		"GrantablePermissions should be preserved",
	)
	s.Empty(rotatedPrincipal.AgentID, "Device credentials should have empty AgentID")
}

// deviceProvisioningServices bundles the registry and credential services a
// device-provisioning test needs, sharing one fixed clock so audit and
// credential timestamps are directly comparable.
type deviceProvisioningServices struct {
	registry   *onprem.RegistryService
	credential *onprem.CredentialService
	owner      onprem.HumanPrincipal
	now        *time.Time
}

func (s *deviceProvisioningStoreSuite) newProvisioningServices(deviceAgentLimit int) deviceProvisioningServices {
	now := time.Now().UTC()
	registryService, err := onprem.NewRegistryService(s.store.Registry(), onprem.RegistryConfig{
		SecretPepper: "0123456789abcdef0123456789abcdef",
		MemberGrantablePermissions: []onprem.Permission{
			onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet,
		},
	}, onprem.WithRegistryClock(func() time.Time { return now }))
	s.Require().NoError(err)
	credentialService, err := onprem.NewCredentialService(s.store.Credentials(), onprem.CredentialConfig{
		RotationOverlap:  time.Minute,
		SecretPepper:     "0123456789abcdef0123456789abcdef",
		DeviceAgentLimit: deviceAgentLimit,
	}, onprem.WithClock(func() time.Time { return now }))
	s.Require().NoError(err)
	owner := onprem.HumanPrincipal{
		UserID: s.userID, MembershipID: s.membershipID, Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive,
	}
	return deviceProvisioningServices{registry: registryService, credential: credentialService, owner: owner, now: &now}
}

// enrollDevice creates a device enrollment, exchanges it for a credential,
// and authenticates it to a device Principal.
func (s *deviceProvisioningStoreSuite) enrollDevice(services deviceProvisioningServices) onprem.Principal {
	ctx := context.Background()
	enrollment, err := services.registry.CreateDeviceEnrollment(ctx, services.owner, onprem.DeviceEnrollmentRequest{
		DeviceName: uniqueCredentialValue("device"),
	})
	s.Require().NoError(err)
	issued, err := services.credential.ExchangeEnrollment(ctx, enrollment.Token)
	s.Require().NoError(err)
	principal, err := services.credential.Authenticate(ctx, issued.APIKey)
	s.Require().NoError(err)
	return principal
}

// TestProvisionDeviceAgentCreatesAgentAndWorkingCredential exercises the
// store's create path: a device provisions a brand-new agent_id, the store
// inserts an onprem_agents row carrying provisioned_by, and the issued
// credential authenticates with exactly the permissions the device granted.
func (s *deviceProvisioningStoreSuite) TestProvisionDeviceAgentCreatesAgentAndWorkingCredential() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)
	device := s.enrollDevice(services)
	agentID := uniqueCredentialValue("device-provisioned-agent")

	provisioned, err := services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
		AgentID: agentID, DisplayName: "Device Agent", AgentType: "codex",
	})
	s.Require().NoError(err)
	s.True(provisioned.AgentCreated)
	s.Empty(provisioned.RotatedFromCredentialID)
	s.True(strings.HasPrefix(provisioned.APIKey, "tm_key_"))

	var provisionedBy, status string
	err = s.store.Pool().QueryRow(ctx, `
		SELECT COALESCE(provisioned_by, ''), status FROM onprem_agents WHERE agent_id = $1
	`, agentID).Scan(&provisionedBy, &status)
	s.Require().NoError(err)
	s.Equal(device.CredentialID, provisionedBy)
	s.Equal("active", status)

	// The provisioned key authenticates and carries exactly the device's
	// grantable permissions (observe, search, get); it must not carry
	// agent_provision — a provisioned agent credential can never re-provision.
	principal, err := services.credential.Authenticate(ctx, provisioned.APIKey)
	s.Require().NoError(err)
	s.Equal(onprem.CredentialKindAgent, principal.Kind)
	s.Equal(agentID, principal.AgentID)
	s.True(principal.HasPermission(onprem.PermissionObserve))
	s.True(principal.HasPermission(onprem.PermissionSearch))
	s.True(principal.HasPermission(onprem.PermissionGet))
	s.False(principal.HasPermission(onprem.PermissionAgentProvision))
}

// TestProvisionDeviceAgentReProvisioningRotates exercises the store's
// rotation path: a second provisioning call for the same device+agent_id
// revokes the previous credential, issues a new active one, and reports the
// old credential ID as RotatedFromCredentialID.
func (s *deviceProvisioningStoreSuite) TestProvisionDeviceAgentReProvisioningRotates() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)
	device := s.enrollDevice(services)
	agentID := uniqueCredentialValue("rotated-agent")

	first, err := services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
		AgentID: agentID, DisplayName: "Device Agent", AgentType: "codex",
	})
	s.Require().NoError(err)
	s.True(first.AgentCreated)

	firstPrincipal, err := services.credential.Authenticate(ctx, first.APIKey)
	s.Require().NoError(err)

	second, err := services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
		AgentID: agentID, DisplayName: "Device Agent", AgentType: "codex",
	})
	s.Require().NoError(err)
	s.False(second.AgentCreated)
	s.Equal(firstPrincipal.CredentialID, second.RotatedFromCredentialID)
	s.NotEqual(first.APIKey, second.APIKey)

	// The old key is now rejected.
	_, err = services.credential.Authenticate(ctx, first.APIKey)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized)

	// The new key authenticates and resolves the same agent.
	secondPrincipal, err := services.credential.Authenticate(ctx, second.APIKey)
	s.Require().NoError(err)
	s.Equal(agentID, secondPrincipal.AgentID)

	var revokedAt *time.Time
	err = s.store.Pool().QueryRow(ctx, `
		SELECT revoked_at FROM agent_credentials WHERE credential_id = $1
	`, firstPrincipal.CredentialID).Scan(&revokedAt)
	s.Require().NoError(err)
	s.NotNil(revokedAt, "the rotated-away credential must be revoked")
}

// TestProvisionDeviceAgentConflictsWithHumanRegisteredAgent verifies that a
// device cannot claim an agent_id a human owner already registered directly.
func (s *deviceProvisioningStoreSuite) TestProvisionDeviceAgentConflictsWithHumanRegisteredAgent() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)
	device := s.enrollDevice(services)
	agentID := uniqueCredentialValue("human-registered-agent")

	_, err := services.registry.CreateAgent(ctx, services.owner, onprem.CreateAgentRequest{
		AgentID: agentID, DisplayName: "Human Agent",
	})
	s.Require().NoError(err)

	_, err = services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
		AgentID: agentID, DisplayName: "Device Agent", AgentType: "codex",
	})

	s.Require().ErrorIs(err, onprem.ErrAgentProvisionConflict)
}

// TestProvisionDeviceAgentConflictsAcrossDevices verifies that a second
// device cannot provision an agent_id the first device already provisions.
func (s *deviceProvisioningStoreSuite) TestProvisionDeviceAgentConflictsAcrossDevices() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)
	deviceA := s.enrollDevice(services)
	deviceB := s.enrollDevice(services)
	agentID := uniqueCredentialValue("contested-agent")

	_, err := services.credential.ProvisionDeviceAgent(ctx, deviceA, onprem.DeviceProvisionRequest{
		AgentID: agentID, DisplayName: "Device A Agent", AgentType: "codex",
	})
	s.Require().NoError(err)

	_, err = services.credential.ProvisionDeviceAgent(ctx, deviceB, onprem.DeviceProvisionRequest{
		AgentID: agentID, DisplayName: "Device B Agent", AgentType: "codex",
	})

	s.Require().ErrorIs(err, onprem.ErrAgentProvisionConflict)
}

// TestProvisionDeviceAgentEnforcesActiveAgentLimit verifies that a device
// cannot exceed its configured active-agent cap: with limit=2, a third
// distinct agent_id must fail with ErrDeviceAgentLimitExceeded.
func (s *deviceProvisioningStoreSuite) TestProvisionDeviceAgentEnforcesActiveAgentLimit() {
	ctx := context.Background()
	services := s.newProvisioningServices(2)
	device := s.enrollDevice(services)

	agentIDs := make([]string, 2)
	for i := 0; i < 2; i++ {
		agentIDs[i] = uniqueCredentialValue(fmt.Sprintf("limited-agent-%d", i))
		_, err := services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
			AgentID: agentIDs[i], DisplayName: "Device Agent", AgentType: "codex",
		})
		s.Require().NoError(err)
	}

	thirdAgentID := uniqueCredentialValue("limited-agent-third")
	_, err := services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
		AgentID: thirdAgentID, DisplayName: "Device Agent", AgentType: "codex",
	})

	s.Require().ErrorIs(err, onprem.ErrDeviceAgentLimitExceeded)

	// Re-provisioning (rotating) an already-counted agent must still be
	// allowed at the cap, since the cap excludes the agent being provisioned.
	rotated, err := services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
		AgentID: agentIDs[0], DisplayName: "Device Agent", AgentType: "codex",
	})
	s.Require().NoError(err)
	s.False(rotated.AgentCreated)
}

// TestProvisionDeviceAgentRecordsAuditTrail verifies that every step of the
// provisioning transaction (agent creation, credential issuance, rotation)
// is captured as an audit event with actor_kind 'device'.
func (s *deviceProvisioningStoreSuite) TestProvisionDeviceAgentRecordsAuditTrail() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)
	device := s.enrollDevice(services)
	agentID := uniqueCredentialValue("audited-agent")

	created, err := services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
		AgentID: agentID, DisplayName: "Device Agent", AgentType: "codex",
	})
	s.Require().NoError(err)

	s.assertAuditEvent(ctx, "device", "identity.agent.created", "agent", agentID)
	s.assertAuditEvent(ctx, "device", "identity.agent.provisioned", "credential", created.CredentialID)
	s.assertAuditEvent(ctx, "device", "identity.credential.issued", "credential", created.CredentialID)

	rotated, err := services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
		AgentID: agentID, DisplayName: "Device Agent", AgentType: "codex",
	})
	s.Require().NoError(err)
	s.NotEmpty(rotated.RotatedFromCredentialID)

	s.assertAuditEvent(ctx, "device", "identity.credential.revoked", "credential", rotated.RotatedFromCredentialID)
	s.assertAuditEvent(ctx, "device", "identity.agent.provisioned", "credential", rotated.CredentialID)
}

func (s *deviceProvisioningStoreSuite) assertAuditEvent(
	ctx context.Context, actorKind, action, targetKind, targetID string,
) {
	var count int
	err := s.store.Pool().QueryRow(ctx, `
		SELECT count(*) FROM onprem_audit_events
		WHERE actor_kind = $1 AND action = $2 AND target_kind = $3 AND target_id = $4
	`, actorKind, action, targetKind, targetID).Scan(&count)
	s.Require().NoError(err)
	s.GreaterOrEqual(count, 1, "expected an audit event actor_kind=%s action=%s target_kind=%s target_id=%s",
		actorKind, action, targetKind, targetID)
}
