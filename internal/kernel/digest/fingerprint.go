package digest

import "strings"

// Fingerprint returns the canonical deterministic SHA-256 hex fingerprint of
// the given parts. It is the ONE algorithm used to derive semantic_hash,
// semantic_document_hash, embedding_contract_hash, plan fingerprints, and
// idempotency keys.
//
// Parts are joined with a NUL byte so "a|b" + "c" and "a" + "b|c" cannot
// collide; callers must canonicalize their input (the domain layer decides
// WHAT enters the fingerprint) and must not pass parts that themselves
// contain NUL.
func Fingerprint(parts ...string) string {
	return SHA256Bytes([]byte(strings.Join(parts, "\x00")))
}
