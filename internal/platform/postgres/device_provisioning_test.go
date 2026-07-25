package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

// deviceProvisioningStoreSuite exercises migration 019, which adds the
// device-credential columns and relaxed agent_id constraints described in
// docs/decisions/2026-07-24-device-scoped-agent-provisioning.md.
type deviceProvisioningStoreSuite struct {
	suite.Suite
	store        *postgres.Store
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
}
