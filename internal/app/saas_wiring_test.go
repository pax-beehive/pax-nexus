package app

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas"
	"github.com/pax-beehive/pax-nexus/internal/explorer"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	pagewikihttp "github.com/pax-beehive/pax-nexus/internal/pagewiki/transport/httpapi"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

const saasTestPepper = "0123456789abcdef0123456789abcdef"

func (s *configSuite) setValidSaaSEnvironment() {
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	s.T().Setenv("TEAM_MEMORY_SECRET_PEPPER", saasTestPepper)
	s.T().Setenv("TEAM_MEMORY_OIDC_ISSUER", "https://issuer.example.com")
	s.T().Setenv("TEAM_MEMORY_OIDC_CLIENT_ID", "client-id")
	s.T().Setenv("TEAM_MEMORY_OIDC_CLIENT_SECRET", "client-secret")
	s.T().Setenv("TEAM_MEMORY_OIDC_REDIRECT_URL", "https://app.example.com/v1/auth/callback")
	s.T().Setenv("TEAM_MEMORY_OIDC_FLOW_SECRET", "flow-secret")
}

func (s *configSuite) TestLoadsSaaSConfiguration() {
	s.setValidSaaSEnvironment()
	config, err := loadSaaSConfig()
	s.Require().NoError(err)
	s.Equal("postgres://database", config.databaseURL)
	s.Equal("https://issuer.example.com", config.oidcIssuer)
	s.Empty(config.bootstrapSecret)
	s.Empty(config.apiKeys)
}

func (s *configSuite) TestSaaSConfigValidation() {
	cases := []struct {
		name    string
		mutate  func()
		wantErr string
	}{
		{
			name: "api keys are rejected",
			mutate: func() {
				s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
			},
			wantErr: "TEAM_MEMORY_API_KEYS is not supported in the saas profile",
		},
		{
			name: "admin api key is rejected",
			mutate: func() {
				s.T().Setenv("TEAM_MEMORY_ADMIN_API_KEY", "admin-secret")
			},
			wantErr: "TEAM_MEMORY_ADMIN_API_KEY is not supported in the saas profile",
		},
		{
			name: "bootstrap secret is rejected",
			mutate: func() {
				s.T().Setenv("TEAM_MEMORY_BOOTSTRAP_SECRET", "bootstrap-secret")
			},
			wantErr: "TEAM_MEMORY_BOOTSTRAP_SECRET is not supported in the saas profile",
		},
		{
			name: "oidc settings are required together",
			mutate: func() {
				s.T().Setenv("TEAM_MEMORY_OIDC_FLOW_SECRET", "")
			},
			wantErr: "all TEAM_MEMORY_OIDC_* settings are required in the saas profile",
		},
		{
			name: "secret pepper minimum length",
			mutate: func() {
				s.T().Setenv("TEAM_MEMORY_SECRET_PEPPER", "short")
			},
			wantErr: "TEAM_MEMORY_SECRET_PEPPER must contain at least 32 characters",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.setValidSaaSEnvironment()
			tc.mutate()
			_, err := loadSaaSConfig()
			s.Require().ErrorContains(err, tc.wantErr)
		})
	}
}

func (s *configSuite) TestOnPremConfigValidationStillRuns() {
	// The shared base loader must not have picked up the saas rules: the
	// on-prem profile keeps accepting its legacy API-key mode.
	s.T().Setenv("TEAM_MEMORY_DATABASE_URL", "postgres://database")
	s.T().Setenv("TEAM_MEMORY_API_KEYS", `{"key":"scope"}`)
	s.T().Setenv("TEAM_MEMORY_EXTRACTOR_MODE", "noop")
	config, err := loadConfig()
	s.Require().NoError(err)
	s.Equal(map[string]string{"key": "scope"}, config.apiKeys)
}

type saasWiringSuite struct {
	suite.Suite
	store *postgres.Store
	runID string
}

func TestSaaSWiringSuite(t *testing.T) {
	suite.Run(t, new(saasWiringSuite))
}

