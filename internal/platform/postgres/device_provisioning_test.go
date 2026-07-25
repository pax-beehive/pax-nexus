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
	enrollment, _, err := registryService.CreateDeviceEnrollment(ctx, owner, onprem.DeviceEnrollmentRequest{
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
	enrollment, _, err := registryService.CreateDeviceEnrollment(ctx, owner, onprem.DeviceEnrollmentRequest{
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
	enrollment, _, err := services.registry.CreateDeviceEnrollment(ctx, services.owner, onprem.DeviceEnrollmentRequest{
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

// TestListDeviceProvisionedAgentsReturnsOwnRowsOrderedIncludingRevokedHistory
// exercises Task 6's store query: device A provisions two agents, one of
// which is rotated (so it has one revoked and one active credential row).
// The list must return only device A's rows, ordered by agent_id then
// created_at DESC, and must include the rotated agent's revoked history.
func (s *deviceProvisioningStoreSuite) TestListDeviceProvisionedAgentsReturnsOwnRowsOrderedIncludingRevokedHistory() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)
	deviceA := s.enrollDevice(services)
	deviceB := s.enrollDevice(services)

	// Deliberately provision in an order that would not already sort by
	// agent_id, so this test would fail if the store didn't apply ORDER BY.
	agentZID := uniqueCredentialValue("zzz-static-agent")
	agentRID := uniqueCredentialValue("rrr-rotated-agent")

	staticAgent, err := services.credential.ProvisionDeviceAgent(ctx, deviceA, onprem.DeviceProvisionRequest{
		AgentID: agentZID, DisplayName: "Static Agent", AgentType: "codex",
	})
	s.Require().NoError(err)

	rotatedFirst, err := services.credential.ProvisionDeviceAgent(ctx, deviceA, onprem.DeviceProvisionRequest{
		AgentID: agentRID, DisplayName: "Rotated Agent", AgentType: "claude",
	})
	s.Require().NoError(err)
	// Advance the fixed clock so the rotated credential's created_at is
	// strictly later than the credential it rotates away, making the
	// created_at DESC ordering deterministic for this test.
	*services.now = services.now.Add(time.Second)
	rotatedSecond, err := services.credential.ProvisionDeviceAgent(ctx, deviceA, onprem.DeviceProvisionRequest{
		AgentID: agentRID, DisplayName: "Rotated Agent", AgentType: "claude",
	})
	s.Require().NoError(err)
	s.Require().Equal(rotatedFirst.CredentialID, rotatedSecond.RotatedFromCredentialID)

	// Device B's own provisioning must not leak into device A's listing.
	deviceBAgentID := uniqueCredentialValue("device-b-agent")
	_, err = services.credential.ProvisionDeviceAgent(ctx, deviceB, onprem.DeviceProvisionRequest{
		AgentID: deviceBAgentID, DisplayName: "Device B Agent", AgentType: "codex",
	})
	s.Require().NoError(err)

	agents, err := services.credential.ListDeviceProvisionedAgents(ctx, deviceA)
	s.Require().NoError(err)

	// agent_id ordering: agentRID ("rrr-...") sorts before agentZID ("zzz-...").
	s.Require().Len(agents, 3, "expected two rows for the rotated agent and one for the static agent")
	for _, agent := range agents {
		s.NotEqual(deviceBAgentID, agent.AgentID, "device B's provisions must not appear in device A's listing")
	}

	// The rotated agent's two rows come first (agent_id "rrr-..." < "zzz-..."),
	// newest created_at first.
	s.Equal(agentRID, agents[0].AgentID)
	s.Equal(rotatedSecond.CredentialID, agents[0].CredentialID)
	s.Nil(agents[0].RevokedAt, "the active rotated credential must not be revoked")
	s.Equal(onprem.AgentStatusActive, agents[0].AgentStatus)
	s.Equal("Rotated Agent", agents[0].DisplayName)
	s.Equal("claude", agents[0].AgentType)

	s.Equal(agentRID, agents[1].AgentID)
	s.Equal(rotatedFirst.CredentialID, agents[1].CredentialID)
	s.NotNil(agents[1].RevokedAt, "the rotated-away credential must be revoked")

	s.True(agents[1].CreatedAt.Before(agents[0].CreatedAt) || agents[1].CreatedAt.Equal(agents[0].CreatedAt),
		"rows for the same agent must be ordered created_at DESC")

	s.Equal(agentZID, agents[2].AgentID)
	s.Equal(staticAgent.CredentialID, agents[2].CredentialID)
	s.Nil(agents[2].RevokedAt)
	s.Equal("Static Agent", agents[2].DisplayName)
}

// TestRevokeDeviceCascadesToProvisionedAgentCredentials exercises Task 7's
// acceptance criterion: revoking a device credential through
// RegistryStore.RevokeDevice immediately 401s the agent key it provisioned,
// atomically, and revokes the device credential itself. It also exercises
// the idempotent-replay contract: the same actor+key on an already-revoked
// device returns the same summary without error, while a different key
// conflicts.
func (s *deviceProvisioningStoreSuite) TestRevokeDeviceCascadesToProvisionedAgentCredentials() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)

	enrollment, _, err := services.registry.CreateDeviceEnrollment(ctx, services.owner, onprem.DeviceEnrollmentRequest{
		DeviceName: uniqueCredentialValue("cascade-device"),
	})
	s.Require().NoError(err)
	deviceIssued, err := services.credential.ExchangeEnrollment(ctx, enrollment.Token)
	s.Require().NoError(err)
	device, err := services.credential.Authenticate(ctx, deviceIssued.APIKey)
	s.Require().NoError(err)

	agentID := uniqueCredentialValue("cascade-agent")
	provisioned, err := services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
		AgentID: agentID, DisplayName: "Cascade Agent", AgentType: "codex",
	})
	s.Require().NoError(err)

	// Sanity: the provisioned agent key authenticates before revocation.
	_, err = services.credential.Authenticate(ctx, provisioned.APIKey)
	s.Require().NoError(err, "provisioned agent key must authenticate before device revocation")

	idempotencyKey := "revoke-key-1"
	summary, err := s.store.Registry().RevokeDevice(
		ctx, services.owner, device.CredentialID, idempotencyKey, *services.now,
	)
	s.Require().NoError(err)
	s.Equal(device.CredentialID, summary.CredentialID)
	s.Require().NotNil(summary.RevokedAt)
	s.Equal(int64(0), summary.ProvisionedAgentCount,
		"the cascade must have already revoked the only agent this device provisioned")

	// The provisioned agent key is now unauthorized.
	_, err = services.credential.Authenticate(ctx, provisioned.APIKey)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized, "cascade must revoke the provisioned agent credential")

	// The device credential itself no longer authenticates.
	_, err = services.credential.Authenticate(ctx, deviceIssued.APIKey)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized, "the device credential itself must be revoked")

	s.assertAuditEvent(ctx, "human", "identity.credential.revoked", "credential", device.CredentialID)
	s.assertAuditEvent(ctx, "system", "identity.credential.revoked", "credential", provisioned.CredentialID)

	var cascadeActorCredentialID string
	err = s.store.Pool().QueryRow(ctx, `
		SELECT actor_credential_id FROM onprem_audit_events
		WHERE actor_kind = 'system' AND action = 'identity.credential.revoked' AND target_id = $1
	`, provisioned.CredentialID).Scan(&cascadeActorCredentialID)
	s.Require().NoError(err)
	s.Equal(device.CredentialID, cascadeActorCredentialID,
		"the cascade audit row must attribute the revoked device credential as the actor")

	// Idempotent replay: same actor + same key on an already-revoked device
	// returns the same summary without error.
	replaySummary, err := s.store.Registry().RevokeDevice(
		ctx, services.owner, device.CredentialID, idempotencyKey, *services.now,
	)
	s.Require().NoError(err)
	s.Equal(summary.CredentialID, replaySummary.CredentialID)
	s.Require().NotNil(replaySummary.RevokedAt)
	s.True(summary.RevokedAt.Equal(*replaySummary.RevokedAt), "replay must report the same revocation instant")
	s.Equal(summary.ProvisionedAgentCount, replaySummary.ProvisionedAgentCount)

	// A different idempotency key on an already-revoked device conflicts.
	_, err = s.store.Registry().RevokeDevice(ctx, services.owner, device.CredentialID, "different-key", *services.now)
	s.Require().ErrorIs(err, onprem.ErrIdempotencyConflict)
}

