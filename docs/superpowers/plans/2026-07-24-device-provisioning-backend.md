# Device-Scoped Agent Provisioning — Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the accepted ADR `docs/decisions/2026-07-24-device-scoped-agent-provisioning.md`: device-kind credentials that can self-provision agent credentials, with cascade revocation, portal read endpoints, audit, tests, and docs — in repo `github.com/pax-beehive/pax-nexus`.

**Architecture:** A device is a row in `agent_credentials` with `kind='device'` (no new table). Device enrollments ride the existing `agent_enrollments` table with `kind='device'`, an empty `agent_id`, the device name in `credential_label`, permissions locked to `agent_provision`, and a `grantable_permissions` array that caps what the device may grant to agents. The provisioning endpoint runs one store transaction that locks the device row, creates-or-rotates the agent, enforces the per-device cap, and audits. Cascade revocation is a shared tx helper used by both existing revoke paths plus a new admin device-revoke endpoint.

**Tech Stack:** Go, CloudWeGo Hertz (`hz` codegen from `idl/team_memory.thrift`), pgx/v5 + PostgreSQL, testify.

## Global Constraints

- **Do not rename contract fields.** Three parties develop in parallel against ADR §2/§4: request `agent_id`, `display_name`, `agent_type`, `permissions`; endpoint `POST /v1/device/agent-provisions`; attribution field `provisioned_by`; permission literal `agent_provision`; credential prefix `tm_key_`; enrollment prefix `tm_enroll_`.
- Base branch: `origin/main` at `dc199b1`. Work in an isolated worktree, branch `feat/device-scoped-agent-provisioning`.
- Migrations re-run on EVERY boot inside one tx (no schema-version table). Every statement must be replay-safe on a populated database. Reference idiom: `internal/platform/postgres/migrations/017_onprem_identity_registry.sql`.
- New migration must be registered in BOTH the `//go:embed` directive (line ~14) AND the ordered slice in `Migrate` (lines ~138-157) of `internal/platform/postgres/store.go`.
- Per-device active agent limit: default **16**, env `TEAM_MEMORY_DEVICE_AGENT_LIMIT`.
- Device credentials must get 403 (ErrForbidden path) on observe/search/recall/channel — guaranteed because their `permissions` array is exactly `{agent_provision}` and every knowledge endpoint requires a different permission. Never let client input set device permissions.
- `agent_provision` must never be grantable to agent credentials: do NOT add it to `validateExplicitPermissions`, `validateEnrollmentRequest`, or `permissionListEnvironment` accept-lists.
- Tests: postgres-backed tests skip unless `TEAM_MEMORY_TEST_POSTGRES_DSN` is set. Run them against a THROWAWAY container (see Task 1 Step 0), never the dirty dev DB on port 55432. Coverage gate: 80% (`make coverage`).
- Full gate before finishing: `make lint test`.
- Existing agent enrollment flow must be behaviorally unchanged (regression: existing test suites pass untouched except where they enumerate permissions/columns).

## File Structure

- `internal/platform/postgres/migrations/019_onprem_device_provisioning.sql` — new (schema).
- `internal/platform/postgres/store.go` — modify (embed + slice).
- `internal/deployment/onprem/contracts.go` — modify (kind consts, `agent_provision`, Principal/EnrollmentRecord/CredentialRecord extensions, new errors, CredentialStore additions).
- `internal/deployment/onprem/service.go` — modify (Authenticate mapping, exchange passthrough, config limit).
- `internal/deployment/onprem/provisioning.go` — new (DeviceProvisionRequest/ProvisionedAgentCredential types + `ProvisionDeviceAgent`, `ListDeviceProvisionedAgents` on CredentialService + permission-subset helper).
- `internal/deployment/onprem/registry.go` — modify (`AgentProfile.ProvisionedBy`, device enrollment request type, `CreateDeviceEnrollment`, `ListDevices`, `GetDevice`, `RevokeDevice` on RegistryService; RegistryStore interface additions; DeviceSummary/DeviceDetail/DeviceProvisionedAgent/DeviceFilter types).
- `internal/platform/postgres/credentials.go` — modify (ResolveCredential kind-aware, ExchangeEnrollment device branch, RevokeCredential cascade, `ProvisionAgentCredential`, `ListDeviceProvisionedAgents`, cascade helper).
- `internal/platform/postgres/registry.go` — modify (`CreateDeviceEnrollment`, `ListDevices`, `GetDevice`, `RevokeDevice`, `provisioned_by` in agent-profile scans).
- `idl/team_memory.thrift` — modify (structs + 6 service methods).
- `internal/teamnote/transport/httpapi/handler/device_endpoints.go` — new (all 6 handler methods).
- `internal/teamnote/transport/httpapi/handler/onprem_mapping.go`, `onprem_endpoints.go` — modify (DTO mapping, error mapping).
- `main.go`, `.env.example` — modify (env limit).
- Tests alongside each: `internal/platform/postgres/migration_test.go`-style + new `internal/platform/postgres/device_provisioning_test.go`, `internal/deployment/onprem/provisioning_test.go`, handler tests in `internal/teamnote/transport/httpapi/handler/device_endpoints_test.go`.
- Docs: `deployment-instruction.md` §7, `docs/on-prem-identity-frontend-integration.md`, ADR status line.

---

### Task 0: Worktree, branch, ADR commit

The ADR file is **untracked** in the main workspace — it must be copied into the worktree and committed, with status flipped to accepted.

**Files:**
- Create (in worktree): `docs/decisions/2026-07-24-device-scoped-agent-provisioning.md` (copied from main workspace)

- [ ] **Step 1: Create worktree from latest origin/main**

Use the superpowers:using-git-worktrees skill. Base: `origin/main` (`dc199b1`), branch name `feat/device-scoped-agent-provisioning`.

- [ ] **Step 2: Copy the ADR in and mark it accepted**

```bash
cp /Users/toddzheng/Workspace/golang/team-memory/docs/decisions/2026-07-24-device-scoped-agent-provisioning.md <worktree>/docs/decisions/
```

Then edit line 3 of the copied file: `状态：提议` → `状态：已接受`.

- [ ] **Step 3: Commit**

```bash
git add docs/decisions/2026-07-24-device-scoped-agent-provisioning.md
git commit -m "docs: accept device-scoped agent provisioning ADR"
```

---

### Task 1: Migration 019

**Files:**
- Create: `internal/platform/postgres/migrations/019_onprem_device_provisioning.sql`
- Modify: `internal/platform/postgres/store.go` (embed directive ~L14, ordered slice in `Migrate` ~L138-157)
- Test: `internal/platform/postgres/device_provisioning_test.go` (new)

