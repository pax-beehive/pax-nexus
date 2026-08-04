package pagewiki

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// StableID derives a deterministic identifier from prefix and values: a
// SHA-256 over the length-delimited values, truncated and hex-encoded. It is
// shared by the service's whole ID scheme (runs, pages, revisions, topics,
// links, citations) and by the postgres legacy-wiki hydration, whose derived
// IDs must remain byte-for-byte stable across releases — change nothing here
// without a migration story (see TestStableIDLocksKnownValues).
func StableID(prefix string, values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	return prefix + "_" + hex.EncodeToString(hash.Sum(nil))[:24]
}

// ExactTextRange returns the byte range of exactText inside content when it
// occurs exactly once; ok is false for an empty needle or any other
// occurrence count. The service grounds links and citations with this rule
// and the in-memory repository re-validates stored revisions with it, so
// both sides call this one helper and cannot drift apart.
func ExactTextRange(content, exactText string) (int, int, bool) {
	if exactText == "" || strings.Count(content, exactText) != 1 {
		return 0, 0, false
	}
	start := strings.Index(content, exactText)
	return start, start + len(exactText), true
}

// ContainsAny reports whether value contains any of the candidate
// substrings.
func ContainsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
