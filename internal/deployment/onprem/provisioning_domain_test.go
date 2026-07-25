package onprem

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentProvisionIsNotAgentGrantable(t *testing.T) {
	_, err := validateExplicitPermissions([]Permission{PermissionAgentProvision})
	require.Error(t, err)
	_, _, err = validateEnrollmentRequest(EnrollmentRequest{
		UserID: "u", AgentID: "a", Permissions: []Permission{PermissionAgentProvision},
	})
	require.Error(t, err)
}