**Interfaces:**
- Produces: columns `agent_credentials.kind/provisioned_by/grantable_permissions`, `agent_enrollments.kind/grantable_permissions`, `onprem_agents.provisioned_by`; audit `actor_kind` accepts `'device'`; `agent_id` may be empty when `kind='device'`.

- [ ] **Step 0: Start a throwaway postgres for this session**

```bash
docker run -d --name tm-plan-pg -e POSTGRES_PASSWORD=test -e POSTGRES_DB=teammemory -p 55499:5432 postgres:16
export TEAM_MEMORY_TEST_POSTGRES_DSN="postgres://postgres:test@localhost:55499/teammemory?sslmode=disable"
```

(If the base image needs pgvector — check `docker-compose` / `make db-up` for the image used; mirror it, e.g. `pgvector/pgvector:pg16` — inspect what `make db-up` starts and use the same image on port 55499.)

- [ ] **Step 1: Write the failing test**

In `internal/platform/postgres/device_provisioning_test.go` (mirror the skip/setup idiom of `migration_test.go` `SetupSuite` L25-35 — create a dedicated schema, run `Migrate` twice to prove replay-safety, then assert the new columns):

```go
package postgres

// suite setup copied from migration_test.go idiom: skip when DSN empty,
// CREATE SCHEMA with sanitized name, pool with search_path, defer drop.

func (s *deviceProvisioningSuite) TestMigration019IsReplaySafeAndAddsDeviceColumns() {
	// Migrate ran once in SetupTest; run again to prove replay on populated DB.
	s.Require().NoError(s.store.Migrate(s.ctx))

	var kind string
	s.Require().NoError(s.pool.QueryRow(s.ctx, `
		SELECT data_type FROM information_schema.columns
		WHERE table_name = 'agent_credentials' AND column_name = 'kind'
	`).Scan(&kind))

	// Device row with empty agent_id must be insertable; agent row with empty agent_id must not.
	_, err := s.pool.Exec(s.ctx, `
		INSERT INTO agent_credentials (credential_id, key_digest, user_id, owner_membership_id,
			agent_id, label, permissions, created_at, kind, grantable_permissions)
		VALUES ('dev-1', decode(repeat('ab',32),'hex'), 'usr-1', $1, '', 'todd-macbook-air',
			'{agent_provision}', now(), 'device', '{observe,search,get}')
	`, s.membershipID) // seed membership in SetupTest like other registry tests do
	s.Require().NoError(err)
	_, err = s.pool.Exec(s.ctx, `
		INSERT INTO agent_credentials (credential_id, key_digest, user_id, owner_membership_id,
			agent_id, label, permissions, created_at)
		VALUES ('bad-1', decode(repeat('cd',32),'hex'), 'usr-1', $1, '', '', '{observe}', now())
	`, s.membershipID)
	s.Require().Error(err) // kind defaults to 'agent' → empty agent_id rejected

	// audit actor_kind accepts 'device'
	_, err = s.pool.Exec(s.ctx, `
		INSERT INTO onprem_audit_events (actor_kind, action, target_kind, target_id, occurred_at)
		VALUES ('device', 'identity.agent.provisioned', 'credential', 'x', now())
	`)
	s.Require().NoError(err)
}
```

Seeding: reuse whatever fixture helper existing postgres registry tests use to create an active user + membership (grep `onprem_memberships` in `internal/platform/postgres/*_test.go` and copy the insert statements into `SetupTest`).

- [ ] **Step 2: Run to verify it fails**

```bash
cd <worktree> && go test ./internal/platform/postgres -run TestDeviceProvisioning -v
```
Expected: FAIL (column `kind` does not exist).

- [ ] **Step 3: Write the migration**

`internal/platform/postgres/migrations/019_onprem_device_provisioning.sql`:

```sql
-- 019_onprem_device_provisioning.sql
-- Device-scoped agent provisioning (ADR docs/decisions/2026-07-24-device-scoped-agent-provisioning.md).
-- Replay-safe: runs on every boot, including on populated databases.

ALTER TABLE agent_credentials
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'agent',
    ADD COLUMN IF NOT EXISTS provisioned_by TEXT,
    ADD COLUMN IF NOT EXISTS grantable_permissions TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE agent_enrollments
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'agent',
    ADD COLUMN IF NOT EXISTS grantable_permissions TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE onprem_agents
    ADD COLUMN IF NOT EXISTS provisioned_by TEXT;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_credentials_kind_check') THEN
        ALTER TABLE agent_credentials
            ADD CONSTRAINT agent_credentials_kind_check CHECK (kind IN ('agent', 'device'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_enrollments_kind_check') THEN
        ALTER TABLE agent_enrollments
            ADD CONSTRAINT agent_enrollments_kind_check CHECK (kind IN ('agent', 'device'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_credentials_provisioned_by_fkey') THEN
        ALTER TABLE agent_credentials
            ADD CONSTRAINT agent_credentials_provisioned_by_fkey
            FOREIGN KEY (provisioned_by) REFERENCES agent_credentials (credential_id) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'onprem_agents_provisioned_by_fkey') THEN
        ALTER TABLE onprem_agents
            ADD CONSTRAINT onprem_agents_provisioned_by_fkey
            FOREIGN KEY (provisioned_by) REFERENCES agent_credentials (credential_id) NOT VALID;
    END IF;
END $$;

-- Device rows carry no agent_id: replace the strict btrim checks with kind-aware ones.
-- The original checks were inline column constraints with auto-generated names, so
-- find them by definition instead of hardcoding the name.
DO $$
DECLARE
    found_name TEXT;
BEGIN
    SELECT conname INTO found_name
    FROM pg_constraint
    WHERE conrelid = 'agent_credentials'::regclass AND contype = 'c'
      AND conname <> 'agent_credentials_agent_id_kind_check'
      AND pg_get_constraintdef(oid) LIKE '%btrim(agent_id)%';
    IF found_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE agent_credentials DROP CONSTRAINT %I', found_name);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_credentials_agent_id_kind_check') THEN
        ALTER TABLE agent_credentials
            ADD CONSTRAINT agent_credentials_agent_id_kind_check
            CHECK (kind = 'device' OR btrim(agent_id) <> '');
    END IF;
END $$;

DO $$
DECLARE
    found_name TEXT;
BEGIN
    SELECT conname INTO found_name
    FROM pg_constraint
    WHERE conrelid = 'agent_enrollments'::regclass AND contype = 'c'
      AND conname <> 'agent_enrollments_agent_id_kind_check'
      AND pg_get_constraintdef(oid) LIKE '%btrim(agent_id)%';
    IF found_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE agent_enrollments DROP CONSTRAINT %I', found_name);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_enrollments_agent_id_kind_check') THEN
        ALTER TABLE agent_enrollments
            ADD CONSTRAINT agent_enrollments_agent_id_kind_check
            CHECK (kind = 'device' OR btrim(agent_id) <> '');
    END IF;
END $$;

-- Audit events may now be performed by a device credential.
DO $$
DECLARE
    found_name TEXT;
BEGIN
    SELECT conname INTO found_name
    FROM pg_constraint
    WHERE conrelid = 'onprem_audit_events'::regclass AND contype = 'c'
      AND conname <> 'onprem_audit_events_actor_kind_device_check'
      AND pg_get_constraintdef(oid) LIKE '%actor_kind%';
    IF found_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE onprem_audit_events DROP CONSTRAINT %I', found_name);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'onprem_audit_events_actor_kind_device_check') THEN
        ALTER TABLE onprem_audit_events
            ADD CONSTRAINT onprem_audit_events_actor_kind_device_check
            CHECK (actor_kind IN ('bootstrap', 'human', 'agent', 'device', 'system'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS agent_credentials_provisioned_by_active_idx
    ON agent_credentials (provisioned_by)
    WHERE provisioned_by IS NOT NULL AND revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS agent_credentials_device_kind_idx
    ON agent_credentials (created_at DESC, credential_id)
    WHERE kind = 'device';

CREATE INDEX IF NOT EXISTS onprem_agents_provisioned_by_idx
    ON onprem_agents (provisioned_by)
    WHERE provisioned_by IS NOT NULL;
```

