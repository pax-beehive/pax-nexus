package saas_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
)

// testExternalUserID mirrors the production derivation so tests can predict
// which user ID the control plane looks memberships up with.
func testExternalUserID(issuer, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + subject))
	return "usr_" + base64.RawURLEncoding.EncodeToString(sum[:18])
}

// testDigest recomputes the production HMAC-peppered digest for assertions
// on the records services hand to their stores.
func testDigest(pepper string, domain string, value string) onprem.Digest {
	mac := hmac.New(sha256.New, []byte(pepper))
	if _, err := mac.Write([]byte(domain + "\x00" + value)); err != nil {
		panic(err)
	}
	var result onprem.Digest
	copy(result[:], mac.Sum(nil))
	return result
}
