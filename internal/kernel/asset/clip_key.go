// Package asset — clip_key.go (Stock Pipeline Cutover, July 2026).
//
// ClipKey is the canonical deterministic identifier for a stock
// clip: the lowercase hex SHA-256 digest of the canonical
// projection `subject_id|source_video_id|start_ms|end_ms`.
//
// godlike/06 SSOT: this function is the SOLE canonical owner of
// "produce a stable clip-row identity from 4 immutable inputs".
// The 4 inputs are the operator-visible per-clip invariants:
//
//	subject_id       — UUID of the canonical subject (from P0-1
//	                   internal/kernel/asset/subject.go, e.g.
//	                   "sugar-ray-robinson"), NOT a folder_path.
//	                   All casing/whitespace variants of a
//	                   display_name resolve to the SAME subject_id
//	                   via subjects.Resolver, so the clip identity
//	                   inherits the canonical identity invariant.
//	source_video_id  — the YouTube video ID at the time the clip
//	                   was sliced (e.g. "dQw4w9WgXcQ"). Stable for
//	                   the life of that YouTube asset.
//	start_ms         — clip start in the source video, integer
//	                   milliseconds. Stable across re-runs because
//	                   driven by the clip-execution plan.
//	end_ms           — clip end in the source video, integer
//	                   milliseconds. Stable across re-runs.
//
// The user-spec literal is:
//
//	sha256("sugar-ray-robinson|dQw4w9WgXcQ|120000|124000")
//
// so this function returns the 64-character lowercase hex digest
// of those exact bytes. The output is intentionally WITHOUT a
// prefix (unlike SHA256IdempotencyKey at the top of this package)
// because the raw hex IS the clip_storage_index row identity; a
// prefix would force every read to re-validate the prefix shape.
//
// godlike/07 NO-FAKE-AVAILABILITY: empty inputs MUST be rejected
// with typed sentinels so the dedup guarantees on
// clip_storage_index.UNIQUE(clip_key) cannot be silently bypassed
// by a half-wired caller.
//
// # Separator choice ('|' not ':')
//
// Each segment is sha256-quoted individually by the underlying
// digest, so neither '|' nor ':' collides with the per-segment
// values in practice. We use '|' here to honor the user-spec
// literal verbatim — operators can verify the key offline with:
//
//	echo -n 'sugar-ray-robinson|dQw4w9WgXcQ|120000|124000' | sha256sum
//
// ':' is reserved for the codebase's segment-delimited route keys
// in pkg/idempotency.AssetKey/JobKey/OutboxKey (see that
// package's SEGMENT DELIMITER section for the routing/data field
// rationale). Splitting the two namespaces keeps a downstream
// ':'-splitter for run-level keys safe from accidentally splitting
// a clip identity.
//
// # Caller responsibilities
//
//  1. Compute ClipKey EARLY in the clip-create flow, before any
//     use of clip_storage_index. The port (internal/domain/clips/
//     idempotency.go::Idempotency.Inspect) reads/writes by clip_key
//     only.
//  2. The asset_id (media_assets.id UUID) is NOT a clip_key input —
//     it is a downstream SQLite primary key that RecordPersistence
//     stamps onto the storage row. The clip_key is the LOGICAL
//     identity; the asset_id is the SQLite row identity.
//  3. The same clip_key across re-runs MUST be byte-stable across
//     processes (no clock-randomized salting, no per-process
//     nonces). The hash input itself is the SSOT.
package asset

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
)

// ErrClipIdentityEmpty is the typed sentinel for ANY ClipKey
// input being empty (subject_id OR source_video_id). godlike/07:
// reachable via errors.Is from any caller seam so operators can
// branch on the failure mode without string-matching the message.
var ErrClipIdentityEmpty = errors.New("asset.ClipKey: subject_id + source_video_id are required (godlike/07 - no fake availability)")

// ErrClipIdentityInvalid is the typed sentinel for MALFORMED
// inputs (negative timestamps OR end_ms <= start_ms). Distinct
// from ErrClipIdentityEmpty because the empty case is the
// "missing field" wire-shape signal while this is the
// "structurally wrong value" semantic signal.
var ErrClipIdentityInvalid = errors.New("asset.ClipKey: start_ms >= 0 AND end_ms > start_ms are required (godlike/07 - no fake availability)")

// ClipKey computes the canonical deterministic clip identifier:
//
//	hex(sha256(
//	  subject_id + "|" + source_video_id + "|" +
//	  strconv.FormatInt(start_ms, 10) + "|" + strconv.FormatInt(end_ms, 10)
//	))
//
// Returns the 64-character lowercase hex SHA-256 digest of the
// canonical projection. Output is byte-stable across processes
// and platforms (crypto/sha256 is FIPS-validated on Linux/macOS
// and bit-identical across x86/arm).
//
// Fail-closed guards (godlike/07):
//   - subject_id == ""                                            → ErrClipIdentityEmpty
//   - source_video_id == ""                                       → ErrClipIdentityEmpty
//   - start_ms < 0                                                → ErrClipIdentityInvalid
//   - end_ms <= start_ms                                          → ErrClipIdentityInvalid
//
// Each guard returns the typed sentinel wrapped with the
// offending field name (subject_id, source_video_id) or value
// (start_ms=N, end_ms=M<=start_ms=N) so operators can trace which
// upstream producer failed canonicalization.
func ClipKey(subjectID, sourceVideoID string, startMs, endMs int64) (string, error) {
	if subjectID == "" {
		return "", fmt.Errorf("%w: empty subject_id", ErrClipIdentityEmpty)
	}
	if sourceVideoID == "" {
		return "", fmt.Errorf("%w: empty source_video_id", ErrClipIdentityEmpty)
	}
	if startMs < 0 {
		return "", fmt.Errorf("%w: start_ms=%d (must be >= 0)", ErrClipIdentityInvalid, startMs)
	}
	if endMs <= startMs {
		return "", fmt.Errorf("%w: end_ms=%d <= start_ms=%d (must be strictly >)", ErrClipIdentityInvalid, endMs, startMs)
	}

	canonical := subjectID + "|" + sourceVideoID + "|" +
		strconv.FormatInt(startMs, 10) + "|" + strconv.FormatInt(endMs, 10)
	sum := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("%x", sum), nil
}
