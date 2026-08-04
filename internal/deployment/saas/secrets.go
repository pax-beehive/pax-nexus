package saas

// Token and digest helpers, mirrored line-for-line from the private helpers
// in internal/deployment/onprem (service.go, identity.go) and
// internal/platform/postgres (identity.go externalUserID). The on-prem ADR
// keeps the on-prem package untouched, so the saas control plane duplicates
// these few stable lines instead of exporting shared helpers: token formats
// ("tm_session_", "tm_invite_", "tm_enroll_", "tm_key_"), the HMAC-peppered
// digest scheme, and the global user ID derivation must stay byte-identical
// with the on-prem profile because both share the onprem_users table and the
// same handler token surface.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
)

const currentDigestKeyVersion int16 = 1

const (
	credentialDigestDomain = "agent-credential"
	enrollmentDigestDomain = "agent-enrollment"
	sessionDigestDomain    = "human-session"
	invitationDigestDomain = "membership-invitation"
)

// externalUserID derives the global user ID from an OIDC identity, exactly
// as the postgres adapters do (postgres.externalUserID), so the control
// plane can look up memberships before the first store write of a login.
func externalUserID(issuer, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + subject))
	return "usr_" + base64.RawURLEncoding.EncodeToString(sum[:18])
}

// newPrefixedID generates a store-row ID ("team_", "mbr_") from the same
// random source as tokens, matching the postgres newPostgresID shape.
func newPrefixedID(prefix string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", fmt.Errorf("read random %s ID: %w", prefix, err)
	}
	return prefix + "_" + token, nil
}

func digest(value string) onprem.Digest {
	return sha256.Sum256([]byte(value))
}

func secretPublicID(value string, prefix string) (string, bool) {
	remainder, found := strings.CutPrefix(value, prefix)
	if !found {
		return "", false
	}
	publicID, secret, found := strings.Cut(remainder, ".")
	if !found || strings.TrimSpace(publicID) == "" || strings.TrimSpace(secret) == "" {
		return "", false
	}
	return publicID, true
}

type secretDigester struct {
	key []byte
}

func newSecretDigester(secret string) (secretDigester, error) {
	secret = strings.TrimSpace(secret)
	if len(secret) < 32 {
		return secretDigester{}, fmt.Errorf("secret pepper must contain at least 32 characters")
	}
	return secretDigester{key: []byte(secret)}, nil
}

func (d secretDigester) Digest(domain string, value string) onprem.Digest {
	mac := hmac.New(sha256.New, d.key)
	payload := []byte(domain + "\x00" + value)
	written, err := mac.Write(payload)
	if err != nil || written != len(payload) {
		panic("HMAC digest write failed")
	}
	var result onprem.Digest
	copy(result[:], mac.Sum(nil))
	return result
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("read random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// enrollmentToken builds the agent enrollment token and its verifiable
// (digest-able) form, mirroring onprem.enrollmentToken including the
// optional portal-URL origin hint third segment.
func enrollmentToken(id string, secret string, portalURL string) (string, string) {
	verifiableToken := "tm_enroll_" + id + "." + secret
	portalURL = strings.TrimSpace(portalURL)
	parsed, err := url.Parse(portalURL)
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Hostname() == "" ||
		parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return verifiableToken, verifiableToken
	}
	originHint := base64.RawURLEncoding.EncodeToString([]byte(portalURL))
	return verifiableToken + "." + originHint, verifiableToken
}

// parseEnrollmentToken mirrors onprem.parseEnrollmentToken: it accepts the
// optional origin-hint segment and returns the verifiable token plus the
// public enrollment ID.
func parseEnrollmentToken(value string) (string, string, bool) {
	remainder, found := strings.CutPrefix(strings.TrimSpace(value), "tm_enroll_")
	if !found {
		return "", "", false
	}
	parts := strings.Split(remainder, ".")
	if len(parts) < 2 || len(parts) > 3 ||
		strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return "tm_enroll_" + parts[0] + "." + parts[1], parts[0], true
}