func (s *saasWiringSuite) SetupSuite() {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		s.T().Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not set")
	}
	store, err := postgres.Open(context.Background(), dsn)
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(context.Background()))
	s.store = store
	// The test database is shared and persistent, so identities and team
	// slugs must be unique per run to keep the suite re-runnable.
	s.runID = fmt.Sprintf("%d", time.Now().UnixNano())
}

func (s *saasWiringSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *saasWiringSuite) newControlPlane() *saas.ControlPlane {
	controlPlane, err := saas.NewControlPlane(s.store.SaaSIdentity(), s.store.SaaSIdentity(), saas.ControlPlaneConfig{
		SecretPepper: saasTestPepper, SessionTTL: time.Hour, InvitationTTL: time.Hour,
	})
	s.Require().NoError(err)
	return controlPlane
}

func (s *saasWiringSuite) TestScopedOperationsService() {
	_, err := newScopedOperationsService(nil, onprem.OperationsConfig{
		EventRetention: 24 * time.Hour, StorageRetention: 7 * 24 * time.Hour,
	})
	s.Require().Error(err)
	_, err = newScopedOperationsService(s.store, onprem.OperationsConfig{})
	s.Require().Error(err)

	service, err := newScopedOperationsService(s.store, onprem.OperationsConfig{
		EventRetention: 24 * time.Hour, StorageRetention: 7 * 24 * time.Hour,
	})
	s.Require().NoError(err)
	owner := onprem.HumanPrincipal{
		UserID: "usr", MembershipID: "mbr", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive,
	}
	s.Run("empty scope is forbidden before any store work", func() {
		_, err := service.Summary(context.Background(), owner, operations.TimeFilter{})
		s.Require().ErrorIs(err, onprem.ErrForbidden)
	})
	s.Run("members fail the delegated capability check", func() {
		member := owner
		member.Role = onprem.RoleMember
		member.ScopeID = "team_scoped_ops"
		_, err := service.Summary(context.Background(), member, operations.TimeFilter{})
		s.Require().ErrorIs(err, onprem.ErrForbidden)
	})
	s.Run("delegates into the principal's team scope", func() {
		owner.ScopeID = "team_scoped_ops"
		events, err := service.ListEvents(context.Background(), owner, operations.EventFilter{Limit: 5})
		s.Require().NoError(err)
		s.Empty(events)
	})
}

