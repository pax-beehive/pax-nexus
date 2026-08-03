package handler

// DTO mapping between the on-prem identity/registry domain types and the
// generated teammemory API models, split out of
// identity_registry_endpoints.go to match the channel_mapping.go and
// onprem_mapping.go convention.

import (
	"strconv"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func humanPrincipalToAPI(principal onprem.HumanPrincipal) *api.HumanMeResponse {
	response := &api.HumanMeResponse{
		UserID: principal.UserID, EmailVerified: principal.EmailVerified,
		Capabilities: humanCapabilitiesToAPI(principal.Capabilities()),
	}
	if principal.Email != "" {
		response.Email = &principal.Email
	}
	if principal.MembershipID != "" {
		response.MembershipID = &principal.MembershipID
		role := string(principal.Role)
		status := string(principal.MembershipStatus)
		response.Role = &role
		response.MembershipStatus = &status
	}
	return response
}

func humanCapabilitiesToAPI(capabilities []onprem.HumanCapability) []string {
	result := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		result = append(result, string(capability))
	}
	return result
}

func invitationToAPI(invitation onprem.Invitation) *api.InvitationResponse {
	result := &api.InvitationResponse{
		InvitationID: invitation.InvitationID,
		TargetEmail:  invitation.TargetEmail, Role: string(invitation.Role), Status: string(invitation.Status),
		CreatedAt: invitation.CreatedAt.Format(time.RFC3339Nano), ExpiresAt: invitation.ExpiresAt.Format(time.RFC3339Nano),
	}
	if invitation.Token != "" {
		result.Token = &invitation.Token
	}
	return result
}

func invitationListToAPI(
	invitations []onprem.Invitation,
	limit int,
) *api.ListInvitationsResponse {
	result := &api.ListInvitationsResponse{Invitations: make([]*api.InvitationResponse, len(invitations))}
	for index, invitation := range invitations {
		result.Invitations[index] = invitationToAPI(invitation)
	}
	if len(invitations) == limit && len(invitations) > 0 {
		cursor := invitations[len(invitations)-1].InvitationID
		result.NextCursor = &cursor
	}
	return result
}

func agentProfileToAPI(profile onprem.AgentProfile) *api.AgentProfile {
	result := &api.AgentProfile{
		AgentID: profile.AgentID, DisplayName: profile.DisplayName, Description: profile.Description,
		AgentType: profile.AgentType, Status: string(profile.Status), DirectoryVisible: profile.DirectoryVisible,
		CreatedAt: profile.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: profile.UpdatedAt.Format(time.RFC3339Nano),
		ResourceVersion: profile.ResourceVersion,
	}
	if profile.RetiredAt != nil {
		value := profile.RetiredAt.Format(time.RFC3339Nano)
		result.RetiredAt = &value
	}
	if profile.OwnerMembershipID != "" {
		result.OwnerMembershipID = &profile.OwnerMembershipID
	}
	if profile.OwnerUserID != "" {
		result.OwnerUserID = &profile.OwnerUserID
	}
	if profile.ProvisionedBy != "" {
		result.ProvisionedBy = &profile.ProvisionedBy
	}
	return result
}

func ownedAgentListToAPI(profiles []onprem.AgentProfile, limit int) *api.ListAgentProfilesResponse {
	result := &api.ListAgentProfilesResponse{Agents: make([]*api.AgentProfile, len(profiles))}
	for index, profile := range profiles {
		result.Agents[index] = agentProfileToAPI(profile)
	}
	if len(profiles) == limit && len(profiles) > 0 {
		cursor := profiles[len(profiles)-1].AgentID
		result.NextCursor = &cursor
	}
	return result
}

func directoryAgentToAPI(profile onprem.AgentProfile) *api.DirectoryAgent {
	return &api.DirectoryAgent{
		AgentID: profile.AgentID, DisplayName: profile.DisplayName,
		Description: profile.Description, AgentType: profile.AgentType,
	}
}

func directoryAgentListToAPI(profiles []onprem.AgentProfile, limit int) *api.ListDirectoryAgentsResponse {
	result := &api.ListDirectoryAgentsResponse{Agents: make([]*api.DirectoryAgent, len(profiles))}
	for index, profile := range profiles {
		result.Agents[index] = directoryAgentToAPI(profile)
	}
	if len(profiles) == limit && len(profiles) > 0 {
		cursor := profiles[len(profiles)-1].AgentID
		result.NextCursor = &cursor
	}
	return result
}

func memberToAPI(member onprem.Member) *api.Member {
	result := &api.Member{
		MembershipID: member.MembershipID, UserID: member.UserID, EmailVerified: member.EmailVerified,
		DisplayName: member.DisplayName, Role: string(member.Role), Status: string(member.Status),
		JoinedAt: member.JoinedAt.Format(time.RFC3339Nano), UpdatedAt: member.UpdatedAt.Format(time.RFC3339Nano),
		ResourceVersion: member.ResourceVersion,
	}
	if member.Email != "" {
		result.Email = &member.Email
	}
	return result
}