**Caveat check before committing:** open `015_onprem_credentials.sql` and `017_...sql` and confirm whether the `agent_id` checks are inline (auto-named) or named constraints. If `agent_enrollments` has no `btrim(agent_id)` check at all, the second DO block is a harmless no-op — still verify a device enrollment row with `agent_id=''` inserts cleanly (checked implicitly in Task 3 tests).

Register in `internal/platform/postgres/store.go`: add `019_onprem_device_provisioning.sql` to the `//go:embed` list AND append to the ordered slice in `Migrate`.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/platform/postgres -run 'TestDeviceProvisioning|TestMigration|TestConcurrentMigrations' -v
```
Expected: PASS (including existing migration tests — replay safety on the shared suite schemas).

- [ ] **Step 5: Commit**

```bash
git add internal/platform/postgres/migrations/019_onprem_device_provisioning.sql internal/platform/postgres/store.go internal/platform/postgres/device_provisioning_test.go
git commit -m "feat(onprem): add device credential schema (migration 019)"
```

---

### Task 2: Domain contracts — kind, agent_provision, record extensions

**Files:**
- Modify: `internal/deployment/onprem/contracts.go`
- Modify: `internal/deployment/onprem/registry.go` (AgentProfile only)
- Test: extend `internal/deployment/onprem/service_test.go` (or the existing contracts test file — grep `TestPrincipal` / `HasPermission` for where these live)

**Interfaces (produces — later tasks depend on these exact names):**

```go
// contracts.go
type CredentialKind string

const (
	CredentialKindAgent  CredentialKind = "agent"
	CredentialKindDevice CredentialKind = "device"
)

const PermissionAgentProvision Permission = "agent_provision"

var (
	ErrAgentProvisionConflict     = errors.New("agent is provisioned by another owner")
	ErrDeviceAgentLimitExceeded   = errors.New("device active agent limit exceeded")
)

