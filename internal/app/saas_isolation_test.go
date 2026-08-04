package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas"
	"github.com/pax-beehive/pax-nexus/internal/evidencelake"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractionqueue"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	teamruntime "github.com/pax-beehive/pax-nexus/internal/teamnote/runtime"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/suite"
)

// saasIsolationSuite is the M3 acceptance test for the SaaS profile: one
// handler stack built exactly the way internal/app wires it
// (buildSaaSHTTPHandler on real Postgres stores), serving two teams whose
// humans authenticate with real ControlPlane-minted sessions and whose
// agents authenticate with real exchanged credentials. OIDC itself is not
// exercised (its authenticator does network discovery at boot); sessions are
// minted through ControlPlane.Login directly, which is the same code path
// the OIDC callback handler uses.
//
// The scenario is stateful by design and runs as ordered subtests:
// sign-up A and B, create teams, switch current team, enroll agents,
// verify cross-team isolation, run the invitation flow for C, and verify
// scoped reads follow team switches.
type saasIsolationSuite struct {
	suite.Suite
	adminPool    *pgxpool.Pool
	store        *postgres.Store
	schema       string
	queue        *extractionqueue.Queue
	controlPlane *saas.ControlPlane
	engine       *server.Hertz
	runID        string
}

func TestSaaSIsolationSuite(t *testing.T) {
	suite.Run(t, new(saasIsolationSuite))
}

func (s *saasIsolationSuite) SetupSuite() {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		s.T().Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, dsn)
	s.Require().NoError(err)
	s.adminPool = adminPool
	s.schema = fmt.Sprintf("saas_isolation_%d", time.Now().UnixNano())
	_, err = adminPool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{s.schema}.Sanitize())
	s.Require().NoError(err)
	parsed, err := url.Parse(dsn)
	s.Require().NoError(err)
	query := parsed.Query()
	query.Set("search_path", s.schema+",public")
	parsed.RawQuery = query.Encode()
	store, err := postgres.Open(ctx, parsed.String())
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(ctx))
	s.store = store
	s.runID = fmt.Sprintf("%d", time.Now().UnixNano())

	controlPlane, err := saas.NewControlPlane(store.SaaSIdentity(), store.SaaSIdentity(), saas.ControlPlaneConfig{
		SecretPepper: saasTestPepper, SessionTTL: 12 * time.Hour, InvitationTTL: 24 * time.Hour,
	})
	s.Require().NoError(err)
	s.controlPlane = controlPlane
	registry, err := saas.NewRegistry(store.SaaSRegistry(), store.SaaSCredentials(), saas.RegistryConfig{
		SecretPepper: saasTestPepper,
		MemberGrantablePermissions: []onprem.Permission{
			onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet,
		},
	})
	s.Require().NoError(err)
	credentials, err := saas.NewCredentials(store.SaaSCredentials(), saas.CredentialConfig{
		RotationOverlap: 5 * time.Minute, SecretPepper: saasTestPepper,
	})
	s.Require().NoError(err)

	noteStore, err := postgres.NewNoteStore(
		store, teamnote.DefaultTTLPolicy(), teamnote.SystemClock{}, postgres.RetrievalConfig{},
	)
	s.Require().NoError(err)
	runtime, err := teamruntime.New(
		evidencelake.New(store.Sessions()), saasIsolationExtractor{},
		teamruntime.Config{NoteStore: noteStore, Logger: slog.New(slog.DiscardHandler)},
	)
	s.Require().NoError(err)
	s.Require().NoError(extractionqueue.Migrate(ctx, store.Pool()))
	queue, err := extractionqueue.New(store.Pool(), runtime, extractionqueue.Config{
		QueuePrefix: fmt.Sprintf("saas_isolation_extract_%d", time.Now().UnixNano()),
		Shards:      1, MaxAttempts: 1, Debounce: 5 * time.Millisecond, BatchTimeout: 10 * time.Millisecond,
		JobTimeout: 10 * time.Second, SoftStopTimeout: 5 * time.Second,
		Logger: slog.New(slog.DiscardHandler),
	})
	s.Require().NoError(err)
	s.Require().NoError(store.Sessions().ConfigureExtractionEnqueuer(queue))
	s.Require().NoError(queue.Start(ctx))
	s.queue = queue

	teamHandler, err := buildSaaSHTTPHandler(
		ctx, runtime, store, saasIsolationRecorder{}, nil,
		applicationConfig{
			operationsEventRetention: 24 * time.Hour, operationsStorageRetention: 7 * 24 * time.Hour,
			portalURL: "/", humanCookieSecure: false,
		},
		slog.New(slog.DiscardHandler), nil, nil, nil,
		&saasIdentityServices{
			controlPlane: controlPlane, registry: registry, credentials: credentials, oidc: saasTestOIDC{},
		},
	)
	s.Require().NoError(err)
	s.engine = server.New()
	s.engine.Use(handler.InstanceMiddleware(teamHandler))
	router.GeneratedRegister(s.engine)
	_ = recall.Config{} // document the recall router is built inside buildSaaSHTTPHandler
}