func memberListToAPI(members []onprem.Member, limit int) *api.ListMembersResponse {
	result := &api.ListMembersResponse{Members: make([]*api.Member, len(members))}
	for index, member := range members {
		result.Members[index] = memberToAPI(member)
	}
	if len(members) == limit && len(members) > 0 {
		cursor := members[len(members)-1].MembershipID
		result.NextCursor = &cursor
	}
	return result
}

func enrollmentMetadataToAPI(metadata onprem.AgentEnrollmentMetadata) *api.AgentEnrollmentMetadata {
	permissions := make([]string, len(metadata.Permissions))
	for index, permission := range metadata.Permissions {
		permissions[index] = string(permission)
	}
	result := &api.AgentEnrollmentMetadata{
		EnrollmentID: metadata.EnrollmentID, AgentID: metadata.AgentID,
		CredentialLabel: metadata.CredentialLabel, Permissions: permissions, Status: metadata.Status,
		CreatedAt: metadata.CreatedAt.Format(time.RFC3339Nano), ExpiresAt: metadata.ExpiresAt.Format(time.RFC3339Nano),
	}
	if metadata.CredentialExpiresAt != nil {
		value := metadata.CredentialExpiresAt.Format(time.RFC3339Nano)
		result.CredentialExpiresAt = &value
	}
	return result
}

func enrollmentMetadataListToAPI(
	metadata []onprem.AgentEnrollmentMetadata,
	limit int,
) *api.ListAgentEnrollmentsResponse {
	result := &api.ListAgentEnrollmentsResponse{Enrollments: make([]*api.AgentEnrollmentMetadata, len(metadata))}
	for index, enrollment := range metadata {
		result.Enrollments[index] = enrollmentMetadataToAPI(enrollment)
	}
	if len(metadata) == limit && len(metadata) > 0 {
		cursor := metadata[len(metadata)-1].EnrollmentID
		result.NextCursor = &cursor
	}
	return result
}

func credentialMetadataToAPI(metadata onprem.AgentCredentialMetadata) *api.AgentCredentialMetadata {
	permissions := make([]string, len(metadata.Permissions))
	for index, permission := range metadata.Permissions {
		permissions[index] = string(permission)
	}
	result := &api.AgentCredentialMetadata{
		CredentialID: metadata.CredentialID, AgentID: metadata.AgentID,
		Label: metadata.Label, Permissions: permissions, CreatedAt: metadata.CreatedAt.Format(time.RFC3339Nano),
	}
	if metadata.ExpiresAt != nil {
		value := metadata.ExpiresAt.Format(time.RFC3339Nano)
		result.ExpiresAt = &value
	}
	if metadata.RevokedAt != nil {
		value := metadata.RevokedAt.Format(time.RFC3339Nano)
		result.RevokedAt = &value
	}
	if metadata.LastUsedAt != nil {
		value := metadata.LastUsedAt.Format(time.RFC3339Nano)
		result.LastUsedAt = &value
	}
	return result
}

func credentialMetadataListToAPI(
	metadata []onprem.AgentCredentialMetadata,
	limit int,
) *api.ListAgentCredentialsResponse {
	result := &api.ListAgentCredentialsResponse{Credentials: make([]*api.AgentCredentialMetadata, len(metadata))}
	for index, credential := range metadata {
		result.Credentials[index] = credentialMetadataToAPI(credential)
	}
	if len(metadata) == limit && len(metadata) > 0 {
		cursor := metadata[len(metadata)-1].CredentialID
		result.NextCursor = &cursor
	}
	return result
}

func auditEventToAPI(event onprem.AuditEvent) *api.AuditEvent {
	result := &api.AuditEvent{
		AuditEventID: event.AuditEventID, ActorKind: event.ActorKind, Action: event.Action,
		TargetKind: event.TargetKind, TargetID: event.TargetID,
		OccurredAt: event.OccurredAt.Format(time.RFC3339Nano),
	}
	if event.ActorUserID != "" {
		result.ActorUserID = &event.ActorUserID
	}
	if event.ActorMembershipID != "" {
		result.ActorMembershipID = &event.ActorMembershipID
	}
	if event.ActorAgentID != "" {
		result.ActorAgentID = &event.ActorAgentID
	}
	if event.ActorCredentialID != "" {
		result.ActorCredentialID = &event.ActorCredentialID
	}
	return result
}

func auditEventListToAPI(events []onprem.AuditEvent, limit int) *api.ListAuditEventsResponse {
	result := &api.ListAuditEventsResponse{AuditEvents: make([]*api.AuditEvent, len(events))}
	for index, event := range events {
		result.AuditEvents[index] = auditEventToAPI(event)
	}
	if len(events) == limit && len(events) > 0 {
		cursor := strconv.FormatInt(events[len(events)-1].AuditEventID, 10)
		result.NextCursor = &cursor
	}
	return result
}
