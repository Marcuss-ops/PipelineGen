// Package operations — generation_submission_ids.go is the
// per-responsibility helper file for the FASE 2 submission
// flow's ID-generation entropy helpers.
//
// godlike/06 SSOT (pure helpers): this file holds ONLY the
// stdlib-based entropy helpers (crypto/rand + encoding/hex + time).
// NO database, NO typed ports, NO mutation. The helpers are
// pure functions returning strings; the only side effect
// (randomHexSuffix's fallback on crypto/rand failure) is a
// deterministic time-derived byte slice — still no I/O
// outside the process.
//
// godlike/10 non-duplication: these helpers are the canonical
// owners of the `*_<unix_nano>_<8hex>` ID shape used across
// the FASE 2 submission flow (job_id + operation_id). A future
// caller that needs the same shape MUST import these helpers
// rather than re-implementing the (prefix, unix_nano, hex)
// composition inline.
package operations

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// defaultJobIDGen produces a unique job ID with the canonical
// `job_<unix_nano>_<8hex>` shape. The 8-char hex suffix is
// derived from crypto/rand (16 bits × 4 bits = ~32 bits
// of entropy) which is sufficient for the FASE 2 Submit
// concurrency range (the submitMu mutex serialises Submits
// on the same process; the suffix breaks the same-nanosecond
// tie).
func defaultJobIDGen() string {
	return generateID("job")
}

// defaultOperationIDGen produces a unique operation ID with
// the canonical `op_<unix_nano>_<8hex>` shape.
func defaultOperationIDGen() string {
	return generateID("op")
}

// generateID returns a `<prefix>_<unix_nano>_<8hex>` ID.
func generateID(prefix string) string {
	now := time.Now().UnixNano()
	suf := randomHexSuffix(4)
	return fmt.Sprintf("%s_%d_%s", prefix, now, suf)
}

// randomHexSuffix returns a lowercase hex suffix. On crypto/rand
// failure it falls back to a time-derived byte slice so the ID
// remains non-empty and stable enough for operational use.
func randomHexSuffix(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		ns := time.Now().UnixNano()
		for i := range buf {
			buf[i] = byte(ns >> (uint(i) * 8))
		}
	}
	return hex.EncodeToString(buf)
}