// TestRevokeDeviceUnknownCredentialNotFound verifies that revoking a
// credential ID that does not name an active device-kind row returns
// ErrCredentialNotFound, whether the ID is entirely unknown or names an
// ordinary agent-kind credential.
func (s *deviceProvisioningStoreSuite) TestRevokeDeviceUnknownCredentialNotFound() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)

	_, err := s.store.Registry().RevokeDevice(ctx, services.owner, "does-not-exist", "", *services.now)
	s.Require().ErrorIs(err, onprem.ErrCredentialNotFound)

	agentID := uniqueCredentialValue("not-a-device-agent")
	_, err = services.registry.CreateAgent(ctx, services.owner, onprem.CreateAgentRequest{
		AgentID: agentID, DisplayName: "Not A Device",
	})
	s.Require().NoError(err)
	agentEnrollment, err := services.registry.CreateEnrollment(ctx, services.owner, agentID, onprem.OwnerEnrollmentRequest{
		CredentialLabel: "laptop", Permissions: []onprem.Permission{onprem.PermissionObserve},
	})
	s.Require().NoError(err)
	agentIssued, err := services.credential.ExchangeEnrollment(ctx, agentEnrollment.Token)
	s.Require().NoError(err)

	_, err = s.store.Registry().RevokeDevice(ctx, services.owner, agentIssued.CredentialID, "", *services.now)
	s.Require().ErrorIs(err, onprem.ErrCredentialNotFound, "an agent-kind credential must not be revocable as a device")
}

