// Package images — chrome_provider_ids.go (commit 5, 2026-07):
// per-request correlation token generators.
//
// PR-CHROME-PROVIDER-SPLIT (commit 5, July 2026): per godlike/06 SSOT,
// chrome_provider_ids.go is the SINGLE canonical owner of "how a
// request is identified" inside this package. Each request shipping
// to the persistent worker carries a generation_id (RFC 4122 UUIDv4
// for downstream log aggregation pivot).
package chrome

import (
	"crypto/rand"
	"fmt"
	"os"
	"time"
)

// generateUUIDv4 returns a valid RFC 4122 UUID v4 string (8-4-4-4-12
// hex with hyphens, 36 chars total). crypto/rand supplies the
// entropy; the version (4) and variant (10x) bits are pinned per the
// RFC so the output is a well-formed UUID that downstream log
// aggregation can pivot on without ad-hoc parsing.
//
// P1.3 (July 2026): replaces generateRequestID() which produced a
// 32-char hex blob. The UUIDv4 form is the canonical "per-request"
// correlation token per the user spec ("UUID per request"). The
// fallback to a timestamp + PID path on rand.Read failure is
// preserved so the generation_id is never empty, even on
// /dev/urandom EIO.
func generateUUIDv4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("ts-%d-%d", time.Now().UnixNano(), os.Getpid())
	}
	// Pin version (4) and variant (RFC 4122 10xx).
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