// Principal gains:
//   Kind                 CredentialKind   (zero value "" must behave as agent)
//   GrantablePermissions []Permission
// EnrollmentRecord gains:
//   Kind                 CredentialKind
//   GrantablePermissions []Permission
// CredentialRecord gains:
//   Kind                 CredentialKind
//   ProvisionedBy        string
//   GrantablePermissions []Permission
// registry.go AgentProfile gains:
//   ProvisionedBy string
```

- [ ] **Step 1: Write failing tests** — a unit test asserting `PermissionAgentProvision == Permission("agent_provision")`, that `validateExplicitPermissions([]Permission{PermissionAgentProvision})` returns an error (agent_provision is NOT agent-grantable), and that `validateEnrollmentRequest` rejects it likewise.

```go
func TestAgentProvisionIsNotAgentGrantable(t *testing.T) {
	_, err := validateExplicitPermissions([]Permission{PermissionAgentProvision})
	require.Error(t, err)
	_, _, err = validateEnrollmentRequest(EnrollmentRequest{
		UserID: "u", AgentID: "a", Permissions: []Permission{PermissionAgentProvision},
	})
	require.Error(t, err)
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/deployment/onprem -run TestAgentProvision -v` → FAIL (undefined: PermissionAgentProvision).

- [ ] **Step 3: Implement** the additions above. Do NOT touch the accept-lists in the two validators — the test passes precisely because `agent_provision` stays out of them.

- [ ] **Step 4: Run** — `go test ./internal/deployment/onprem/... -v` → PASS, whole package still compiles.

- [ ] **Step 5: Commit** — `git commit -m "feat(onprem): add credential kind and agent_provision permission"`

---

### Task 3: Device enrollment create + exchange + authenticate

**Files:**
- Modify: `internal/deployment/onprem/registry.go` (service + RegistryStore interface)
- Modify: `internal/deployment/onprem/service.go` (Authenticate mapping)
- Modify: `internal/platform/postgres/registry.go` (`CreateDeviceEnrollment`)
- Modify: `internal/platform/postgres/credentials.go` (`ExchangeEnrollment` device branch, `lockEnrollmentExchangeOwner` returns kind, `ResolveCredential` kind-aware)
- Tests: `internal/deployment/onprem/registry_test.go` (unit, fake store), `internal/platform/postgres/device_provisioning_test.go` (integration)

**Interfaces:**
- Consumes: Task 2 types.
- Produces:

```go
// registry.go
type DeviceEnrollmentRequest struct {
	DeviceName           string
	GrantablePermissions []Permission // empty → registry's configured member-grantable set
	ExpiresIn            time.Duration
}

func (s *RegistryService) CreateDeviceEnrollment(ctx context.Context, principal HumanPrincipal, request DeviceEnrollmentRequest) (Enrollment, error)

// RegistryStore interface addition:
CreateDeviceEnrollment(context.Context, string, EnrollmentRecord) error // (membershipID, record)

// RegistryService gains field grantableList []Permission (config order, deduped),
// set in NewRegistryService next to the existing map.
```

**Service semantics (write exactly):**
- `authorizeHumanAdmin(principal)` — Owner/Admin only (ADR §3).
- Device name: trim; required, ≤200 chars, reject control chars — new `validateDeviceName(name string) error` mirroring `validateAgentIdentity`'s loop.
- Grantable: empty → copy `s.grantableList`; otherwise `validateExplicitPermissions` + every permission must be in `s.grantable` (same error text pattern as `CreateEnrollment` L328-332).
- ExpiresIn: 0 → `defaultEnrollmentTTL`; negative → `ErrInvalidIdentityInput` wrap.
- Record: `EnrollmentRecord{Kind: CredentialKindDevice, AgentID: "", CredentialLabel: deviceName, Permissions: []Permission{PermissionAgentProvision}, GrantablePermissions: grantable, UserID/MembershipID from principal, TokenDigest/DigestKeyVersion/CreatedAt/ExpiresAt exactly as CreateEnrollment does (reuse enrollmentToken + digester)}`.

**Store `CreateDeviceEnrollment` (postgres/registry.go):** transactional like `CreateOwnedEnrollment` but no agent lock — verify membership active (`FOR UPDATE` on memberships/users as in `lockEnrollmentExchangeOwner` L173-186), then:

```sql
INSERT INTO agent_enrollments (
    enrollment_id, token_digest, user_id, membership_id, agent_id,
    credential_label, permissions, created_at, expires_at, credential_expires_at,
    digest_key_version, kind, grantable_permissions
) VALUES ($1, $2, $3, $4, '', $5, $6, $7, $8, NULL, $9, 'device', $10)
```

plus `insertAuditEvent(ctx, tx, "human", record.UserID, record.MembershipID, "", "", "identity.enrollment.created", "enrollment", record.ID, record.CreatedAt)`.

**Exchange device branch (postgres/credentials.go):**
- `lockEnrollmentExchangeOwner` → also `SELECT kind`; return `(membershipID, agentID, kind, error)`; skip the `onprem_agents` lock when `kind='device'`.
- In `ExchangeEnrollment`: consume query additionally `RETURNING kind, grantable_permissions`; when device: skip the `onprem_agent_identities` claim; insert credential with `kind='device'`, `agent_id=''`, `grantable_permissions`, `label` = enrollment `credential_label`; audit actor_kind `'device'` for `identity.credential.issued`.
- Credential INSERT statements (exchange + rotate) gain columns `kind, provisioned_by, grantable_permissions` with values from the record (`nullableText(credential.ProvisionedBy)`).

**`ResolveCredential` rewrite (kind-aware; replaces the hard join on onprem_agents):**

```sql
UPDATE agent_credentials credentials
SET last_used_at = $3
FROM onprem_memberships memberships
JOIN onprem_users users ON users.user_id = memberships.user_id
WHERE ($1 = '' OR credentials.credential_id = $1) AND credentials.key_digest = $2
  AND credentials.owner_membership_id = memberships.membership_id
  AND credentials.user_id = memberships.user_id
  AND credentials.revoked_at IS NULL
  AND (credentials.expires_at IS NULL OR credentials.expires_at > $3)
  AND memberships.status = 'active'
  AND users.identity_status IN ('active', 'unclaimed')
  AND (
      credentials.kind = 'device'
      OR EXISTS (
          SELECT 1 FROM onprem_agents agents
          WHERE agents.agent_id = credentials.agent_id
            AND agents.owner_membership_id = credentials.owner_membership_id
            AND agents.status = 'active'
      )
  )
RETURNING credentials.credential_id, credentials.key_digest, credentials.user_id,
          credentials.owner_membership_id, credentials.agent_id, credentials.label,
          credentials.permissions, credentials.created_at, credentials.expires_at,
          credentials.revoked_at, credentials.last_used_at,
          COALESCE(credentials.rotated_from_credential_id, ''),
          credentials.kind, credentials.grantable_permissions,
          COALESCE(credentials.provisioned_by, '')
```

Scan the three new fields into `record.Kind` / `record.GrantablePermissions` / `record.ProvisionedBy`.

**`Authenticate` (service.go):** map `Kind: record.Kind` and `GrantablePermissions: append([]Permission(nil), record.GrantablePermissions...)` into the returned Principal.

- [ ] **Step 1: Failing unit tests** (registry_test.go, fake store): Member role → ErrForbidden; empty device name → error; grantable superset of config → error; happy path record has Kind device, Permissions exactly `{agent_provision}`, AgentID "".
- [ ] **Step 2: Failing integration test** (device_provisioning_test.go): full loop — create device enrollment via RegistryService with real postgres store → exchange token via CredentialService → `Authenticate(apiKey)` returns Principal with `Kind == CredentialKindDevice`, `HasPermission(PermissionAgentProvision)`, and NOT observe/search/get; second exchange of same token fails; regression: an ordinary agent enrollment exchange still works and its Principal has `Kind == CredentialKindAgent` (empty-string kind must not appear).
- [ ] **Step 3: Run both** → FAIL (methods undefined).
- [ ] **Step 4: Implement** as specced above.
- [ ] **Step 5: Run** `go test ./internal/deployment/onprem/... ./internal/platform/postgres -v` → PASS.
- [ ] **Step 6: Commit** — `git commit -m "feat(onprem): device enrollment create, exchange, and authentication"`

---

### Task 4: Thrift IDL + codegen

**Files:**
- Modify: `idl/team_memory.thrift`
- Generated: `internal/teamnote/transport/httpapi/model/...`, `router/...`, handler stubs

- [ ] **Step 1: Add structs** (before the `service` block, near the other agent-registry structs):

```thrift
struct CreateDeviceEnrollmentRequest {
  1: required string device_name (api.body="device_name")
  2: optional list<string> grantable_permissions (api.body="grantable_permissions")
  3: optional i64 expires_in_seconds (api.body="expires_in_seconds")
}

struct DeviceEnrollmentResponse {
  1: required string enrollment_id
  2: required string token
  3: required string expires_at
  4: required string device_name
  5: required list<string> grantable_permissions
}

struct ProvisionDeviceAgentRequest {
  1: required string agent_id (api.body="agent_id")
  2: required string display_name (api.body="display_name")
  3: required string agent_type (api.body="agent_type")
  4: optional list<string> permissions (api.body="permissions")
}

struct ProvisionDeviceAgentResponse {
  1: required string credential_id
  2: required string api_key
  3: required string agent_id
  4: required list<string> permissions
  5: required string created_at
  6: optional string expires_at
  7: optional string rotated_from_credential_id
  8: required bool agent_created
}

struct ListDeviceProvisionsRequest {}

struct DeviceProvisionedAgent {
  1: required string agent_id
  2: required string display_name
  3: required string agent_type
  4: required string agent_status
  5: required string credential_id
  6: required string created_at
  7: optional string revoked_at
  8: optional string last_used_at
}

struct ListDeviceProvisionsResponse {
  1: required list<DeviceProvisionedAgent> agents
}

struct ListDevicesRequest {
  1: optional string status (api.query="status")
  2: optional i32 limit (api.query="limit")
  3: optional string cursor (api.query="cursor")
}

struct DeviceSummary {
  1: required string credential_id
  2: required string device_name
  3: required string created_by_user_id
  4: required string created_by_membership_id
  5: required string status
  6: required i64 provisioned_agent_count
  7: required string created_at
  8: optional string revoked_at
  9: optional string last_used_at
  10: required list<string> grantable_permissions
}

struct ListDevicesResponse {
  1: required list<DeviceSummary> devices
  2: optional string next_cursor
}

struct DeviceByIDRequest {
  1: required string credential_id (api.path="credential_id")
}

struct DeviceDetailResponse {
  1: required DeviceSummary device
  2: required list<DeviceProvisionedAgent> agents
}

struct DeviceSummaryResponse {
  1: required DeviceSummary device
}
```

Also: `AgentProfile` struct gains `13: optional string provisioned_by`; `AgentCredentialResponse` gains `4: optional string kind`.

- [ ] **Step 2: Add service methods** (inside `service TeamMemoryService`, keep grouping near the other onprem routes):

```thrift
  DeviceEnrollmentResponse CreateDeviceEnrollment(1: CreateDeviceEnrollmentRequest request) (api.post="/v1/me/device-enrollments")
  ProvisionDeviceAgentResponse ProvisionDeviceAgent(1: ProvisionDeviceAgentRequest request) (api.post="/v1/device/agent-provisions")
  ListDeviceProvisionsResponse ListDeviceProvisions(1: ListDeviceProvisionsRequest request) (api.get="/v1/device/agent-provisions")
  ListDevicesResponse ListAdminDevices(1: ListDevicesRequest request) (api.get="/v1/admin/devices")
  DeviceDetailResponse GetAdminDevice(1: DeviceByIDRequest request) (api.get="/v1/admin/devices/:credential_id")
  DeviceSummaryResponse RevokeAdminDevice(1: DeviceByIDRequest request) (api.delete="/v1/admin/devices/:credential_id")
```

- [ ] **Step 3: Generate + compile**

```bash
make generate
go build ./...
```
Expected: new handler stubs appear (e.g. `provision_device_agent.go`); build fails only on the not-yet-written `Handler` methods — if so, add temporary method stubs returning 501 in `device_endpoints.go` to get `go build ./...` green, they are replaced in Tasks 5-8. Check `git diff --stat` to confirm no unrelated generated churn.

- [ ] **Step 4: Commit** — `git commit -m "feat(idl): device provisioning endpoints"`

---

### Task 5: Provision endpoint (service + store + handler + config)

**Files:**
- Create: `internal/deployment/onprem/provisioning.go`
- Modify: `internal/deployment/onprem/contracts.go` (CredentialStore interface + ProvisionOutcome), `service.go` (`CredentialConfig.DeviceAgentLimit`)
- Modify: `internal/platform/postgres/credentials.go` (`ProvisionAgentCredential`)
- Create: `internal/teamnote/transport/httpapi/handler/device_endpoints.go` (ProvisionDeviceAgent method), modify `onprem_mapping.go`, `onprem_endpoints.go` (error mapping)
- Modify: `main.go` (env `TEAM_MEMORY_DEVICE_AGENT_LIMIT` via `intEnvironment`, default 16, threaded into `CredentialConfig`), `.env.example`
- Tests: `internal/deployment/onprem/provisioning_test.go` (unit, fake store), `internal/platform/postgres/device_provisioning_test.go` (tx semantics), handler table-driven test

**Interfaces:**

```go
// provisioning.go
type DeviceProvisionRequest struct {
	AgentID     string
	DisplayName string
	AgentType   string
	Permissions []Permission
}

type ProvisionedAgentCredential struct {
	CredentialID            string
	APIKey                  string
	AgentID                 string
	Permissions             []Permission
	CreatedAt               time.Time
	ExpiresAt               *time.Time
	RotatedFromCredentialID string
	AgentCreated            bool
}

func (s *CredentialService) ProvisionDeviceAgent(ctx context.Context, principal Principal, request DeviceProvisionRequest) (ProvisionedAgentCredential, error)

// contracts.go
type ProvisionOutcome struct {
	RotatedFromCredentialID string
	AgentCreated            bool
}
// CredentialStore interface addition:
ProvisionAgentCredential(context.Context, string, AgentProfile, CredentialRecord, int, time.Time) (ProvisionOutcome, error)
// (deviceCredentialID, desired profile, pre-minted credential, activeAgentLimit, now)
```

**Service logic (complete):**

```go
func (s *CredentialService) ProvisionDeviceAgent(
	ctx context.Context, principal Principal, request DeviceProvisionRequest,
) (ProvisionedAgentCredential, error) {
	if principal.ScopeID != LocalScopeID || principal.CredentialID == "" ||
		principal.Kind != CredentialKindDevice || !principal.HasPermission(PermissionAgentProvision) {
		return ProvisionedAgentCredential{}, ErrForbidden
	}
	agentID := strings.TrimSpace(request.AgentID)
	displayName := strings.TrimSpace(request.DisplayName)
	if err := validateAgentIdentity(agentID, displayName); err != nil {
		return ProvisionedAgentCredential{}, err
	}
	agentType := strings.TrimSpace(request.AgentType)
	if agentType == "" {
		return ProvisionedAgentCredential{}, fmt.Errorf("%w: agent_type is required", ErrInvalidIdentityInput)
	}
	permissions, err := deviceGrantedPermissions(request.Permissions, principal.GrantablePermissions)
	if err != nil {
		return ProvisionedAgentCredential{}, err
	}
	id, apiKey, record, err := s.newCredential(CredentialRecord{
		UserID: principal.UserID, MembershipID: principal.MembershipID, AgentID: agentID,
		Label: displayName, Permissions: permissions,
		Kind: CredentialKindAgent, ProvisionedBy: principal.CredentialID,
	})
	if err != nil {
		return ProvisionedAgentCredential{}, err
	}
	now := record.CreatedAt
	profile := AgentProfile{
		AgentID: agentID, OwnerMembershipID: principal.MembershipID, OwnerUserID: principal.UserID,
		DisplayName: displayName, AgentType: agentType, Status: AgentStatusActive,
		DirectoryVisible: true, CreatedAt: now, UpdatedAt: now, ResourceVersion: 1,
		ProvisionedBy: principal.CredentialID,
	}
	outcome, err := s.store.ProvisionAgentCredential(
		ctx, principal.CredentialID, profile, record, s.config.DeviceAgentLimit, now,
	)
	if err != nil {
		return ProvisionedAgentCredential{}, fmt.Errorf("provision device agent: %w", err)
	}
	return ProvisionedAgentCredential{
		CredentialID: id, APIKey: apiKey, AgentID: agentID, Permissions: permissions,
		CreatedAt: now, RotatedFromCredentialID: outcome.RotatedFromCredentialID,
		AgentCreated: outcome.AgentCreated,
	}, nil
}

func deviceGrantedPermissions(requested, grantable []Permission) ([]Permission, error) {
	if len(requested) == 0 {
		if len(grantable) == 0 {
			return nil, fmt.Errorf("%w: device has no grantable permissions", ErrForbidden)
		}
		return append([]Permission(nil), grantable...), nil
	}
	validated, err := validateExplicitPermissions(requested)
	if err != nil {
		return nil, err
	}
	allowed := make(map[Permission]struct{}, len(grantable))
	for _, permission := range grantable {
		allowed[permission] = struct{}{}
	}
	for _, permission := range validated {
		if _, ok := allowed[permission]; !ok {
			return nil, fmt.Errorf("%w: permission %q exceeds device grantable set", ErrForbidden, permission)
		}
	}
	return validated, nil
}
```

`NewCredentialService`: `if config.DeviceAgentLimit <= 0 { config.DeviceAgentLimit = 16 }`.

**Store transaction (postgres/credentials.go, complete order of operations):**

1. `BEGIN`; lock the device row:
```sql
SELECT user_id, owner_membership_id FROM agent_credentials
WHERE credential_id = $1 AND kind = 'device' AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > $2)
FOR UPDATE
```
No rows → `onprem.ErrUnauthorized`.
2. Lock membership+user active (same query as `lockEnrollmentExchangeOwner` L173-186) → inactive → `ErrUnauthorized`.
3. Resolve agent: `SELECT COALESCE(provisioned_by,''), status FROM onprem_agents WHERE agent_id=$1 FOR UPDATE`.
   - No rows → **create path**: claim `onprem_agent_identities` exactly like `ExchangeEnrollment` L117-127 (conflict → `ErrAgentProvisionConflict`); `INSERT INTO onprem_agents (agent_id, owner_membership_id, display_name, description, agent_type, status, directory_visible, created_at, updated_at, provisioned_by) VALUES (..., 'active', true, ..., $device)`; audit `identity.agent.created` actor_kind `'device'`; `outcome.AgentCreated = true`.
   - Rows, `provisioned_by == deviceCredentialID` AND status `'active'` → **rotation path**:
```sql
UPDATE agent_credentials SET revoked_at = $3
WHERE agent_id = $1 AND provisioned_by = $2 AND revoked_at IS NULL
RETURNING credential_id
```
Audit each returned id as `identity.credential.revoked` (actor `'device'`); set `outcome.RotatedFromCredentialID` to the newest (first returned when you `ORDER BY created_at DESC` via a CTE, or just take any — there is normally exactly one).
   - Rows with any other `provisioned_by` (including `''` = human-registered) or non-active status → `ErrAgentProvisionConflict`.
4. Cap check:
```sql
SELECT count(DISTINCT agent_id) FROM agent_credentials
WHERE provisioned_by = $1 AND revoked_at IS NULL AND agent_id <> $2
```
`>= limit` → `ErrDeviceAgentLimitExceeded`.
5. Insert the new credential row (same INSERT as exchange, with `kind='agent'`, `provisioned_by=$device`, empty grantable).
6. Audit `identity.agent.provisioned`: `insertAuditEvent(ctx, tx, "device", profile.OwnerUserID, profile.OwnerMembershipID, profile.AgentID, deviceCredentialID, "identity.agent.provisioned", "credential", credential.ID, now)` plus the standard `identity.credential.issued` (actor `'device'`).
7. `COMMIT`.

**Handler (`device_endpoints.go`):** mirror `ExchangeAgentEnrollment`/`RevokeAgentCredential` structure: `h.requireOnPrem`, `principal, ok := h.authorize(ctx, c, onprem.PermissionAgentProvision)`, bind request, map permissions strings ↔ `onprem.Permission`, call `h.credentials.ProvisionDeviceAgent`, respond with `ProvisionDeviceAgentResponse`. The service re-checks `Kind == CredentialKindDevice`, so an agent credential that somehow had the permission still gets `ErrForbidden` → 403.

**Error mapping (`writeOnPremError`):** add `onprem.ErrAgentProvisionConflict` → HTTP 409, code `agent_provision_conflict`; `onprem.ErrDeviceAgentLimitExceeded` → HTTP 422, code `device_agent_limit_exceeded`. Follow the existing switch/errors.Is style and stable-code conventions in that function.

**`main.go`:** in `loadOnPremConfig`, read `TEAM_MEMORY_DEVICE_AGENT_LIMIT` with `intEnvironment("TEAM_MEMORY_DEVICE_AGENT_LIMIT", 16)`, validate range 1..1000 following the `validateOperationsConfig` idiom, assign to `CredentialConfig.DeviceAgentLimit`. Add a commented line to `.env.example`.

- [ ] **Step 1: Failing unit tests** (provisioning_test.go, fake CredentialStore): forbidden matrix (agent-kind principal → ErrForbidden even with agent_provision permission; device principal without permission → ErrForbidden); permission subset enforcement (request exceeding grantable → ErrForbidden; empty request → inherits full grantable set); invalid agent_id / empty agent_type → ErrInvalidIdentityInput; happy path passes limit + provisioned_by into store and returns APIKey with `tm_key_` prefix.
- [ ] **Step 2: Failing integration tests** (postgres): create → row in `onprem_agents` with `provisioned_by`; re-provision same agent → old credential revoked_at set, new active, `RotatedFromCredentialID` = old id; provision an agent_id that a human registered → `ErrAgentProvisionConflict`; provision from device B an agent of device A → conflict; fill to limit (use limit=2 in test) → third distinct agent → `ErrDeviceAgentLimitExceeded`; audit rows exist with actor_kind `'device'`.
- [ ] **Step 3: Failing handler table-driven test**: 401 no bearer; 403 agent credential; 200 device credential with full body assertions; 409 conflict; 422 limit — wire fake service per existing `onPremHandlerSuite` pattern in `handler_test.go`.
- [ ] **Step 4: Run all three → FAIL, then implement, then PASS.**

```bash
go test ./internal/deployment/onprem/... ./internal/platform/postgres ./internal/teamnote/transport/httpapi/handler -run 'Provision|Device' -v
```
- [ ] **Step 5: Commit** — `git commit -m "feat(onprem): device agent provisioning endpoint"`

---

### Task 6: Device self-view — GET /v1/device/agent-provisions

**Files:**
- Modify: `internal/deployment/onprem/provisioning.go` + `contracts.go` (store interface), `internal/platform/postgres/credentials.go`, `device_endpoints.go`
- Test: extend the three test files from Task 5

**Interfaces:**

```go
// contracts.go — used by both this endpoint and Task 8's admin detail:
type DeviceProvisionedAgent struct {
	AgentID              string
	DisplayName          string
	AgentType            string
	AgentStatus          AgentStatus
	CredentialID         string
	CreatedAt            time.Time
	RevokedAt            *time.Time
	LastUsedAt           *time.Time
}
// CredentialStore interface addition:
ListDeviceProvisionedAgents(context.Context, string) ([]DeviceProvisionedAgent, error)