// TestLegacyRevokeCredentialCascadesDeviceRevocation exercises the legacy
// admin path: CredentialStore.RevokeCredential(deviceID) must cascade to
// provisioned agent credentials identically to RegistryStore.RevokeDevice.
func (s *deviceProvisioningStoreSuite) TestLegacyRevokeCredentialCascadesDeviceRevocation() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)

	enrollment, _, err := services.registry.CreateDeviceEnrollment(ctx, services.owner, onprem.DeviceEnrollmentRequest{
		DeviceName: uniqueCredentialValue("legacy-cascade-device"),
	})
	s.Require().NoError(err)
	deviceIssued, err := services.credential.ExchangeEnrollment(ctx, enrollment.Token)
	s.Require().NoError(err)
	device, err := services.credential.Authenticate(ctx, deviceIssued.APIKey)
	s.Require().NoError(err)

	agentID := uniqueCredentialValue("legacy-cascade-agent")
	provisioned, err := services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
		AgentID: agentID, DisplayName: "Legacy Cascade Agent", AgentType: "codex",
	})
	s.Require().NoError(err)

	_, err = services.credential.Authenticate(ctx, provisioned.APIKey)
	s.Require().NoError(err)

	s.Require().NoError(s.store.Credentials().RevokeCredential(ctx, device.CredentialID, time.Now().UTC()))

	_, err = services.credential.Authenticate(ctx, provisioned.APIKey)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized, "legacy revoke must cascade to provisioned agent credentials")

	_, err = services.credential.Authenticate(ctx, deviceIssued.APIKey)
	s.Require().ErrorIs(err, onprem.ErrUnauthorized, "legacy revoke must revoke the device credential itself")

	s.assertAuditEvent(ctx, "system", "identity.credential.revoked", "credential", device.CredentialID)
	s.assertAuditEvent(ctx, "system", "identity.credential.revoked", "credential", provisioned.CredentialID)
}

