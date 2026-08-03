package onprem

import "testing"

func TestLocalizePrincipalStampsLocalScope(t *testing.T) {
	principal := localizePrincipal(HumanPrincipal{UserID: "user-1"})
	if principal.ScopeID != LocalScopeID {
		t.Fatalf("ScopeID = %q, want %q", principal.ScopeID, LocalScopeID)
	}
}