func (s *CredentialService) ListDeviceProvisionedAgents(ctx context.Context, principal Principal) ([]DeviceProvisionedAgent, error)
```

Service guard identical to `ProvisionDeviceAgent`'s. Store query:

```sql
SELECT agents.agent_id, agents.display_name, agents.agent_type, agents.status,
       credentials.credential_id, credentials.created_at, credentials.revoked_at, credentials.last_used_at
FROM agent_credentials credentials
JOIN onprem_agents agents ON agents.agent_id = credentials.agent_id
WHERE credentials.provisioned_by = $1
ORDER BY agents.agent_id, credentials.created_at DESC
```

Return every credential row (portal cascade preview wants revoked history too; the handler for THIS endpoint filters to active-only? No — return all, ordered; paxl can filter). Handler: bearer + `PermissionAgentProvision`, map to `ListDeviceProvisionsResponse`.

- [ ] **Steps: failing test (device sees its two provisioned agents, not another device's; agent credential → 403) → implement → PASS → commit** `git commit -m "feat(onprem): device provision listing"`

---

### Task 7: Cascade revocation + admin device revoke

**Files:**
- Modify: `internal/platform/postgres/credentials.go` (cascade helper + `RevokeCredential`), `internal/platform/postgres/registry.go` (`RevokeDevice`), `internal/deployment/onprem/registry.go` (service + store interface), `device_endpoints.go` (RevokeAdminDevice handler)
- Tests: postgres + handler

**Interfaces:**

```go
// postgres/credentials.go
func cascadeRevokeProvisionedCredentials(ctx context.Context, tx pgx.Tx, deviceCredentialID string, now time.Time) (int64, error)