// TestLegacyRevokeCredentialDoesNotCascadeForAgentCredentials is a regression
// test: revoking a plain agent-kind credential through the legacy path must
// not invoke the device cascade (only one audit revocation row, for the
// credential itself).
func (s *deviceProvisioningStoreSuite) TestLegacyRevokeCredentialDoesNotCascadeForAgentCredentials() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)
	agentID := uniqueCredentialValue("regression-agent")
	_, err := services.registry.CreateAgent(ctx, services.owner, onprem.CreateAgentRequest{
		AgentID: agentID, DisplayName: "Regression Agent",
	})
	s.Require().NoError(err)
	enrollment, err := services.registry.CreateEnrollment(ctx, services.owner, agentID, onprem.OwnerEnrollmentRequest{
		CredentialLabel: "regression-credential", Permissions: []onprem.Permission{onprem.PermissionObserve},
	})
	s.Require().NoError(err)
	issued, err := services.credential.ExchangeEnrollment(ctx, enrollment.Token)
	s.Require().NoError(err)

	s.Require().NoError(s.store.Credentials().RevokeCredential(ctx, issued.CredentialID, time.Now().UTC()))

	var revokedAt *time.Time
	err = s.store.Pool().QueryRow(ctx, `
		SELECT revoked_at FROM agent_credentials WHERE credential_id = $1
	`, issued.CredentialID).Scan(&revokedAt)
	s.Require().NoError(err)
	s.NotNil(revokedAt)

	var revokeAuditCount int
	err = s.store.Pool().QueryRow(ctx, `
		SELECT count(*) FROM onprem_audit_events
		WHERE action = 'identity.credential.revoked' AND target_id = $1
	`, issued.CredentialID).Scan(&revokeAuditCount)
	s.Require().NoError(err)
	s.Equal(1, revokeAuditCount, "revoking an agent-kind credential must not run the device cascade")
}

// TestListDevicesCountsAndStatusFilter exercises Task 8's ListDevices query:
// an active device with one actively-provisioned agent reports
// provisioned_agent_count 1, a revoked device reports 0, and the status
// filter includes/excludes each accordingly. agent_credentials is a shared
// table across this package's suites, so assertions look up each device's
// row by credential ID within a large-limit, unfiltered/filtered listing
// rather than assuming the whole result set belongs to this test.
func (s *deviceProvisioningStoreSuite) TestListDevicesCountsAndStatusFilter() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)

	active := s.enrollDevice(services)
	activeAgentID := uniqueCredentialValue("list-devices-active-agent")
	_, err := services.credential.ProvisionDeviceAgent(ctx, active, onprem.DeviceProvisionRequest{
		AgentID: activeAgentID, DisplayName: "Active Device Agent", AgentType: "codex",
	})
	s.Require().NoError(err)

	revoked := s.enrollDevice(services)
	_, err = s.store.Registry().RevokeDevice(ctx, services.owner, revoked.CredentialID, "list-devices-revoke", *services.now)
	s.Require().NoError(err)

	all, err := s.store.Registry().ListDevices(ctx, onprem.DeviceFilter{Limit: 100})
	s.Require().NoError(err)
	byID := deviceSummaryByCredentialID(all)
	s.Require().Contains(byID, active.CredentialID)
	s.Nil(byID[active.CredentialID].RevokedAt)
	s.Equal(int64(1), byID[active.CredentialID].ProvisionedAgentCount)
	s.Require().Contains(byID, revoked.CredentialID)
	s.NotNil(byID[revoked.CredentialID].RevokedAt)
	s.Equal(int64(0), byID[revoked.CredentialID].ProvisionedAgentCount)

	activeOnly, err := s.store.Registry().ListDevices(ctx, onprem.DeviceFilter{Status: "active", Limit: 100})
	s.Require().NoError(err)
	activeByID := deviceSummaryByCredentialID(activeOnly)
	s.Contains(activeByID, active.CredentialID)
	s.NotContains(activeByID, revoked.CredentialID, "status=active must exclude revoked devices")

	revokedOnly, err := s.store.Registry().ListDevices(ctx, onprem.DeviceFilter{Status: "revoked", Limit: 100})
	s.Require().NoError(err)
	revokedByID := deviceSummaryByCredentialID(revokedOnly)
	s.Contains(revokedByID, revoked.CredentialID)
	s.NotContains(revokedByID, active.CredentialID, "status=revoked must exclude active devices")
}

