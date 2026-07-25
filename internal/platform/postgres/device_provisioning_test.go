package postgres_test

import (
	"context"
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