func (s *saasIsolationSuite) TearDownSuite() {
	if s.queue != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.NoError(s.queue.Stop(ctx))
		cancel()
	}
	if s.store != nil {
		s.store.Close()
	}
	if s.adminPool != nil {
		_, err := s.adminPool.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{s.schema}.Sanitize()+" CASCADE")
		s.NoError(err)
		s.adminPool.Close()
	}
}

// saasIsolationRecorder satisfies operations.Recorder without writing
// anywhere; operation-event persistence is not what this suite isolates.
type saasIsolationRecorder struct{}

func (saasIsolationRecorder) Record(_ context.Context, event operations.Event) (operations.Event, error) {
	return event, nil
}

// saasTestOIDC satisfies handler.OIDCLifecycle; the login endpoints are not
// exercised in this suite (sessions are minted through ControlPlane.Login).
type saasTestOIDC struct{}

func (saasTestOIDC) BeginLogin() (onprem.OIDCFlow, error) {
	return onprem.OIDCFlow{}, errors.New("oidc is not exercised in this suite")
}

func (saasTestOIDC) CompleteLogin(context.Context, string, string, string) (onprem.ExternalIdentity, error) {
	return onprem.ExternalIdentity{}, errors.New("oidc is not exercised in this suite")
}

// saasIsolationExtractor mirrors twoScopeExtractor: the slice's first event
// becomes one blocker candidate with the event's own content and task_ref.
type saasIsolationExtractor struct{}

func (saasIsolationExtractor) Extract(ctx context.Context, slice evidencelake.Slice) (extractor.Result, error) {
	if err := ctx.Err(); err != nil {
		return extractor.Result{}, fmt.Errorf("extract saas isolation fixture: %w", err)
	}
	if len(slice.Events) == 0 {
		return extractor.Result{}, nil
	}
	event := slice.Events[0]
	return extractor.Result{Candidates: []teamnote.Candidate{{
		ID: "candidate-" + event.ID, Action: teamnote.ActionCreate, Kind: teamnote.KindBlocker,
		Subject: "deployment", Body: event.Content, TaskRef: event.TaskRef,
		Origin: event.Actor, EvidenceEventIDs: []string{event.ID},
	}}}, nil
}

// saasTestUser bundles one human's session with the team artifacts they
// create over the scenario.
type saasTestUser struct {
	sessionToken  string
	principal     onprem.HumanPrincipal
	teamID        string
	agentID       string
	agentAPIKey   string
	membershipID  string
	invitedToken  string
	secretMessage string
}

func (s *saasIsolationSuite) login(label string) saasTestUser {
	email := fmt.Sprintf("%s-%s@example.com", label, s.runID)
	session, err := s.controlPlane.Login(context.Background(), onprem.ExternalIdentity{
		Issuer: "https://issuer.example.com", Subject: label + "-" + s.runID,
		Email: email, EmailVerified: true, DisplayName: label,
	})
	s.Require().NoError(err)
	return saasTestUser{sessionToken: session.Token, principal: session.Principal, secretMessage: label + "-secret-" + s.runID}
}