// TestListDevicesOrdersNewestFirstAndCursorPagesThroughOwnRows exercises
// Task 8's ORDER BY created_at DESC, credential_id keyset and its cursor
// round-trip through onprem.EncodeDeviceCursor (the same helper the HTTP
// handler uses to build next_cursor). agent_credentials is a shared,
// never-truncated table this whole package's suites (and repeated runs of
// this very test) accumulate rows into, so this test does not assume our
// three devices are the only rows: it fetches the full global ordering,
// asserts the DESC/credential_id-ASC keyset invariant holds across every
// row, locates our three devices by relative position within that full
// list, and verifies cursor continuation reproduces the full list's own
// next row exactly -- both around our devices and independent of them.
func (s *deviceProvisioningStoreSuite) TestListDevicesOrdersNewestFirstAndCursorPagesThroughOwnRows() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)

	oldest := s.enrollDevice(services)
	*services.now = services.now.Add(time.Hour)
	middle := s.enrollDevice(services)
	*services.now = services.now.Add(time.Hour)
	newest := s.enrollDevice(services)

	full, err := s.store.Registry().ListDevices(ctx, onprem.DeviceFilter{Status: "active", Limit: 1000})
	s.Require().NoError(err)
	s.Require().Less(len(full), 1000, "the fixture's Limit must exceed the table's active device count")

	// created_at must be non-increasing across the whole result (the DESC
	// half of the keyset). The credential_id tie-break is verified below via
	// exact cursor continuation instead of a client-side re-sort: Postgres's
	// text comparison for the WHERE clause's "credential_id > $4" and the
	// ORDER BY tie-break share the same collation, which need not match a
	// byte-wise comparison in Go.
	for i := 1; i < len(full); i++ {
		prev, current := full[i-1], full[i]
		s.Falsef(current.CreatedAt.After(prev.CreatedAt), "rows %d and %d must be ordered created_at DESC", i-1, i)
	}

	indexOf := func(credentialID string) int {
		for index, device := range full {
			if device.CredentialID == credentialID {
				return index
			}
		}
		s.Require().Failf("device not found in ListDevices result", "credential_id=%s", credentialID)
		return -1
	}
	oldestIndex := indexOf(oldest.CredentialID)
	middleIndex := indexOf(middle.CredentialID)
	newestIndex := indexOf(newest.CredentialID)
	s.Less(newestIndex, middleIndex, "the most recently created device must sort before an older one")
	s.Less(middleIndex, oldestIndex, "an older device must sort before the oldest device")

	// Cursor continuation from the row immediately before "middle" in the
	// full ordering must return exactly "middle" with Limit=1.
	before := full[middleIndex-1]
	cursor := onprem.EncodeDeviceCursor(before.CreatedAt, before.CredentialID)
	page, err := s.store.Registry().ListDevices(ctx, onprem.DeviceFilter{Status: "active", Limit: 1, Cursor: cursor})
	s.Require().NoError(err)
	s.Require().Len(page, 1)
	s.Equal(middle.CredentialID, page[0].CredentialID)

	// Paging on from "middle" with Limit=1 must return whatever the full
	// list has immediately after it, matching exact continuation semantics
	// regardless of what that next row happens to be.
	cursor2 := onprem.EncodeDeviceCursor(page[0].CreatedAt, page[0].CredentialID)
	page2, err := s.store.Registry().ListDevices(ctx, onprem.DeviceFilter{Status: "active", Limit: 1, Cursor: cursor2})
	s.Require().NoError(err)
	s.Require().Len(page2, 1)
	s.Equal(full[middleIndex+1].CredentialID, page2[0].CredentialID)
}