// onprem/registry.go
func (s *RegistryService) RevokeDevice(ctx context.Context, principal HumanPrincipal, credentialID string, idempotencyKey string) (DeviceSummary, error)
// RegistryStore addition:
RevokeDevice(context.Context, HumanPrincipal, string, string, time.Time) (DeviceSummary, error)

// Define DeviceSummary in onprem/registry.go NOW if Task 8 has not run yet
// (Task 8 lists the same struct — do not define it twice):
type DeviceSummary struct {
	CredentialID          string
	DeviceName            string // credentials.label
	CreatedByUserID       string
	CreatedByMembershipID string
	CreatedAt             time.Time
	RevokedAt             *time.Time
	LastUsedAt            *time.Time
	GrantablePermissions  []Permission
	ProvisionedAgentCount int64
}
```

**Cascade helper (complete):**

```go
func cascadeRevokeProvisionedCredentials(
	ctx context.Context, tx pgx.Tx, deviceCredentialID string, now time.Time,
) (int64, error) {
	rows, err := tx.Query(ctx, `
		UPDATE agent_credentials
		SET revoked_at = $2
		WHERE provisioned_by = $1 AND revoked_at IS NULL
		RETURNING credential_id, user_id, owner_membership_id, agent_id
	`, deviceCredentialID, now)
	if err != nil {
		return 0, fmt.Errorf("cascade revoke provisioned credentials: %w", err)
	}
	type revoked struct{ credentialID, userID, membershipID, agentID string }
	var all []revoked
	for rows.Next() {
		var current revoked
		if err := rows.Scan(&current.credentialID, &current.userID, &current.membershipID, &current.agentID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan cascade-revoked credential: %w", err)
		}
		all = append(all, current)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate cascade-revoked credentials: %w", err)
	}
	for _, current := range all {
		if err := insertAuditEvent(ctx, tx, "system", current.userID, current.membershipID,
			current.agentID, deviceCredentialID, "identity.credential.revoked",
			"credential", current.credentialID, now); err != nil {
			return 0, err
		}
	}
	return int64(len(all)), nil
}
```

**Wire-up:**
- `CredentialStore.RevokeCredential` (legacy admin `DELETE /v1/admin/agent-credentials/:id`): change the revoke UPDATE to `RETURNING kind`; when `kind='device'`, call the cascade helper before committing.
- `RegistryStore.RevokeDevice` (new, postgres/registry.go): transactional, mirrors `RevokeOwnedCredential`'s idempotent-replay pattern (L598-621: same actor+key on an already-revoked row → return current state; different key → `ErrIdempotencyConflict`), but selects by `credential_id AND kind='device'` with no agent scoping; on fresh revoke: set `revoked_at`, `revoke_idempotency_*`, audit `identity.credential.revoked` actor `'human'`, then cascade helper. Returns the DeviceSummary (reuse the Task 8 summary query inside the same tx, or a second SELECT after UPDATE).
- Service `RevokeDevice`: `authorizeHumanAdmin` + non-empty credentialID → store.
- Handler `RevokeAdminDevice`: mirror `RevokeAdminAgentCredential` handler (human session + CSRF + `Idempotency-Key` header extraction — copy the exact header/CSRF handling from `identity_registry_endpoints.go` revoke handlers).

- [ ] **Step 1: Failing integration test — the acceptance criterion:** provision agent from device, `Authenticate(agentKey)` succeeds; revoke device via `RegistryStore.RevokeDevice`; `Authenticate(agentKey)` now returns `ErrUnauthorized` AND `Authenticate(deviceKey)` fails; audit shows one device revoke + N cascade rows. Also: legacy path `CredentialStore.RevokeCredential(deviceID)` cascades identically; idempotent replay returns same summary; second call with different key → `ErrIdempotencyConflict`.
- [ ] **Step 2: Failing handler test:** DELETE /v1/admin/devices/:id as Member → 403; as Admin → 200 with device summary; unknown id → 404.
- [ ] **Step 3: Implement → PASS → commit** `git commit -m "feat(onprem): cascade device revocation"`

---

### Task 8: Portal read endpoints — admin devices + provisioned_by on agents

**Files:**
- Modify: `internal/deployment/onprem/registry.go` (DeviceSummary/DeviceDetail/DeviceFilter, ListDevices/GetDevice service methods, store interface)
- Modify: `internal/platform/postgres/registry.go` (queries; add `COALESCE(provisioned_by,'')` to every agent-profile SELECT/scan)
- Modify: `onprem_mapping.go` (agentProfileToAPI gains ProvisionedBy; new deviceSummaryToAPI/deviceProvisionedAgentToAPI), `device_endpoints.go` (ListAdminDevices/GetAdminDevice handlers)
- Tests: postgres + handler

**Interfaces:**

```go
type DeviceFilter struct {
	Status string // "", "active", "revoked"
	Limit  int
	Cursor string
}

