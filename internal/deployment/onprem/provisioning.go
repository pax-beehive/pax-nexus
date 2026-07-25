package onprem

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DeviceProvisionRequest carries a device credential's request to create or
// rotate the credential for one of the agents it provisions.
type DeviceProvisionRequest struct {
	AgentID     string
	DisplayName string
	AgentType   string
	Permissions []Permission
}

// ProvisionedAgentCredential is the credential (and agent lifecycle outcome)
// issued by a successful device-scoped provisioning request.
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

// ProvisionDeviceAgent creates or rotates the credential for an agent that a
// device credential provisions. Only a device credential carrying
// PermissionAgentProvision may call this; an agent credential is always
// forbidden, even if it somehow carries the permission.
func (s *CredentialService) ProvisionDeviceAgent(
	ctx context.Context, principal Principal, request DeviceProvisionRequest,
) (ProvisionedAgentCredential, error) {
	if err := requireDeviceProvisioner(principal); err != nil {
		return ProvisionedAgentCredential{}, err
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

// requireDeviceProvisioner is the guard shared by every device-scoped
// provisioning endpoint: only a device credential carrying
// PermissionAgentProvision, scoped to this deployment, may proceed. An
// agent credential is always forbidden, even if it somehow carries the
// permission.
func requireDeviceProvisioner(principal Principal) error {
	if principal.ScopeID != LocalScopeID || principal.CredentialID == "" ||
		principal.Kind != CredentialKindDevice || !principal.HasPermission(PermissionAgentProvision) {
		return ErrForbidden
	}
	return nil
}

// ListDeviceProvisionedAgents returns every credential row (including
// revoked history) the device credential in principal has provisioned.
// Subject to the same guard as ProvisionDeviceAgent: only a device
// credential carrying PermissionAgentProvision may call this.
func (s *CredentialService) ListDeviceProvisionedAgents(
	ctx context.Context, principal Principal,
) ([]DeviceProvisionedAgent, error) {
	if err := requireDeviceProvisioner(principal); err != nil {
		return nil, err
	}
	agents, err := s.store.ListDeviceProvisionedAgents(ctx, principal.CredentialID)
	if err != nil {
		return nil, fmt.Errorf("list device provisioned agents: %w", err)
	}
	return agents, nil
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