// TestGetDeviceListsProvisionedAgentsIncludingRevokedHistory exercises
// Task 8's GetDevice: the summary reports the device's own state (active,
// one actively-provisioned agent) and the agents list includes every
// credential row the device has provisioned, including the revoked history
// left behind by a rotation (Task 6's query, reused verbatim).
func (s *deviceProvisioningStoreSuite) TestGetDeviceListsProvisionedAgentsIncludingRevokedHistory() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)
	device := s.enrollDevice(services)
	agentID := uniqueCredentialValue("get-device-agent")

	first, err := services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
		AgentID: agentID, DisplayName: "Get Device Agent", AgentType: "codex",
	})
	s.Require().NoError(err)
	*services.now = services.now.Add(time.Second)
	second, err := services.credential.ProvisionDeviceAgent(ctx, device, onprem.DeviceProvisionRequest{
		AgentID: agentID, DisplayName: "Get Device Agent", AgentType: "codex",
	})
	s.Require().NoError(err)
	s.Require().Equal(first.CredentialID, second.RotatedFromCredentialID)

	detail, err := s.store.Registry().GetDevice(ctx, device.CredentialID)
	s.Require().NoError(err)
	s.Equal(device.CredentialID, detail.Device.CredentialID)
	s.Nil(detail.Device.RevokedAt)
	s.Equal(int64(1), detail.Device.ProvisionedAgentCount,
		"one distinct agent is actively provisioned despite two credential rows")

	s.Require().Len(detail.Agents, 2, "both the rotated-away and active credential rows must be included")
	byCredentialID := map[string]onprem.DeviceProvisionedAgent{}
	for _, agent := range detail.Agents {
		byCredentialID[agent.CredentialID] = agent
		s.Equal(agentID, agent.AgentID)
	}
	s.Require().Contains(byCredentialID, first.CredentialID)
	s.NotNil(byCredentialID[first.CredentialID].RevokedAt, "the rotated-away credential must be revoked")
	s.Require().Contains(byCredentialID, second.CredentialID)
	s.Nil(byCredentialID[second.CredentialID].RevokedAt, "the active credential must not be revoked")
}

// TestGetDeviceUnknownOrAgentKindCredentialNotFound mirrors
// TestRevokeDeviceUnknownCredentialNotFound: an entirely unknown credential
// ID and an ordinary agent-kind credential ID must both return
// ErrCredentialNotFound, since GetDevice only resolves kind='device' rows.
func (s *deviceProvisioningStoreSuite) TestGetDeviceUnknownOrAgentKindCredentialNotFound() {
	ctx := context.Background()
	services := s.newProvisioningServices(16)

	_, err := s.store.Registry().GetDevice(ctx, "does-not-exist")
	s.Require().ErrorIs(err, onprem.ErrCredentialNotFound)

	agentID := uniqueCredentialValue("get-device-not-a-device")
	_, err = services.registry.CreateAgent(ctx, services.owner, onprem.CreateAgentRequest{
		AgentID: agentID, DisplayName: "Not A Device",
	})
	s.Require().NoError(err)
	agentEnrollment, err := services.registry.CreateEnrollment(ctx, services.owner, agentID, onprem.OwnerEnrollmentRequest{
		CredentialLabel: "laptop", Permissions: []onprem.Permission{onprem.PermissionObserve},
	})
	s.Require().NoError(err)
	agentIssued, err := services.credential.ExchangeEnrollment(ctx, agentEnrollment.Token)
	s.Require().NoError(err)

	_, err = s.store.Registry().GetDevice(ctx, agentIssued.CredentialID)
	s.Require().ErrorIs(err, onprem.ErrCredentialNotFound, "an agent-kind credential must not be resolvable as a device")
}

func deviceSummaryByCredentialID(devices []onprem.DeviceSummary) map[string]onprem.DeviceSummary {
	result := make(map[string]onprem.DeviceSummary, len(devices))
	for _, device := range devices {
		result[device.CredentialID] = device
	}
	return result
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