// TestScopedOperationsServiceCrossTenantIsolation is the end-to-end proof
// that the tenant-isolation fix (Tasks 1-3 of the operation-events plan)
// actually closes the leak for a real request. Every other test covering
// this table exercises the SQL layer directly against a store already bound
// to one scope; this one drives two DIFFERENT onprem.HumanPrincipal values
// through forPrincipal, which is what resolves a caller's team and binds
// store.Operations(principal.ScopeID) per call in production. A bug in that
// resolution — e.g. forPrincipal ignoring principal.ScopeID and reusing a
// stale service — would leave every SQL-level test green while every
// request still read the wrong team's data.
//
// Both scope IDs are suffixed with s.runID (a nanosecond timestamp) rather
// than being static literals: this suite's database is shared and
// persistent across runs with no per-test schema (unlike
// operationsStoreSuite/saasIsolationSuite, which each create and drop their
// own schema), so a static scope string would risk colliding with rows a
// previous run of this same suite left behind. A scope no run has ever used
// before cannot collide with anything already in the table.
func (s *saasWiringSuite) TestScopedOperationsServiceCrossTenantIsolation() {
	ctx := context.Background()
	service, err := newScopedOperationsService(s.store, onprem.OperationsConfig{
		EventRetention: 24 * time.Hour, StorageRetention: 7 * 24 * time.Hour,
	})
	s.Require().NoError(err)

	scopeA := "ops_isolation_a_" + s.runID
	scopeB := "ops_isolation_b_" + s.runID
	now := time.Now().UTC()

	// Writing goes straight through store.Operations(scopeID).Record: there
	// is no service-layer write method (recording is done by the separate
	// operations.Recorder wrapping the store, not by OperationsService).
	// What this test proves is that READS through forPrincipal only ever
	// see the scope of the principal that called them — that is where the
	// leak Tasks 1-3 closed actually lived.
	recordEvent := func(scopeID, agentID string) {
		attempt, attemptErr := operations.NewAttemptID()
		s.Require().NoError(attemptErr)
		_, recordErr := s.store.Operations(scopeID).Record(ctx, operations.Event{
			ScopeID: scopeID, AttemptID: attempt, Kind: operations.KindObservationObserve,
			Outcome: operations.OutcomeSucceeded, Actor: operations.Actor{Kind: "agent", AgentID: agentID},
			StartedAt: now, CompletedAt: now, InputItems: 1, AcceptedItems: 1,
		})
		s.Require().NoError(recordErr)
	}
	// Two events for A, one for B: unequal counts mean the assertions below
	// cannot pass by coincidence from both sides looking the same.
	recordEvent(scopeA, "agent-a")
	recordEvent(scopeA, "agent-a")
	recordEvent(scopeB, "agent-b")

	principalA := onprem.HumanPrincipal{
		UserID: "usr-a", MembershipID: "mbr-a", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive, ScopeID: scopeA,
	}
	principalB := onprem.HumanPrincipal{
		UserID: "usr-b", MembershipID: "mbr-b", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive, ScopeID: scopeB,
	}

	s.Run("principal A sees only A's events through the service layer", func() {
		summary, err := service.Summary(ctx, principalA, operations.TimeFilter{})
		s.Require().NoError(err)
		s.Equal(int64(2), summary.Observations.Requests)

		events, err := service.ListEvents(ctx, principalA, operations.EventFilter{Limit: 50})
		s.Require().NoError(err)
		s.Require().Len(events, 2)
		for _, event := range events {
			s.Equal("agent-a", event.Actor.AgentID)
		}

		stats, err := service.AgentStats(ctx, principalA, operations.TimeFilter{})
		s.Require().NoError(err)
		s.Require().Len(stats.Agents, 1)
		s.Equal("agent-a", stats.Agents[0].AgentID)
	})

	s.Run("principal B sees only B's event through the service layer", func() {
		summary, err := service.Summary(ctx, principalB, operations.TimeFilter{})
		s.Require().NoError(err)
		s.Equal(int64(1), summary.Observations.Requests)

		events, err := service.ListEvents(ctx, principalB, operations.EventFilter{Limit: 50})
		s.Require().NoError(err)
		s.Require().Len(events, 1)
		s.Equal("agent-b", events[0].Actor.AgentID)

		stats, err := service.AgentStats(ctx, principalB, operations.TimeFilter{})
		s.Require().NoError(err)
		s.Require().Len(stats.Agents, 1)
		s.Equal("agent-b", stats.Agents[0].AgentID)
	})
}

func (s *saasWiringSuite) TestScopedExplorerService() {
	service := newScopedExplorerService(s.store)
	owner := onprem.HumanPrincipal{
		UserID: "usr", MembershipID: "mbr", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive,
	}
	s.Run("empty scope is forbidden", func() {
		_, err := service.ListTeamNotes(context.Background(), owner, explorer.TeamNoteFilter{Limit: 5})
		s.Require().ErrorIs(err, onprem.ErrForbidden)
	})
	s.Run("delegates into the principal's team scope", func() {
		owner.ScopeID = "team_scoped_explorer"
		notes, err := service.ListTeamNotes(context.Background(), owner, explorer.TeamNoteFilter{Limit: 5})
		s.Require().NoError(err)
		s.Empty(notes)
	})
}