// csrfFor recomputes the handler's CSRF token convention
// (base64url(sha256("csrf\x00" + session token))) so mutation requests can
// present a matching cookie/header pair.
func csrfFor(sessionToken string) string {
	digest := sha256.Sum256([]byte("csrf\x00" + sessionToken))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func (s *saasIsolationSuite) request(
	method, path, body string, headers ...ut.Header,
) *ut.ResponseRecorder {
	var requestBody *ut.Body
	if body != "" {
		requestBody = &ut.Body{Body: strings.NewReader(body), Len: len(body)}
	}
	allHeaders := append([]ut.Header{{Key: "Content-Type", Value: "application/json"}}, headers...)
	return ut.PerformRequest(s.engine.Engine, method, path, requestBody, allHeaders...)
}

// human performs an authenticated browser request; mutation adds the CSRF
// cookie/header pair.
func (s *saasIsolationSuite) human(user saasTestUser, mutation bool, method, path, body string) *ut.ResponseRecorder {
	cookie := "tm_human_session=" + user.sessionToken
	headers := []ut.Header{{Key: "Cookie", Value: cookie}}
	if mutation {
		csrf := csrfFor(user.sessionToken)
		headers[0] = ut.Header{Key: "Cookie", Value: cookie + "; tm_csrf=" + csrf}
		headers = append(headers, ut.Header{Key: "X-CSRF-Token", Value: csrf})
	}
	return s.request(method, path, body, headers...)
}

func (s *saasIsolationSuite) agent(user saasTestUser, method, path, body string) *ut.ResponseRecorder {
	return s.request(method, path, body, ut.Header{Key: "Authorization", Value: "Bearer " + user.agentAPIKey})
}

// decodeBodyMap decodes a response body into a generic object for
// shape-agnostic assertions (presence checks, booleans, nested fields).
func decodeBodyMap(s *suite.Suite, response *ut.ResponseRecorder) map[string]any {
	var decoded map[string]any
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &decoded))
	return decoded
}

// bodyField decodes a response body and walks the given object path to a
// string field, failing the test on any shape mismatch (errcheck-safe typed
// access for the JSON documents this suite consumes).
func bodyField(s *suite.Suite, response *ut.ResponseRecorder, path ...string) string {
	var decoded any
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &decoded))
	for _, key := range path {
		object, ok := decoded.(map[string]any)
		s.Require().True(ok, "expected a JSON object above %q in %s", key, response.Body.String())
		decoded = object[key]
	}
	value, ok := decoded.(string)
	s.Require().True(ok, "expected a JSON string at %v in %s", path, response.Body.String())
	return value
}

func (s *saasIsolationSuite) createTeam(user *saasTestUser, name string) {
	response := s.human(*user, true, http.MethodPost, "/v1/teams", fmt.Sprintf(`{"name":%q}`, name))
	s.Require().Equal(http.StatusCreated, response.Code, response.Body.String())
	user.teamID = bodyField(&s.Suite, response, "team", "team_id")
}

func (s *saasIsolationSuite) switchTeam(user *saasTestUser, teamID string) {
	response := s.human(*user, true, http.MethodPost, "/v1/me/current-team", fmt.Sprintf(`{"team_id":%q}`, teamID))
	s.Require().Equal(http.StatusOK, response.Code, response.Body.String())
	user.membershipID = bodyField(&s.Suite, response, "membership_id")
}

func (s *saasIsolationSuite) enrollAgent(user *saasTestUser) {
	create := s.human(*user, true, http.MethodPost, "/v1/me/agents",
		fmt.Sprintf(`{"agent_id":%q,"display_name":"Bot"}`, "agent-"+user.teamID))
	s.Require().Equal(http.StatusCreated, create.Code, create.Body.String())
	user.agentID = "agent-" + user.teamID

	enroll := s.human(*user, true, http.MethodPost, "/v1/me/agents/"+user.agentID+"/enrollments",
		`{"credential_label":"e2e","permissions":["observe","search","get"]}`)
	s.Require().Equal(http.StatusCreated, enroll.Code, enroll.Body.String())
	token := bodyField(&s.Suite, enroll, "token")

	exchange := s.request(http.MethodPost, "/v1/agent-enrollments/exchange",
		fmt.Sprintf(`{"token":%q}`, token))
	s.Require().Equal(http.StatusOK, exchange.Code, exchange.Body.String())
	user.agentAPIKey = bodyField(&s.Suite, exchange, "api_key")
}

