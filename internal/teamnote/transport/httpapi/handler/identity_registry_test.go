package handler_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/mocks"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

type identityRegistryHandlerSuite struct {
	suite.Suite
	controller *gomock.Controller
	handler    *handler.Handler
	identity   *humanIdentityService
	registry   *agentRegistryService
}

func TestIdentityRegistryHandlerSuite(t *testing.T) {
	suite.Run(t, new(identityRegistryHandlerSuite))
}

func (s *identityRegistryHandlerSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.identity = &humanIdentityService{principal: onprem.HumanPrincipal{
		UserID: "member-user", MembershipID: "member-membership", Role: onprem.RoleMember,
		MembershipStatus: onprem.MembershipStatusActive, Email: "member@example.com", EmailVerified: true,
	}}
	s.registry = &agentRegistryService{}
	configured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, &channelService{},
		slog.New(slog.DiscardHandler),
		handler.WithAgentRegistry(s.registry),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
	)
	s.Require().NoError(err)
	s.handler = configured
}

func (s *identityRegistryHandlerSuite) TestHumanCreatesAgentThroughGeneratedRoute() {
	response := s.performGenerated(http.MethodPost, "/v1/me/agents",
		`{"agent_id":"reviewer","display_name":"Reviewer","description":"Reviews changes","agent_type":"codex"}`,
		ut.Header{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"},
		ut.Header{Key: "X-CSRF-Token", Value: "csrf"})

	s.Equal(consts.StatusCreated, response.Code)
	s.Equal("reviewer", s.registry.created.AgentID)
	s.True(s.registry.created.DirectoryVisible)
	s.Contains(response.Body.String(), `"agent_id":"reviewer"`)
}

func (s *identityRegistryHandlerSuite) TestHumanMutationRejectsMissingCSRF() {
	response := s.performGenerated(http.MethodPost, "/v1/me/agents",
		`{"agent_id":"reviewer","display_name":"Reviewer"}`,
		ut.Header{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"})

	s.Equal(consts.StatusForbidden, response.Code)
}

func (s *identityRegistryHandlerSuite) TestHumanConflictErrorsHaveStableCodes() {
	headers := []ut.Header{
		{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"},
		{Key: "X-CSRF-Token", Value: "csrf"},
	}
	tests := []struct {
		name     string
		err      error
		wantCode string
	}{
		{name: "last active owner", err: onprem.ErrLastActiveOwner, wantCode: "last_active_owner"},
		{name: "stale resource version", err: onprem.ErrResourceVersionConflict, wantCode: "resource_version_conflict"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.identity.updateMemberErr = test.err
			response := s.performGenerated(http.MethodPatch, "/v1/admin/members/member-membership",
				`{"status":"suspended","resource_version":1}`, headers...)
			s.Equal(consts.StatusConflict, response.Code)
			s.Contains(response.Header().Get("Content-Type"), "application/json")
			s.JSONEq(`{"code":"`+test.wantCode+`","message":"the requested change conflicts with current state"}`,
				response.Body.String())
		})
	}
}

func (s *identityRegistryHandlerSuite) TestAgentCreationConflictErrorsHaveStableCodes() {
	headers := []ut.Header{
		{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"},
		{Key: "X-CSRF-Token", Value: "csrf"},
	}
	tests := []struct {
		name     string
		err      error
		wantCode string
	}{
		{name: "agent ID already exists", err: onprem.ErrAgentIDConflict, wantCode: "agent_id_conflict"},
		{name: "idempotency key reused", err: onprem.ErrIdempotencyConflict, wantCode: "idempotency_conflict"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.registry.createAgentErr = test.err
			response := s.performGenerated(http.MethodPost, "/v1/me/agents",
				`{"agent_id":"reviewer","display_name":"Reviewer"}`, headers...)
			s.Equal(consts.StatusConflict, response.Code)
			s.JSONEq(`{"code":"`+test.wantCode+`","message":"the requested change conflicts with current state"}`,
				response.Body.String())
		})
	}
}

func (s *identityRegistryHandlerSuite) TestAgentListsAndGetsDirectoryProfiles() {
	list := s.performGenerated(http.MethodGet, "/v1/channel/agents", "",
		ut.Header{Key: "Authorization", Value: "Bearer agent"})
	s.Equal(consts.StatusOK, list.Code)
	s.Contains(list.Body.String(), `"agent_id":"receiver"`)
	s.NotContains(list.Body.String(), "owner_user_id")

	get := s.performGenerated(http.MethodGet, "/v1/channel/agents/receiver", "",
		ut.Header{Key: "Authorization", Value: "Bearer agent"})
	s.Equal(consts.StatusOK, get.Code)
	s.Contains(get.Body.String(), `"description":"Receives capsules"`)
}

func (s *identityRegistryHandlerSuite) TestHumanLoginMeAndLogoutRoutes() {
	login := s.performGenerated(http.MethodGet, "/v1/auth/login", "")
	s.Equal(consts.StatusFound, login.Code)
	s.Equal("https://identity.test/login", login.Header().Get("Location"))
	s.Contains(login.Header().Get("Set-Cookie"), "tm_oidc_flow=flow")

	callback := s.performGenerated(http.MethodGet, "/v1/auth/callback?code=code&state=state", "",
		ut.Header{Key: "Cookie", Value: "tm_oidc_flow=flow"})
	s.Equal(consts.StatusFound, callback.Code)
	s.Equal("/portal", callback.Header().Get("Location"))
	s.Contains(callback.Header().Get("Set-Cookie"), "tm_human_session=session")

	me := s.performGenerated(http.MethodGet, "/v1/me", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
	s.Equal(consts.StatusOK, me.Code)
	s.Contains(me.Body.String(), `"membership_id":"member-membership"`)
	s.Contains(me.Body.String(), `"capabilities":[]`)

	logout := s.performGenerated(http.MethodPost, "/v1/auth/logout", `{}`,
		ut.Header{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"},
		ut.Header{Key: "X-CSRF-Token", Value: "csrf"})
	s.Equal(consts.StatusOK, logout.Code)
	s.True(s.identity.loggedOut)
}

func (s *identityRegistryHandlerSuite) TestHumanMePublishesOperationsCapabilityForAdmin() {
	s.identity.principal.Role = onprem.RoleAdmin

	response := s.performGenerated(http.MethodGet, "/v1/me", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})

	s.Equal(consts.StatusOK, response.Code)
	s.Contains(response.Body.String(), `"capabilities":["view.operations"]`)
}

func (s *identityRegistryHandlerSuite) TestInvitationAndMemberAdministrationRoutes() {
	headers := []ut.Header{
		{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"},
		{Key: "X-CSRF-Token", Value: "csrf"},
		{Key: "Idempotency-Key", Value: "invitation-accept-1"},
	}
	claim := s.performGenerated(http.MethodPost, "/v1/bootstrap/claim", `{}`,
		append(headers, ut.Header{Key: "X-PAX-Bootstrap-Secret", Value: "bootstrap"})...)
	s.Equal(consts.StatusOK, claim.Code)

	invitation := s.performGenerated(http.MethodPost, "/v1/admin/invitations",
		`{"target_email":"invitee@example.com","role":"member","expires_in_seconds":3600}`, headers...)
	s.Equal(consts.StatusCreated, invitation.Code)
	s.Contains(invitation.Body.String(), `"token":"tm_invite_test"`)
	invitationList := s.performGenerated(http.MethodGet, "/v1/admin/invitations?limit=1", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
	s.Equal(consts.StatusOK, invitationList.Code)
	s.Contains(invitationList.Body.String(), `"next_cursor":"invitation"`)
	s.NotContains(invitationList.Body.String(), `"token"`)
	revoked := s.performGenerated(http.MethodDelete, "/v1/admin/invitations/invitation", "{}", headers...)
	s.Equal(consts.StatusOK, revoked.Code)
	s.Contains(revoked.Body.String(), `"status":"revoked"`)

	accepted := s.performGenerated(http.MethodPost, "/v1/invitations/accept",
		`{"token":"tm_invite_test"}`, headers...)
	s.Equal(consts.StatusOK, accepted.Code)
	s.Equal("invitation-accept-1", s.identity.acceptedIdempotencyKey)

	list := s.performGenerated(http.MethodGet, "/v1/admin/members?limit=1", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
	s.Equal(consts.StatusOK, list.Code)
	s.Contains(list.Body.String(), `"next_cursor":"member-membership"`)

	get := s.performGenerated(http.MethodGet, "/v1/admin/members/member-membership", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
	s.Equal(consts.StatusOK, get.Code)

	update := s.performGenerated(http.MethodPatch, "/v1/admin/members/member-membership",
		`{"status":"suspended","resource_version":1}`, headers...)
	s.Equal(consts.StatusOK, update.Code)
	s.Contains(update.Body.String(), `"status":"suspended"`)
	auditList := s.performGenerated(http.MethodGet, "/v1/admin/audit-events?limit=1", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
	s.Equal(consts.StatusOK, auditList.Code)
	s.Contains(auditList.Body.String(), `"next_cursor":"1"`)
	auditGet := s.performGenerated(http.MethodGet, "/v1/admin/audit-events/1", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
	s.Equal(consts.StatusOK, auditGet.Code)
	s.Contains(auditGet.Body.String(), `"action":"identity.agent.created"`)
}

func (s *identityRegistryHandlerSuite) TestOwnedAndAdminAgentLifecycleRoutes() {
	headers := []ut.Header{
		{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"},
		{Key: "X-CSRF-Token", Value: "csrf"},
		{Key: "Idempotency-Key", Value: "agent-mutation-1"},
	}
	list := s.performGenerated(http.MethodGet, "/v1/me/agents?limit=1", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
	s.Equal(consts.StatusOK, list.Code)
	s.Contains(list.Body.String(), `"next_cursor":"reviewer"`)

	get := s.performGenerated(http.MethodGet, "/v1/me/agents/reviewer", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
	s.Equal(consts.StatusOK, get.Code)

	update := s.performGenerated(http.MethodPatch, "/v1/me/agents/reviewer",
		`{"display_name":"Updated Reviewer","status":"suspended","resource_version":1}`, headers...)
	s.Equal(consts.StatusOK, update.Code)
	s.Contains(update.Body.String(), `"display_name":"Updated Reviewer"`)

	enrollment := s.performGenerated(http.MethodPost, "/v1/me/agents/reviewer/enrollments",
		`{"credential_label":"laptop","permissions":["channel_receive"],"expires_in_seconds":600}`, headers...)
	s.Equal(consts.StatusCreated, enrollment.Code)
	s.Contains(enrollment.Body.String(), `"token":"tm_enroll_test"`)
	enrollmentList := s.performGenerated(http.MethodGet, "/v1/me/agents/reviewer/enrollments?limit=1", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
	s.Equal(consts.StatusOK, enrollmentList.Code)
	s.Contains(enrollmentList.Body.String(), `"next_cursor":"enrollment"`)
	s.NotContains(enrollmentList.Body.String(), `"token"`)
	enrollmentRevoke := s.performGenerated(
		http.MethodDelete, "/v1/me/agents/reviewer/enrollments/enrollment", "{}", headers...,
	)
	s.Equal(consts.StatusOK, enrollmentRevoke.Code)

	credentialList := s.performGenerated(http.MethodGet, "/v1/me/agents/reviewer/credentials?limit=1", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
	s.Equal(consts.StatusOK, credentialList.Code)
	s.Contains(credentialList.Body.String(), `"credential_id":"credential"`)
	credentialRevoke := s.performGenerated(
		http.MethodDelete, "/v1/me/agents/reviewer/credentials/credential", "{}", headers...,
	)
	s.Equal(consts.StatusOK, credentialRevoke.Code)

	retire := s.performGenerated(http.MethodDelete, "/v1/me/agents/reviewer",
		`{"resource_version":2}`, headers...)
	s.Equal(consts.StatusOK, retire.Code)
	s.Contains(retire.Body.String(), `"status":"retired"`)

	adminList := s.performGenerated(http.MethodGet, "/v1/admin/agents?owner_membership_id=member-membership&limit=1", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
	s.Equal(consts.StatusOK, adminList.Code)
	s.Equal("member-membership", s.registry.adminFilter.OwnerMembershipID)
	adminGet := s.performGenerated(http.MethodGet, "/v1/admin/agents/reviewer", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
	s.Equal(consts.StatusOK, adminGet.Code)
	adminUpdate := s.performGenerated(http.MethodPatch, "/v1/admin/agents/reviewer",
		`{"status":"suspended","resource_version":1}`, headers...)
	s.Equal(consts.StatusOK, adminUpdate.Code)
	adminRetire := s.performGenerated(http.MethodDelete,
		"/v1/admin/agents/reviewer?resource_version=2", "", headers...)
	s.Equal(consts.StatusOK, adminRetire.Code)
	s.Contains(adminRetire.Body.String(), `"status":"retired"`)
	transfer := s.performGenerated(http.MethodPost, "/v1/admin/agents/reviewer/transfer",
		`{"target_membership_id":"other-membership","resource_version":1}`, headers...)
	s.Equal(consts.StatusOK, transfer.Code)
	s.Contains(transfer.Body.String(), `"owner_membership_id":"other-membership"`)
	adminEnrollmentList := s.performGenerated(
		http.MethodGet, "/v1/admin/agents/reviewer/enrollments?limit=1", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"},
	)
	s.Equal(consts.StatusOK, adminEnrollmentList.Code)
	s.Contains(adminEnrollmentList.Body.String(), `"enrollment_id":"enroll-1"`)
	adminEnrollmentRevoke := s.performGenerated(
		http.MethodDelete, "/v1/admin/agents/reviewer/enrollments/enroll-1", "{}", headers...,
	)
	s.Equal(consts.StatusOK, adminEnrollmentRevoke.Code)
	adminCredentialList := s.performGenerated(
		http.MethodGet, "/v1/admin/agents/reviewer/credentials?limit=1", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"},
	)
	s.Equal(consts.StatusOK, adminCredentialList.Code)
	s.Contains(adminCredentialList.Body.String(), `"credential_id":"credential-1"`)
	adminCredentialRevoke := s.performGenerated(
		http.MethodDelete, "/v1/admin/agents/reviewer/credentials/credential-1", "{}", headers...,
	)
	s.Equal(consts.StatusOK, adminCredentialRevoke.Code)
	s.Equal("agent-mutation-1", s.registry.lastIdempotencyKey)
}

func (s *identityRegistryHandlerSuite) performGenerated(
	method string,
	path string,
	body string,
	headers ...ut.Header,
) *ut.ResponseRecorder {
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)
	allHeaders := append([]ut.Header{{Key: "Content-Type", Value: "application/json"}}, headers...)
	var requestBody *ut.Body
	if body != "" {
		requestBody = &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}
	}
	return ut.PerformRequest(hertz.Engine, method, path, requestBody, allHeaders...)
}

type humanIdentityService struct {
	principal              onprem.HumanPrincipal
	loggedOut              bool
	acceptedIdempotencyKey string
	updateMemberErr        error
}

func (s *humanIdentityService) Login(
	context.Context,
	onprem.ExternalIdentity,
) (onprem.HumanSession, error) {
	return onprem.HumanSession{Token: "session", ExpiresAt: time.Now().Add(time.Hour), Principal: s.principal}, nil
}

func (s *humanIdentityService) AuthenticateSession(
	_ context.Context,
	token string,
) (onprem.HumanPrincipal, error) {
	if token != "session" {
		return onprem.HumanPrincipal{}, onprem.ErrUnauthorized
	}
	return s.principal, nil
}

func (s *humanIdentityService) Logout(context.Context, string) error {
	s.loggedOut = true
	return nil
}

func (s *humanIdentityService) ClaimBootstrap(
	context.Context,
	onprem.HumanPrincipal,
	string,
) (onprem.HumanPrincipal, error) {
	return s.principal, nil
}

func (s *humanIdentityService) CreateInvitation(
	context.Context,
	onprem.HumanPrincipal,
	onprem.InvitationRequest,
) (onprem.Invitation, error) {
	now := time.Now()
	return onprem.Invitation{
		InvitationID: "invitation", Token: "tm_invite_test", TargetEmail: "invitee@example.com",
		Role: onprem.RoleMember, Status: onprem.InvitationStatusPending, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}, nil
}

func (s *humanIdentityService) AcceptInvitation(
	_ context.Context,
	_ onprem.HumanPrincipal,
	_ string,
	idempotencyKey string,
) (onprem.HumanPrincipal, error) {
	s.acceptedIdempotencyKey = idempotencyKey
	return s.principal, nil
}

func (s *humanIdentityService) ListInvitations(
	context.Context,
	onprem.HumanPrincipal,
	onprem.InvitationFilter,
) ([]onprem.Invitation, error) {
	now := time.Now()
	return []onprem.Invitation{{
		InvitationID: "invitation", TargetEmail: "invitee@example.com", Role: onprem.RoleMember,
		Status: onprem.InvitationStatusPending, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}}, nil
}

func (s *humanIdentityService) RevokeInvitation(
	context.Context,
	onprem.HumanPrincipal,
	string,
) (onprem.Invitation, error) {
	now := time.Now()
	return onprem.Invitation{
		InvitationID: "invitation", TargetEmail: "invitee@example.com", Role: onprem.RoleMember,
		Status: onprem.InvitationStatusRevoked, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}, nil
}

func (s *humanIdentityService) ListMembers(
	context.Context,
	onprem.HumanPrincipal,
	onprem.MemberFilter,
) ([]onprem.Member, error) {
	return []onprem.Member{testMember(onprem.MembershipStatusActive)}, nil
}

func (s *humanIdentityService) GetMember(
	context.Context,
	onprem.HumanPrincipal,
	string,
) (onprem.Member, error) {
	return testMember(onprem.MembershipStatusActive), nil
}

func (s *humanIdentityService) UpdateMember(
	context.Context,
	onprem.HumanPrincipal,
	string,
	onprem.UpdateMemberRequest,
) (onprem.Member, error) {
	if s.updateMemberErr != nil {
		return onprem.Member{}, s.updateMemberErr
	}
	return testMember(onprem.MembershipStatusSuspended), nil
}

func (s *humanIdentityService) ListAuditEvents(
	context.Context,
	onprem.HumanPrincipal,
	onprem.AuditFilter,
) ([]onprem.AuditEvent, error) {
	return []onprem.AuditEvent{{
		AuditEventID: 1, ActorKind: "human", Action: "identity.agent.created",
		TargetKind: "agent", TargetID: "reviewer", OccurredAt: time.Now(),
	}}, nil
}

func (s *humanIdentityService) GetAuditEvent(
	context.Context,
	onprem.HumanPrincipal,
	int64,
) (onprem.AuditEvent, error) {
	return onprem.AuditEvent{
		AuditEventID: 1, ActorKind: "human", Action: "identity.agent.created",
		TargetKind: "agent", TargetID: "reviewer", OccurredAt: time.Now(),
	}, nil
}

func testMember(status onprem.MembershipStatus) onprem.Member {
	now := time.Now()
	return onprem.Member{
		MembershipID: "member-membership", UserID: "member-user", Email: "member@example.com",
		EmailVerified: true, DisplayName: "Member", Role: onprem.RoleMember, Status: status,
		JoinedAt: now, UpdatedAt: now, ResourceVersion: 2,
	}
}

type oidcService struct{}

func (s *oidcService) BeginLogin() (onprem.OIDCFlow, error) {
	return onprem.OIDCFlow{
		AuthorizationURL: "https://identity.test/login", CookieValue: "flow", ExpiresAt: time.Now().Add(time.Minute),
	}, nil
}

func (s *oidcService) CompleteLogin(
	context.Context,
	string,
	string,
	string,
) (onprem.ExternalIdentity, error) {
	return onprem.ExternalIdentity{}, nil
}

type agentRegistryService struct {
	created            onprem.CreateAgentRequest
	adminFilter        onprem.AgentFilter
	lastIdempotencyKey string
	createAgentErr     error
}

func testAgent(status onprem.AgentStatus, displayName string, version int64) onprem.AgentProfile {
	now := time.Now()
	return onprem.AgentProfile{
		AgentID: "reviewer", OwnerMembershipID: "member-membership", OwnerUserID: "member-user",
		DisplayName: displayName, Description: "Reviews changes", AgentType: "codex", Status: status,
		DirectoryVisible: true, CreatedAt: now, UpdatedAt: now, ResourceVersion: version,
	}
}

func (s *agentRegistryService) CreateAgent(
	_ context.Context,
	_ onprem.HumanPrincipal,
	request onprem.CreateAgentRequest,
) (onprem.AgentProfile, error) {
	if s.createAgentErr != nil {
		return onprem.AgentProfile{}, s.createAgentErr
	}
	s.created = request
	return onprem.AgentProfile{
		AgentID: request.AgentID, DisplayName: request.DisplayName, Description: request.Description,
		AgentType: request.AgentType, DirectoryVisible: request.DirectoryVisible,
		Status: onprem.AgentStatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now(), ResourceVersion: 1,
	}, nil
}

func (s *agentRegistryService) ListOwnedAgents(
	context.Context,
	onprem.HumanPrincipal,
	onprem.AgentFilter,
) ([]onprem.AgentProfile, error) {
	return []onprem.AgentProfile{testAgent(onprem.AgentStatusActive, "Reviewer", 1)}, nil
}

func (s *agentRegistryService) GetOwnedAgent(
	context.Context,
	onprem.HumanPrincipal,
	string,
) (onprem.AgentProfile, error) {
	return testAgent(onprem.AgentStatusActive, "Reviewer", 1), nil
}

func (s *agentRegistryService) UpdateOwnedAgent(
	_ context.Context,
	_ onprem.HumanPrincipal,
	_ string,
	request onprem.UpdateAgentRequest,
) (onprem.AgentProfile, error) {
	displayName := "Reviewer"
	if request.DisplayName != nil {
		displayName = *request.DisplayName
	}
	return testAgent(onprem.AgentStatusSuspended, displayName, 2), nil
}

func (s *agentRegistryService) RetireOwnedAgent(
	_ context.Context,
	_ onprem.HumanPrincipal,
	_ string,
	_ int64,
	idempotencyKey string,
) (onprem.AgentProfile, error) {
	s.lastIdempotencyKey = idempotencyKey
	return testAgent(onprem.AgentStatusRetired, "Updated Reviewer", 3), nil
}

func (s *agentRegistryService) CreateEnrollment(
	context.Context,
	onprem.HumanPrincipal,
	string,
	onprem.OwnerEnrollmentRequest,
) (onprem.Enrollment, error) {
	return onprem.Enrollment{ID: "enrollment", Token: "tm_enroll_test", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (s *agentRegistryService) ListEnrollments(
	context.Context,
	onprem.HumanPrincipal,
	string,
	onprem.AgentArtifactFilter,
) ([]onprem.AgentEnrollmentMetadata, error) {
	now := time.Now()
	return []onprem.AgentEnrollmentMetadata{{
		EnrollmentID: "enrollment", AgentID: "reviewer", CredentialLabel: "laptop",
		Permissions: []onprem.Permission{onprem.PermissionChannelReceive}, Status: "pending",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}}, nil
}

func (s *agentRegistryService) RevokeEnrollment(
	_ context.Context,
	_ onprem.HumanPrincipal,
	agentID string,
	enrollmentID string,
	idempotencyKey string,
) (onprem.AgentEnrollmentMetadata, error) {
	s.lastIdempotencyKey = idempotencyKey
	return onprem.AgentEnrollmentMetadata{EnrollmentID: enrollmentID, AgentID: agentID, Status: "revoked"}, nil
}

func (s *agentRegistryService) ListCredentials(
	context.Context,
	onprem.HumanPrincipal,
	string,
	onprem.AgentArtifactFilter,
) ([]onprem.AgentCredentialMetadata, error) {
	now := time.Now()
	return []onprem.AgentCredentialMetadata{{
		CredentialID: "credential", AgentID: "reviewer", Label: "laptop",
		Permissions: []onprem.Permission{onprem.PermissionChannelReceive}, CreatedAt: now,
	}}, nil
}

func (s *agentRegistryService) RevokeOwnedCredential(
	_ context.Context,
	_ onprem.HumanPrincipal,
	agentID string,
	credentialID string,
	idempotencyKey string,
) (onprem.AgentCredentialMetadata, error) {
	s.lastIdempotencyKey = idempotencyKey
	return onprem.AgentCredentialMetadata{CredentialID: credentialID, AgentID: agentID}, nil
}

func (s *agentRegistryService) ListDirectoryAgents(
	context.Context,
	onprem.Principal,
	onprem.AgentFilter,
) ([]onprem.AgentProfile, error) {
	return []onprem.AgentProfile{{
		AgentID: "receiver", DisplayName: "Receiver", Description: "Receives capsules", AgentType: "codex",
	}}, nil
}

func (s *agentRegistryService) GetDirectoryAgent(
	context.Context,
	onprem.Principal,
	string,
) (onprem.AgentProfile, error) {
	return onprem.AgentProfile{
		AgentID: "receiver", DisplayName: "Receiver", Description: "Receives capsules", AgentType: "codex",
	}, nil
}

func (s *agentRegistryService) ListAdminAgents(
	_ context.Context,
	_ onprem.HumanPrincipal,
	filter onprem.AgentFilter,
) ([]onprem.AgentProfile, error) {
	s.adminFilter = filter
	return []onprem.AgentProfile{testAgent(onprem.AgentStatusActive, "Reviewer", 1)}, nil
}

func (s *agentRegistryService) GetAdminAgent(
	context.Context,
	onprem.HumanPrincipal,
	string,
) (onprem.AgentProfile, error) {
	return testAgent(onprem.AgentStatusActive, "Reviewer", 1), nil
}

func (s *agentRegistryService) UpdateAdminAgent(
	_ context.Context,
	_ onprem.HumanPrincipal,
	_ string,
	request onprem.UpdateAgentRequest,
) (onprem.AgentProfile, error) {
	displayName := "Reviewer"
	if request.DisplayName != nil {
		displayName = *request.DisplayName
	}
	return testAgent(onprem.AgentStatusSuspended, displayName, request.ResourceVersion+1), nil
}

func (s *agentRegistryService) RetireAdminAgent(
	_ context.Context,
	_ onprem.HumanPrincipal,
	_ string,
	resourceVersion int64,
	idempotencyKey string,
) (onprem.AgentProfile, error) {
	s.lastIdempotencyKey = idempotencyKey
	return testAgent(onprem.AgentStatusRetired, "Reviewer", resourceVersion+1), nil
}

func (s *agentRegistryService) TransferAgent(
	_ context.Context,
	_ onprem.HumanPrincipal,
	_ string,
	request onprem.TransferAgentRequest,
) (onprem.AgentProfile, error) {
	profile := testAgent(onprem.AgentStatusActive, "Reviewer", request.ResourceVersion+1)
	profile.OwnerMembershipID = request.TargetMembershipID
	return profile, nil
}

func (s *agentRegistryService) ListAdminEnrollments(
	context.Context,
	onprem.HumanPrincipal,
	string,
	onprem.AgentArtifactFilter,
) ([]onprem.AgentEnrollmentMetadata, error) {
	return []onprem.AgentEnrollmentMetadata{{EnrollmentID: "enroll-1", AgentID: "receiver", Status: "pending"}}, nil
}

func (s *agentRegistryService) RevokeAdminEnrollment(
	_ context.Context,
	_ onprem.HumanPrincipal,
	agentID string,
	enrollmentID string,
	idempotencyKey string,
) (onprem.AgentEnrollmentMetadata, error) {
	s.lastIdempotencyKey = idempotencyKey
	return onprem.AgentEnrollmentMetadata{EnrollmentID: enrollmentID, AgentID: agentID, Status: "revoked"}, nil
}

func (s *agentRegistryService) ListAdminCredentials(
	context.Context,
	onprem.HumanPrincipal,
	string,
	onprem.AgentArtifactFilter,
) ([]onprem.AgentCredentialMetadata, error) {
	return []onprem.AgentCredentialMetadata{{CredentialID: "credential-1", AgentID: "receiver"}}, nil
}

func (s *agentRegistryService) RevokeAdminCredential(
	_ context.Context,
	_ onprem.HumanPrincipal,
	agentID string,
	credentialID string,
	idempotencyKey string,
) (onprem.AgentCredentialMetadata, error) {
	s.lastIdempotencyKey = idempotencyKey
	return onprem.AgentCredentialMetadata{CredentialID: credentialID, AgentID: agentID}, nil
}