type DeviceSummary struct {
	CredentialID          string
	DeviceName            string // credentials.label
	CreatedByUserID       string
	CreatedByMembershipID string
	CreatedAt             time.Time
	RevokedAt             *time.Time
	LastUsedAt            *time.Time
	GrantablePermissions  []Permission
	ProvisionedAgentCount int64
}

type DeviceDetail struct {
	Device DeviceSummary
	Agents []DeviceProvisionedAgent
}

func (s *RegistryService) ListDevices(ctx context.Context, principal HumanPrincipal, filter DeviceFilter) ([]DeviceSummary, error) // authorizeHumanAdmin
func (s *RegistryService) GetDevice(ctx context.Context, principal HumanPrincipal, credentialID string) (DeviceDetail, error)      // authorizeHumanAdmin
// RegistryStore additions:
ListDevices(context.Context, DeviceFilter) ([]DeviceSummary, error)
GetDevice(context.Context, string) (DeviceDetail, error)
```

**List query** (cursor = `created_at|credential_id` keyset like other admin lists — copy the cursor encode/decode helpers already in postgres/registry.go):

```sql
SELECT credentials.credential_id, credentials.label, credentials.user_id,
       credentials.owner_membership_id, credentials.created_at, credentials.revoked_at,
       credentials.last_used_at, credentials.grantable_permissions,
       (SELECT count(DISTINCT p.agent_id) FROM agent_credentials p
        WHERE p.provisioned_by = credentials.credential_id AND p.revoked_at IS NULL)