// TestWikiScopeResolver drives the Page Wiki scope resolver through the real
// saas services on a real database: sign-up, team creation, team switch,
// then agent enrollment exchange, proving both authentication paths resolve
// the same team scope.
func (s *saasWiringSuite) TestWikiScopeResolver() {
	ctx := context.Background()
	controlPlane := s.newControlPlane()
	credentials, err := saas.NewCredentials(s.store.SaaSCredentials(), saas.CredentialConfig{
		RotationOverlap: time.Minute, SecretPepper: saasTestPepper,
	})
	s.Require().NoError(err)
	registry, err := saas.NewRegistry(s.store.SaaSRegistry(), s.store.SaaSCredentials(), saas.RegistryConfig{
		SecretPepper: saasTestPepper, MemberGrantablePermissions: []onprem.Permission{onprem.PermissionGet},
	})
	s.Require().NoError(err)
	services := &saasIdentityServices{controlPlane: controlPlane, credentials: credentials}
	resolve := saasWikiScopeResolver(services)

	s.Run("unauthenticated requests cannot resolve a scope", func() {
		_, err := resolve(ctx, app.NewContext(0))
		s.Require().ErrorIs(err, pagewikihttp.ErrScopeUnresolved)
	})

	session, err := controlPlane.Login(ctx, onprem.ExternalIdentity{
		Issuer: "https://issuer.example.com", Subject: "wiki-resolver-user-" + s.runID,
		Email: fmt.Sprintf("wiki-%s@example.com", s.runID), EmailVerified: true,
	})
	s.Require().NoError(err)

	s.Run("a session without a current team cannot resolve a scope", func() {
		request := app.NewContext(0)
		request.Request.Header.SetCookie(humanSessionCookieName, session.Token)
		_, err := resolve(ctx, request)
		s.Require().ErrorIs(err, pagewikihttp.ErrScopeUnresolved)
	})

	team, err := controlPlane.CreateTeam(ctx, session.Principal, "Wiki Team "+s.runID, "")
	s.Require().NoError(err)
	switched, err := controlPlane.SwitchTeam(ctx, session.Principal, team.TeamID)
	s.Require().NoError(err)

	s.Run("a session with a current team resolves it", func() {
		request := app.NewContext(0)
		request.Request.Header.SetCookie(humanSessionCookieName, session.Token)
		scope, err := resolve(ctx, request)
		s.Require().NoError(err)
		s.Equal(team.TeamID, scope)
	})

	s.Run("a bearer agent credential resolves its team", func() {
		agent, err := registry.CreateAgent(ctx, switched, onprem.CreateAgentRequest{
			AgentID: "wiki-resolver-agent-" + s.runID, DisplayName: "Wiki Bot",
		})
		s.Require().NoError(err)
		enrollment, err := registry.CreateEnrollment(ctx, switched, agent.AgentID, onprem.OwnerEnrollmentRequest{
			Permissions: []onprem.Permission{onprem.PermissionGet},
		})
		s.Require().NoError(err)
		issued, err := credentials.ExchangeEnrollment(ctx, enrollment.Token)
		s.Require().NoError(err)

		request := app.NewContext(0)
		request.Request.Header.Set("Authorization", "Bearer "+issued.APIKey)
		scope, err := resolve(ctx, request)
		s.Require().NoError(err)
		s.Equal(team.TeamID, scope)
	})

	s.Run("an invalid bearer credential cannot resolve a scope", func() {
		request := app.NewContext(0)
		request.Request.Header.Set("Authorization", "Bearer tm_key_missing.secret")
		_, err := resolve(ctx, request)
		s.Require().ErrorIs(err, pagewikihttp.ErrScopeUnresolved)
	})
}

func (s *saasWiringSuite) TestOnPremWikiScopePin() {
	scope, err := onPremWikiScope(context.Background(), app.NewContext(0))
	s.Require().NoError(err)
	s.Equal(onprem.LocalScopeID, scope)
}

func (s *saasWiringSuite) TestBearerCredentialParsing() {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"bearer key", "Bearer tm_key_abc.secret", "tm_key_abc.secret"},
		{"missing prefix", "tm_key_abc.secret", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			request := app.NewContext(0)
			if tc.header != "" {
				request.Request.Header.Set("Authorization", tc.header)
			}
			s.Equal(tc.want, bearerCredential(request))
		})
	}
}
