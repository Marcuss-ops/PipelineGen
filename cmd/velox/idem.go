package main

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// keyHexLen is the number of SHA-256 hex characters kept in the Idempotency-Key
// (16 bytes). Short enough to stay header-friendly, long enough that a payload
// collision is not a practical concern.
const keyHexLen = 32

// idempotencyKey derives a deterministic Idempotency-Key from the project name
// and the exact payload bytes.
//
// A key built from `date +%s` (or any clock/random source) makes every retry a
// new logical submission, so a re-run duplicates work instead of replaying —
// which is the opposite of what the header is for. Same project + same payload
// ⇒ same key, always.
//
// The digest is computed by internal/kernel/digest (the SHA-256 SSOT); this
// package must not import crypto/sha256 directly (godlike/06).
func idempotencyKey(project string, payload []byte) string {
	return slug(project) + "-" + payloadSHA256(payload)[:keyHexLen]
}

// payloadSHA256 is the full content digest, recorded with the job so a replay
// can prove it re-sent the same bytes.
func payloadSHA256(payload []byte) string {
	return digest.SHA256Bytes(payload)
}

// slug lowercases and reduces s to [a-z0-9-] so a project name with spaces or
// punctuation still produces a header-safe key.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "job"
	}
	return out
}