FROM agent_credentials credentials
WHERE credentials.kind = 'device'
  AND ($1 = '' OR ($1 = 'active' AND credentials.revoked_at IS NULL)
               OR ($1 = 'revoked' AND credentials.revoked_at IS NOT NULL))
ORDER BY credentials.created_at DESC, credentials.credential_id
LIMIT $2
```

Detail = summary by id (`kind='device'` else `ErrCredentialNotFound`) + the Task 6 provisioned-agents query. API `status` field derives: `revoked_at == nil ? "active" : "revoked"`.

`provisioned_by` on agents: add the column to every `onprem_agents` SELECT feeding `AgentProfile` scans in postgres/registry.go (grep `display_name, description, agent_type` to find them all), map into `agentProfileToAPI` as optional (empty → unset).

- [ ] **Step 1: Failing tests:** postgres — two devices, one revoked, counts correct, filter by status, detail lists agents; handler — Member → 403, Admin → 200 shapes, agent list response includes `provisioned_by` for a provisioned agent and omits it for a human-registered one.
- [ ] **Step 2: Implement → PASS → commit** `git commit -m "feat(onprem): admin device views and provisioned_by attribution"`

---

### Task 9: Docs, .env.example, full gate

**Files:**
- Modify: `deployment-instruction.md` §7, `docs/on-prem-identity-frontend-integration.md`, `.env.example`

- [ ] **Step 1: `deployment-instruction.md` §7** — rewrite onboarding to one-per-machine: Owner/Admin creates a **Device Enrollment** in the portal (device name), copy token once, `paxl device connect onprem --url ... --device-name ... --enrollment-token tm_enroll_...`; then per-tool self-provisioning (`paxl channel connect onprem --agent <id>`, `paxm setup --integration team-memory`); third-party clients use `POST /v1/device/agent-provisions` with the Bearer device key. Keep the existing per-agent enrollment flow as the fallback/alternative path (ADR: 纯增量). Document `TEAM_MEMORY_DEVICE_AGENT_LIMIT` in §2's env table.

- [ ] **Step 2: `docs/on-prem-identity-frontend-integration.md`** — add (in Chinese, matching document style): device enrollment creation (`POST /v1/me/device-enrollments`, Owner/Admin, CSRF, request/response fields incl. `grantable_permissions`), Devices list/detail/revoke (`GET /v1/admin/devices`, `GET /v1/admin/devices/:credential_id`, `DELETE /v1/admin/devices/:credential_id` with Idempotency-Key and cascade semantics + cascade-preview guidance = detail's agents array), response models `DeviceSummary`/`DeviceProvisionedAgent`, `provisioned_by` field on `AgentProfile`, and the two new error codes `agent_provision_conflict` (409) / `device_agent_limit_exceeded` (422). Cross-reference TM-WKS-005 acceptance criteria — every field the FE issue names must appear here.

- [ ] **Step 3: Full gate**

```bash
export TEAM_MEMORY_TEST_POSTGRES_DSN="postgres://postgres:test@localhost:55499/teammemory?sslmode=disable"
make lint test
```
Expected: lint clean, coverage ≥ 80%, integration suites pass. Fix fallout (coverage shortfalls → add unit tests for the new mapping/validation branches).

- [ ] **Step 4: Commit** — `git commit -m "docs(onprem): device provisioning onboarding and portal contract"`

---

## Acceptance checklist (from the capsule — verify before finishing)

- [ ] Exchange + provision + list endpoints all live (what `paxl device connect` needs).
- [ ] Revoking a device → its provisioned agent keys immediately 401 (Task 7 test).
- [ ] Existing agent enrollment flow regression-clean (`make test` green with untouched legacy tests).
- [ ] Contract field names match ADR §2/§4 verbatim.
