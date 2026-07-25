package onprem_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/stretchr/testify/suite"
)

// sequentialTokenSource returns a token generator that never exhausts,
// unlike the fixed-length tokenSequence used by service_test.go. Several
// tests below call ProvisionDeviceAgent more than once per test method
// (each successful call consumes two tokens), so a bounded sequence would
// need constant resizing as cases are added.
func sequentialTokenSource() func() (string, error) {
	counter := 0
	return func() (string, error) {
		counter++
		return fmt.Sprintf("token-%d", counter), nil
	}
}

type provisioningSuite struct {
	suite.Suite
	store   *memoryCredentialStore
	service *onprem.CredentialService
	now     time.Time
}

func TestProvisioningSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(provisioningSuite))
}

func (s *provisioningSuite) SetupTest() {
	s.now = time.Date(2026, time.July, 24, 9, 0, 0, 0, time.UTC)
	s.store = newMemoryCredentialStore()
	service, err := onprem.NewCredentialService(s.store, onprem.CredentialConfig{
		RotationOverlap:  5 * time.Minute,
		SecretPepper:     "0123456789abcdef0123456789abcdef",
		DeviceAgentLimit: 4,
	}, onprem.WithClock(func() time.Time { return s.now }), onprem.WithTokenSource(sequentialTokenSource()))
	s.Require().NoError(err)
	s.service = service
}

func (s *provisioningSuite) device() onprem.Principal {
	return onprem.Principal{
		UserID: "owner", MembershipID: "membership-1", ScopeID: onprem.LocalScopeID,
		CredentialID: "device-credential", Kind: onprem.CredentialKindDevice,
		Permissions:          []onprem.Permission{onprem.PermissionAgentProvision},
		GrantablePermissions: []onprem.Permission{onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet},
	}
}

func (s *provisioningSuite) validRequest() onprem.DeviceProvisionRequest {
	return onprem.DeviceProvisionRequest{AgentID: "agent-1", DisplayName: "Agent One", AgentType: "codex"}
}

// TestForbiddenMatrix exercises every principal shape the service must
// reject with ErrForbidden before ever reaching the store, including an
// agent-kind credential that somehow carries agent_provision.
func (s *provisioningSuite) TestForbiddenMatrix() {
	ctx := context.Background()
	tests := []struct {
		name      string
		principal onprem.Principal
	}{
		{
			name: "agent kind credential with agent_provision permission",
			principal: onprem.Principal{
				UserID: "owner", MembershipID: "membership-1", ScopeID: onprem.LocalScopeID,
				CredentialID: "agent-credential", Kind: onprem.CredentialKindAgent,
				Permissions: []onprem.Permission{onprem.PermissionAgentProvision},
			},
		},
		{
			name: "device kind without agent_provision permission",
			principal: onprem.Principal{
				UserID: "owner", MembershipID: "membership-1", ScopeID: onprem.LocalScopeID,
				CredentialID: "device-credential", Kind: onprem.CredentialKindDevice,
				Permissions: []onprem.Permission{onprem.PermissionObserve},
			},
		},
		{
			name: "wrong scope",
			principal: onprem.Principal{
				UserID: "owner", MembershipID: "membership-1", ScopeID: "other-scope",
				CredentialID: "device-credential", Kind: onprem.CredentialKindDevice,
				Permissions: []onprem.Permission{onprem.PermissionAgentProvision},
			},
		},
		{
			name: "empty credential ID",
			principal: onprem.Principal{
				UserID: "owner", MembershipID: "membership-1", ScopeID: onprem.LocalScopeID,
				Kind: onprem.CredentialKindDevice, Permissions: []onprem.Permission{onprem.PermissionAgentProvision},
			},
		},
		{
			name:      "zero value principal",
			principal: onprem.Principal{},
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := s.service.ProvisionDeviceAgent(ctx, test.principal, s.validRequest())
			s.Require().ErrorIs(err, onprem.ErrForbidden)
			s.Empty(s.store.provisionCalls, "store must not be reached when the principal check fails")
		})
	}
}

// TestPermissionSubsetEnforcement covers both directions of the grantable
// permission rule: an empty request inherits the device's full grantable
// set, and an explicit request that exceeds the grantable set is forbidden.
func (s *provisioningSuite) TestPermissionSubsetEnforcement() {
	ctx := context.Background()

	request := s.validRequest()
	_, err := s.service.ProvisionDeviceAgent(ctx, s.device(), request)
	s.Require().NoError(err)
	s.Require().Len(s.store.provisionCalls, 1)
	s.Equal(
		[]onprem.Permission{onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet},
		s.store.provisionCalls[0].credential.Permissions,
		"empty request permissions must inherit the device's full grantable set",
	)

	exceeding := s.validRequest()
	exceeding.Permissions = []onprem.Permission{onprem.PermissionObserve, onprem.PermissionChannelSend}
	_, err = s.service.ProvisionDeviceAgent(ctx, s.device(), exceeding)
	s.Require().ErrorIs(err, onprem.ErrForbidden)
	s.Require().Len(s.store.provisionCalls, 1, "the store must not be reached for a request exceeding the grantable set")

	withinSet := s.validRequest()
	withinSet.AgentID = "agent-2"
	withinSet.Permissions = []onprem.Permission{onprem.PermissionSearch}
	_, err = s.service.ProvisionDeviceAgent(ctx, s.device(), withinSet)
	s.Require().NoError(err)
	s.Require().Len(s.store.provisionCalls, 2)
	s.Equal([]onprem.Permission{onprem.PermissionSearch}, s.store.provisionCalls[1].credential.Permissions)
}

