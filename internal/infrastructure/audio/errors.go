// Package audioasset — errors.go (PR-VO-TTS-PERSISTENT-WORKER, July 2026):
// canonical typed-sentinel surface for the persistent TTS worker.
//
// godlike/07 typed-error contract (audit P0.5, July 2026): every error
// return from the persistent-worker paddings (ensureStarted, Generate,
// sendSynthesizeRequest, Health, Stop) wraps one of these typed
// sentinels via fmt.Errorf(... %w ...) so callers can probe with
// errors.Is without parsing string fragments. The sentinels are
// package-private-shape (lowercase for internal names; UPPERCASE for
// exported) so they're discoverable from `internal/app/` adapters and
// the canonical composition wiring at build_bundles_core.go.
//
// Cross-references:
//   - architecture/current.yaml#VO-DECOMPOSITION-2026-07-04.linked_issues[PR-VO-TTS-PERSISTENT-WORKER]
//   - AGENTS.md Pattern 10 typed-port contract (godlike/06 SSOT)
package audioasset

import (
	"errors"
	"strings"
)

// ErrWorkerUnavailable is the canonical typed sentinel for "the persistent
// TTS worker (Python tts_edge_server.py) could not be brought up".
//
// Wrapped by:
//   - worker_process.go::ensureStarted — when the script is missing
//     from disk, the subprocess fails to Start, or the PORT=<n> line
//     does not appear on stdout (startup crash signature).
//   - processor.go::Generate — when ensureStarted returned a fail-closed
//     error AND the fallback decision routes through this sentinel.
//
// Recovery contract: when wrapped via fallThroughLegacy, the processor
// falls back to the legacy spawn-per-call path (forward-pointer
// PR-VO-TTS-PERSISTENT-WORKER-CUTOVER for the CUTOVER-phase removal
// of that fallback).
var ErrWorkerUnavailable = errors.New("audioasset: persistent TTS worker unavailable")

// ErrWorkerHealthFailed is the canonical typed sentinel for "the persistent
// TTS worker responded on /synthesize but the post-startup GET /health
// probe returned non-200 OR the worker died after startup".
//
// Distinct from ErrWorkerUnavailable so callers can distinguish "the
// worker never came up" (retry-the-whole-pipeline) from "the worker
// came up then died" (reset + re-launch).
var ErrWorkerHealthFailed = errors.New("audioasset: persistent TTS worker health check failed")

// ErrSynthesizeFailed is the canonical typed sentinel for "the persistent
// TTS worker rejected the /synthesize request" — either because the
// HTTP status was non-200 or because the JSON response had
// ok=false with an error body.
//
// The underlying error from the Python server (if any) is preserved
// via a second %w wrap (Go 1.20+): errors.Is recovers the sentinel,
// errors.As recovers the underlying cause.
var ErrSynthesizeFailed = errors.New("audioasset: synthesize request failed")

// ErrOutputMissing is the canonical typed sentinel for "the worker
// reported success but the output file is missing from disk" — a
// hard fail-closed contract. This catches the pre-DRY case where
// the Python subprocess claimed success but a path mismatch or
// race-condition cleanup left the file gone; without the typed
// sentinel, callers would interpret the missing file as
// "Generate returned nil error + zero LocalPath".
var ErrOutputMissing = errors.New("audioasset: synthesis output file missing")

// ErrInvalidFilename is the canonical typed sentinel for "the caller
// supplied a Filename that fails the path-traversal invariant" —
// e.g. "../etc/passwd" or just "////var/log". Hard fail-closed;
// Processor rejects before any TTS work happens.
var ErrInvalidFilename = errors.New("audioasset: invalid filename (path traversal detected)")

// ErrEmptyAudio is the canonical typed sentinel for "the TTS bridge
// produced an empty audio file" — the legacy CLI's "Empty file" error or
// the persistent worker's "generated file is empty or missing" error. Both
// are transient upstream conditions (edge-tts rate-limit / network glitch),
// so Generate retries the synthesis before surfacing the failure instead of
// failing the run immediately.
var ErrEmptyAudio = errors.New("audioasset: TTS bridge produced empty audio")

// ErrSilentAudio is the canonical typed sentinel for "the TTS bridge
// produced a non-empty but inaudible audio file" — a synthesized VO whose
// peak level never rises above the audible floor (edge-tts glitch producing
// a near-silent MP3). Distinct from ErrEmptyAudio (zero bytes) so the
// minimum-loudness gate can retry the synthesis on the same transient
// condition instead of shipping a silent voiceover.
var ErrSilentAudio = errors.New("audioasset: TTS bridge produced silent audio")

// isBridgeEmptyAudioError reports whether a bridge ok=false error string is
// the canonical empty-audio condition. Kept as a single classification site
// so both bridge paths (legacy CLI + persistent worker) share one decision.
func isBridgeEmptyAudioError(msg string) bool {
	switch strings.TrimSpace(msg) {
	case "Empty file", "generated file is empty or missing":
		return true
	default:
		return false
	}
}
