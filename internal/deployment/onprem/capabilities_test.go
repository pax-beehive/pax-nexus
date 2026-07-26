package onprem_test

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/stretchr/testify/suite"
)

type capabilitiesSuite struct {
	suite.Suite
}

func TestCapabilitiesSuite(t *testing.T) {
	suite.Run(t, new(capabilitiesSuite))
}

func (s *capabilitiesSuite) TestOnlyActiveOwnerCanViewTeamMemory() {
	tests := []struct {
		name    string
		role    onprem.Role
		status  onprem.MembershipStatus
		allowed bool
	}{
		{name: "active owner", role: onprem.RoleOwner, status: onprem.MembershipStatusActive, allowed: true},
		{name: "active admin", role: onprem.RoleAdmin, status: onprem.MembershipStatusActive},
		{name: "active member", role: onprem.RoleMember, status: onprem.MembershipStatusActive},
		{name: "suspended owner", role: onprem.RoleOwner, status: onprem.MembershipStatusSuspended},
		{name: "removed owner", role: onprem.RoleOwner, status: onprem.MembershipStatusRemoved},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			principal := onprem.HumanPrincipal{Role: test.role, MembershipStatus: test.status}
			s.Equal(test.allowed, principal.HasCapability(onprem.CapabilityViewTeamMemory))
			s.Equal(test.allowed, containsCapability(principal.Capabilities(), onprem.CapabilityViewTeamMemory))
		})
	}
}

func containsCapability(capabilities []onprem.HumanCapability, target onprem.HumanCapability) bool {
	for _, capability := range capabilities {
		if capability == target {
			return true
		}
	}
	return false
}