// TestDeviceWithNoGrantablePermissionsIsForbidden covers the degenerate case
// where a device enrollment somehow carries no grantable permissions at all.
func (s *provisioningSuite) TestDeviceWithNoGrantablePermissionsIsForbidden() {
	ctx := context.Background()
	device := s.device()
	device.GrantablePermissions = nil

	_, err := s.service.ProvisionDeviceAgent(ctx, device, s.validRequest())

	s.Require().ErrorIs(err, onprem.ErrForbidden)
}

// TestInvalidInputs covers agent identity and agent_type validation, which
// must reject the request before the store is ever called.
func (s *provisioningSuite) TestInvalidInputs() {
	ctx := context.Background()
	tests := []struct {
		name    string
		request onprem.DeviceProvisionRequest
	}{
		{name: "empty agent_id", request: onprem.DeviceProvisionRequest{DisplayName: "Agent", AgentType: "codex"}},
		{name: "empty display_name", request: onprem.DeviceProvisionRequest{AgentID: "agent-1", AgentType: "codex"}},
		{
			name: "agent_id contains path separator",
			request: onprem.DeviceProvisionRequest{
				AgentID: "agent/1", DisplayName: "Agent", AgentType: "codex",
			},
		},
		{
			name: "agent_id too long",
			request: onprem.DeviceProvisionRequest{
				AgentID: strings.Repeat("a", 129), DisplayName: "Agent", AgentType: "codex",
			},
		},
		{
			name:    "empty agent_type",
			request: onprem.DeviceProvisionRequest{AgentID: "agent-1", DisplayName: "Agent"},
		},
		{
			name: "blank agent_type",
			request: onprem.DeviceProvisionRequest{
				AgentID: "agent-1", DisplayName: "Agent", AgentType: "   ",
			},
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := s.service.ProvisionDeviceAgent(ctx, s.device(), test.request)
			s.Require().ErrorIs(err, onprem.ErrInvalidIdentityInput)
			s.Empty(s.store.provisionCalls, "the store must not be reached for invalid input")
		})
	}
}

// TestHappyPathPassesLimitAndProvisionedByAndReturnsAPIKey exercises the
// success path: the configured device agent limit and the device's
// credential ID reach the store untouched, and the returned API key carries
// the tm_key_ prefix used by every issued credential.
func (s *provisioningSuite) TestHappyPathPassesLimitAndProvisionedByAndReturnsAPIKey() {
	ctx := context.Background()
	s.store.provisionOutcome = onprem.ProvisionOutcome{AgentCreated: true}

	result, err := s.service.ProvisionDeviceAgent(ctx, s.device(), s.validRequest())

	s.Require().NoError(err)
	s.Require().Len(s.store.provisionCalls, 1)
	call := s.store.provisionCalls[0]
	s.Equal("device-credential", call.deviceCredentialID)
	s.Equal("device-credential", call.credential.ProvisionedBy)
	s.Equal(4, call.activeAgentLimit)
	s.Equal("device-credential", call.profile.ProvisionedBy)
	s.Equal("agent-1", call.profile.AgentID)
	s.Equal(onprem.AgentStatusActive, call.profile.Status)
	s.True(call.profile.DirectoryVisible)
	s.Equal(onprem.CredentialKindAgent, call.credential.Kind)

	s.True(strings.HasPrefix(result.APIKey, "tm_key_"), "issued API key must carry the tm_key_ prefix, got %q", result.APIKey)
	s.NotEmpty(result.CredentialID)
	s.Equal("agent-1", result.AgentID)
	s.True(result.AgentCreated)
	s.Empty(result.RotatedFromCredentialID)
}

// TestOutcomePassthrough verifies that rotation metadata returned by the
// store passes through to the service's result unchanged.
func (s *provisioningSuite) TestOutcomePassthrough() {
	ctx := context.Background()
	s.store.provisionOutcome = onprem.ProvisionOutcome{RotatedFromCredentialID: "old-credential"}

	result, err := s.service.ProvisionDeviceAgent(ctx, s.device(), s.validRequest())

	s.Require().NoError(err)
	s.Equal("old-credential", result.RotatedFromCredentialID)
	s.False(result.AgentCreated)
}

// TestStoreErrorsPropagate verifies that conflict and limit errors returned
// by the store surface through the service wrapped, but still matchable
// with errors.Is.
func (s *provisioningSuite) TestStoreErrorsPropagate() {
	ctx := context.Background()
	for _, storeErr := range []error{onprem.ErrAgentProvisionConflict, onprem.ErrDeviceAgentLimitExceeded} {
		s.Run(storeErr.Error(), func() {
			s.store.provisionErr = storeErr
			_, err := s.service.ProvisionDeviceAgent(ctx, s.device(), s.validRequest())
			s.Require().ErrorIs(err, storeErr)
		})
	}
}