func (s *saasIsolationSuite) TestSaaSProfileIsolatesTeamsOverHTTP() {
	ctx := context.Background()
	userA := s.login("alice")
	userB := s.login("bob")

	s.Run("fresh sign-up has no team and no current team", func() {
		me := s.human(userA, false, http.MethodGet, "/v1/me", "")
		s.Require().Equal(http.StatusOK, me.Code)
		body := me.Body.String()
		s.NotContains(body, `"current_team_id"`)
		s.NotContains(body, `"membership_id"`)
	})

	s.Run("users create their teams", func() {
		s.createTeam(&userA, "Alpha Team")
		s.createTeam(&userB, "Beta Team")
		s.NotEqual(userA.teamID, userB.teamID)
	})

	s.Run("created team is not current until switched", func() {
		me := s.human(userA, false, http.MethodGet, "/v1/me", "")
		body := me.Body.String()
		s.Contains(body, `"team_id":"`+userA.teamID+`"`)
		s.NotContains(body, `"current_team_id"`)
		s.switchTeam(&userA, userA.teamID)
		s.switchTeam(&userB, userB.teamID)
		me = s.human(userA, false, http.MethodGet, "/v1/me", "")
		s.Contains(me.Body.String(), `"current_team_id":"`+userA.teamID+`"`)
	})

	s.Run("each team enrolls an agent credential", func() {
		s.enrollAgent(&userA)
		s.enrollAgent(&userB)
	})

	s.Run("member lists only show the current team", func() {
		membersA := s.human(userA, false, http.MethodGet, "/v1/admin/members", "")
		s.Require().Equal(http.StatusOK, membersA.Code)
		s.Contains(membersA.Body.String(), userA.membershipID)
		s.NotContains(membersA.Body.String(), userB.membershipID)
		s.Require().NotEmpty(userA.membershipID, "switch must surface the membership for these assertions")
	})

	s.Run("agent of another team is not found", func() {
		response := s.human(userA, false, http.MethodGet, "/v1/me/agents/"+userB.agentID, "")
		s.Equal(http.StatusNotFound, response.Code, response.Body.String())
	})

	s.Run("switching into a foreign team is forbidden", func() {
		response := s.human(userA, true, http.MethodPost, "/v1/me/current-team",
			fmt.Sprintf(`{"team_id":%q}`, userB.teamID))
		s.Equal(http.StatusForbidden, response.Code)
		s.Contains(response.Body.String(), "not_team_member")
	})

	s.Run("agent credentials only write and recall their own team's notes", func() {
		now := time.Now().UTC()
		for _, user := range []saasTestUser{userA, userB} {
			body := fmt.Sprintf(`{
			  "events":[{
			    "id":"event-%s",
			    "actor":{"user_id":"owner","agent_id":"%s","session_id":"session-%s"},
			    "sequence":1,
			    "type":"message",
			    "content":%q,
			    "task_ref":"shared-release",
			    "occurred_at":%q
			  }],
			  "complete":true
			}`, user.teamID, user.agentID, user.teamID, user.secretMessage, now.Format(time.RFC3339Nano))
			response := s.agent(user, http.MethodPost, "/v1/session-batches", body)
			s.Require().Equal(http.StatusOK, response.Code, response.Body.String())
		}
		s.Require().Eventually(func() bool {
			var countA, countB int64
			if err := s.store.Pool().QueryRow(ctx,
				`SELECT count(*) FROM team_notes WHERE scope_id = $1`, userA.teamID).Scan(&countA); err != nil {
				return false
			}
			if err := s.store.Pool().QueryRow(ctx,
				`SELECT count(*) FROM team_notes WHERE scope_id = $1`, userB.teamID).Scan(&countB); err != nil {
				return false
			}
			return countA == 1 && countB == 1
		}, 10*time.Second, 25*time.Millisecond, "each team's extraction did not land exactly one note")

		recall := func(user saasTestUser) string {
			body := fmt.Sprintf(`{
			  "actor":{"user_id":"owner","agent_id":"%s","session_id":"recall-%s"},
			  "task_ref":"shared-release",
			  "token_budget":256,
			  "query":"secret",
			  "max_items":3
			}`, user.agentID, user.teamID)
			response := s.agent(user, http.MethodPost, "/v1/notes/recall", body)
			s.Require().Equal(http.StatusOK, response.Code, response.Body.String())
			return response.Body.String()
		}
		s.Contains(recall(userA), userA.secretMessage)
		s.NotContains(recall(userA), userB.secretMessage)
		s.Contains(recall(userB), userB.secretMessage)
		s.NotContains(recall(userB), userA.secretMessage)
	})

	s.Run("audit events are filtered to the current team", func() {
		events := s.human(userA, false, http.MethodGet, "/v1/admin/audit-events", "")
		s.Require().Equal(http.StatusOK, events.Code)
		body := events.Body.String()
		s.Contains(body, userA.teamID)
		s.NotContains(body, userB.teamID)
	})

	userC := s.login("carol")
	s.Run("invitation flow adds C to team A over HTTP", func() {
		invitation := s.human(userA, true, http.MethodPost, "/v1/admin/invitations",
			fmt.Sprintf(`{"target_email":"carol-%s@example.com","role":"member"}`, s.runID))
		s.Require().Equal(http.StatusCreated, invitation.Code, invitation.Body.String())
		userC.invitedToken = bodyField(&s.Suite, invitation, "token")

		accept := s.request(http.MethodPost, "/v1/invitations/accept",
			fmt.Sprintf(`{"token":%q}`, userC.invitedToken),
			ut.Header{Key: "Content-Type", Value: "application/json"},
			ut.Header{Key: "Cookie", Value: "tm_human_session=" + userC.sessionToken + "; tm_csrf=" + csrfFor(userC.sessionToken)},
			ut.Header{Key: "X-CSRF-Token", Value: csrfFor(userC.sessionToken)},
			ut.Header{Key: "Idempotency-Key", Value: "accept-carol-" + s.runID},
		)
		s.Require().Equal(http.StatusOK, accept.Code, accept.Body.String())
		userC.membershipID = bodyField(&s.Suite, accept, "membership_id")

		me := s.human(userC, false, http.MethodGet, "/v1/me", "")
		body := me.Body.String()
		s.Contains(body, `"team_id":"`+userA.teamID+`"`)
		s.Contains(body, `"current_team_id":"`+userA.teamID+`"`,
			"accepting switches the session to the invited team")
	})

	s.Run("accept replay with the same idempotency key succeeds", func() {
		replay := s.request(http.MethodPost, "/v1/invitations/accept",
			fmt.Sprintf(`{"token":%q}`, userC.invitedToken),
			ut.Header{Key: "Content-Type", Value: "application/json"},
			ut.Header{Key: "Cookie", Value: "tm_human_session=" + userC.sessionToken + "; tm_csrf=" + csrfFor(userC.sessionToken)},
			ut.Header{Key: "X-CSRF-Token", Value: csrfFor(userC.sessionToken)},
			ut.Header{Key: "Idempotency-Key", Value: "accept-carol-" + s.runID},
		)
		s.Require().Equal(http.StatusOK, replay.Code, replay.Body.String())
		s.Equal(userC.membershipID, bodyField(&s.Suite, replay, "membership_id"))
	})

	s.Run("scoped reads follow the current team switch", func() {
		carolTeamAMembership := userC.membershipID
		s.Require().NotEmpty(carolTeamAMembership)

		s.createTeam(&userC, "Carol Team")
		s.switchTeam(&userC, userC.teamID)
		members := s.human(userC, false, http.MethodGet, "/v1/admin/members", "")
		s.Require().Equal(http.StatusOK, members.Code)
		s.Contains(members.Body.String(), userC.membershipID, "C is the owner of their own team")
		s.NotContains(members.Body.String(), userA.membershipID)
		s.NotContains(members.Body.String(), carolTeamAMembership)

		s.switchTeam(&userC, userA.teamID)
		me := s.human(userC, false, http.MethodGet, "/v1/me", "")
		s.Require().Equal(http.StatusOK, me.Code)
		s.Contains(me.Body.String(), `"current_team_id":"`+userA.teamID+`"`)
		s.Contains(me.Body.String(), `"membership_id":"`+carolTeamAMembership+`"`)
		// As a plain member of team A, C can use member-level surfaces:
		// listing their own agents (none in team A) works, while the
		// admin-gated members list is forbidden (asserted below).
		agents := s.human(userC, false, http.MethodGet, "/v1/me/agents", "")
		s.Require().Equal(http.StatusOK, agents.Code, agents.Body.String())
		s.NotContains(agents.Body.String(), userA.agentID)
	})

	s.Run("member C cannot see owner-only admin surfaces of team A", func() {
		// C is a member (not admin) in team A: invitations admin is forbidden.
		response := s.human(userC, false, http.MethodGet, "/v1/admin/invitations", "")
		s.Equal(http.StatusForbidden, response.Code)
	})

	var deviceAPIKey, provisionedAPIKey, deviceCredentialID string
	s.Run("device enrollment and agent provisioning work in team A", func() {
		enroll := s.human(userA, true, http.MethodPost, "/v1/me/device-enrollments",
			`{"device_name":"workstation"}`)
		s.Require().Equal(http.StatusCreated, enroll.Code, enroll.Body.String())
		enrollBody := decodeBodyMap(&s.Suite, enroll)
		s.Equal("workstation", enrollBody["device_name"])
		s.NotEmpty(enrollBody["grantable_permissions"])

		exchange := s.request(http.MethodPost, "/v1/agent-enrollments/exchange",
			fmt.Sprintf(`{"token":%q}`, enrollBody["token"]))
		s.Require().Equal(http.StatusOK, exchange.Code, exchange.Body.String())
		deviceAPIKey = bodyField(&s.Suite, exchange, "api_key")
		deviceCredentialID = bodyField(&s.Suite, exchange, "credential_id")
		s.Equal(userA.principal.UserID, bodyField(&s.Suite, exchange, "user_id"),
			"paxl device connect needs user_id in the exchange response")
		s.NotEmpty(decodeBodyMap(&s.Suite, exchange)["permissions"])
		s.Equal("device", bodyField(&s.Suite, exchange, "kind"))

		provision := s.request(http.MethodPost, "/v1/device/agent-provisions",
			fmt.Sprintf(`{"agent_id":"paxl-%s","display_name":"paxl","agent_type":"kimi"}`, s.runID),
			ut.Header{Key: "Authorization", Value: "Bearer " + deviceAPIKey})
		s.Require().Equal(http.StatusOK, provision.Code, provision.Body.String())
		provisionBody := decodeBodyMap(&s.Suite, provision)
		s.Equal(true, provisionBody["agent_created"])
		provisionedAPIKey = bodyField(&s.Suite, provision, "api_key")

		provisions := s.request(http.MethodGet, "/v1/device/agent-provisions", "",
			ut.Header{Key: "Authorization", Value: "Bearer " + deviceAPIKey})
		s.Require().Equal(http.StatusOK, provisions.Code, provisions.Body.String())
		s.Contains(provisions.Body.String(), "paxl-"+s.runID)

		devices := s.human(userA, false, http.MethodGet, "/v1/admin/devices", "")
		s.Require().Equal(http.StatusOK, devices.Code)
		s.Contains(devices.Body.String(), "workstation")
	})

	s.Run("provisioned agent credential works in team A and is invisible to team B", func() {
		now := time.Now().UTC()
		secret := "device-secret-" + s.runID
		observe := s.request(http.MethodPost, "/v1/session-batches", fmt.Sprintf(`{
		  "events":[{
		    "id":"event-device-%s",
		    "actor":{"user_id":"owner","agent_id":"paxl-%s","session_id":"session-device-%s"},
		    "sequence":1,
		    "type":"message",
		    "content":%q,
		    "task_ref":"device-release",
		    "occurred_at":%q
		  }],
		  "complete":true
		}`, s.runID, s.runID, s.runID, secret, now.Format(time.RFC3339Nano)),
			ut.Header{Key: "Authorization", Value: "Bearer " + provisionedAPIKey})
		s.Require().Equal(http.StatusOK, observe.Code, observe.Body.String())
		s.Require().Eventually(func() bool {
			var count int64
			if err := s.store.Pool().QueryRow(ctx,
				`SELECT count(*) FROM team_notes WHERE scope_id = $1 AND task_ref = 'device-release'`,
				userA.teamID).Scan(&count); err != nil {
				return false
			}
			return count == 1
		}, 10*time.Second, 25*time.Millisecond, "the device-provisioned agent's note did not land in team A")

		recallBody := fmt.Sprintf(`{
		  "actor":{"user_id":"owner","agent_id":"paxl-%s","session_id":"recall-device-%s"},
		  "task_ref":"device-release",
		  "token_budget":256,
		  "query":"secret",
		  "max_items":3
		}`, s.runID, s.runID)
		recall := s.request(http.MethodPost, "/v1/notes/recall", recallBody,
			ut.Header{Key: "Authorization", Value: "Bearer " + provisionedAPIKey})
		s.Require().Equal(http.StatusOK, recall.Code, recall.Body.String())
		s.Contains(recall.Body.String(), secret)

		crossTeam := s.request(http.MethodPost, "/v1/notes/recall", recallBody,
			ut.Header{Key: "Authorization", Value: "Bearer " + userB.agentAPIKey})
		s.Require().Equal(http.StatusOK, crossTeam.Code, crossTeam.Body.String())
		s.NotContains(crossTeam.Body.String(), secret)

		devices := s.human(userB, false, http.MethodGet, "/v1/admin/devices", "")
		s.Require().Equal(http.StatusOK, devices.Code)
		s.NotContains(devices.Body.String(), "workstation")
	})

	s.Run("device revocation cascades to provisioned credentials and replays", func() {
		revoke := s.request(http.MethodDelete, "/v1/admin/devices/"+deviceCredentialID, "",
			ut.Header{Key: "Cookie", Value: "tm_human_session=" + userA.sessionToken + "; tm_csrf=" + csrfFor(userA.sessionToken)},
			ut.Header{Key: "X-CSRF-Token", Value: csrfFor(userA.sessionToken)},
			ut.Header{Key: "Idempotency-Key", Value: "revoke-device-" + s.runID},
		)
		s.Require().Equal(http.StatusOK, revoke.Code, revoke.Body.String())

		replayed := s.request(http.MethodDelete, "/v1/admin/devices/"+deviceCredentialID, "",
			ut.Header{Key: "Cookie", Value: "tm_human_session=" + userA.sessionToken + "; tm_csrf=" + csrfFor(userA.sessionToken)},
			ut.Header{Key: "X-CSRF-Token", Value: csrfFor(userA.sessionToken)},
			ut.Header{Key: "Idempotency-Key", Value: "revoke-device-" + s.runID},
		)
		s.Require().Equal(http.StatusOK, replayed.Code, replayed.Body.String())

		provisions := s.request(http.MethodGet, "/v1/device/agent-provisions", "",
			ut.Header{Key: "Authorization", Value: "Bearer " + deviceAPIKey})
		s.Equal(http.StatusUnauthorized, provisions.Code, "the revoked device key must not authenticate")
		recall := s.request(http.MethodPost, "/v1/notes/recall", `{"actor":{"user_id":"owner","agent_id":"x","session_id":"y"},"task_ref":"device-release","token_budget":128,"query":"secret","max_items":1}`,
			ut.Header{Key: "Authorization", Value: "Bearer " + provisionedAPIKey})
		s.Equal(http.StatusUnauthorized, recall.Code, "the cascade must kill the provisioned agent key")
	})

	s.Run("saas profile rejects on-prem-only surfaces", func() {
		bootstrap := s.human(userA, true, http.MethodPost, "/v1/bootstrap/claim", "")
		s.Equal(http.StatusNotImplemented, bootstrap.Code)
		s.Contains(bootstrap.Body.String(), "unsupported_profile")

		channel := s.agent(userA, http.MethodPost, "/v1/channel/envelopes",
			`{"recipient_agent_id":"x","payload":{}}`)
		s.Equal(http.StatusNotImplemented, channel.Code)
	})
}
