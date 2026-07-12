// Package asset — pipeline_state.go is the canonical 12-state enum
// for the per-item ingest-to-index journey (Fase 4, July 2026).
//
// Relationship to existing state machines (Fase 4 design rationale):
//
//   - LifecycleState (lifecycle_state.go) — the deletion/online state
//     machine (STAGING/PROCESSING/ACTIVE/DELETED/...). ORTHOGONAL
//     to PipelineState: a clip with PipelineState=INDEXED typically
//     has LifecycleState=ACTIVE (or PUBLISHED pre-activation); a
//     clip with PipelineState=FAILED typically retains its
//     LifecycleState unchanged (e.g. PROCESSING) because the
//     failure is a per-item flag, not an online-status flip.
//
//   - IndexState (index_state.go) — the indexer-specific state
//     machine (DISCOVERED/EMBEDDING/INDEXED/...). OVERLAPS
//     semantically with the tail of PipelineState (DISCOVERED,
//     INDEX_PENDING, INDEXED) but has a different surface; the
//     PipelineState machine is the per-item journey view
//     (operator-facing diagnostic), IndexState is the indexer's
//     narrow progress view (worker-internal).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// 12 PipelineState values + the IsValidTransition matrix + the
// SanitizeSafeMessage function. Other packages MUST NOT redeclare
// these values; the strings here are the wire-format values that
// the media_assets_pipeline_events migration (129) and the writer
// (clips_lifecycle_events.go::AppendPipelineEvent) store verbatim.
//
// godlike/06 forward-pointer: a future pkg/safemessage package
// (Commit 6+) will own URL/email/API-key PII detection layered on
// top of SanitizeSafeMessage. SanitizeSafeMessage is the
// ASCII-control baseline; the dedicated sanitizer is layered on
// top via `safemessage.Sanitize(s) string` which calls
// SanitizeSafeMessage first then applies the PII/secret rules.
// The wire shape (TEXT column + JSON key "safe_message") is
// stable across both layers.
//
// godlike/07 NO-FAKE-AVAILABILITY: every state transition is a
// typed event written to media_assets_pipeline_events. There is
// no "implicit" transition; the writer is the only path that
// may produce a new event row. Direct UPDATEs to
// media_assets.lifecycle_state that bypass the event log are
// detectable as a per-clip inconsistency between
// media_assets.lifecycle_state and the most-recent event's
// `fase` column for that clip.
package asset

import (
	"strings"
	"unicode"
)

// PipelineState is the canonical per-item state machine for the
// artlist clip ingest-to-index journey (Fase 4, July 2026).
// 12 values, all UPPERCASE. The state machine tracks the per-item
// journey from discovery through indexing, plus two failure-state
// exits.
type PipelineState string

const (
	// StatePipelineDiscovered — initial sentinel. The clip has
	// been identified by the search pipeline but no download has
	// been requested yet. The "newly found" state.
	StatePipelineDiscovered PipelineState = "DISCOVERED"
	// StatePipelineDownloadPending — a download request has been
	// enqueued for this clip but the downloader worker hasn't
	// picked it up yet. Operators see this state when the queue
	// is healthy but the worker pool is saturated.
	StatePipelineDownloadPending PipelineState = "DOWNLOAD_PENDING"
	// StatePipelineDownloading — the downloader worker is actively
	// fetching the clip bytes. Failure modes:
	//   - Transient (network 5xx, 429) → retryable
	//   - Permanent (404, content filter) → FAILED
	StatePipelineDownloading PipelineState = "DOWNLOADING"
	// StatePipelineDownloaded — the clip bytes are local; ready
	// for the processing stage.
	StatePipelineDownloaded PipelineState = "DOWNLOADED"
	// StatePipelineProcessing — ffmpeg is generating renditions.
	StatePipelineProcessing PipelineState = "PROCESSING"
	// StatePipelineProcessed — renditions are local; ready for
	// the publishing stage.
	StatePipelineProcessed PipelineState = "PROCESSED"
	// StatePipelinePublishing — the clip is being uploaded to
	// remote storage (Drive).
	StatePipelinePublishing PipelineState = "PUBLISHING"
	// StatePipelinePublished — the clip is on Drive; ready for
	// the indexing stage.
	StatePipelinePublished PipelineState = "PUBLISHED"
	// StatePipelineIndexPending — a Qdrant indexing request has
	// been enqueued but the indexer worker hasn't picked it up
	// yet.
	StatePipelineIndexPending PipelineState = "INDEX_PENDING"
	// StatePipelineIndexed — terminal success. The clip is on
	// Drive AND its Qdrant point is current. This is the
	// canonical search-ready state.
	StatePipelineIndexed PipelineState = "INDEXED"
	// StatePipelineFailed — terminal failure. Reached from any
	// non-terminal state when the worker classifies the error
	// as permanent (e.g. 404, content filter, validation) or
	// when retryable errors exhaust the retry budget.
	StatePipelineFailed PipelineState = "FAILED"
	// StatePipelineSkipped — terminal skip. Reached from any
	// non-terminal state when the operator explicitly skips
	// processing (e.g. the clip was already indexed, the term
	// was filtered out, the source is in a deny list).
	StatePipelineSkipped PipelineState = "SKIPPED"
)

// CanonicalPipelineStateValues returns the closed enumeration of
// canonical PipelineState strings. Callers use this as the
// single-source-of-truth list for migrations, dashboards, and
// (future) CHECK constraints on media_assets_pipeline_events.fase.
func CanonicalPipelineStateValues() []PipelineState {
	return []PipelineState{
		StatePipelineDiscovered,
		StatePipelineDownloadPending,
		StatePipelineDownloading,
		StatePipelineDownloaded,
		StatePipelineProcessing,
		StatePipelineProcessed,
		StatePipelinePublishing,
		StatePipelinePublished,
		StatePipelineIndexPending,
		StatePipelineIndexed,
		StatePipelineFailed,
		StatePipelineSkipped,
	}
}

// validPipelineStateSet is the O(1) membership set backing
// Valid(). Built once at init from CanonicalPipelineStateValues.
var validPipelineStateSet = func() map[PipelineState]struct{} {
	m := make(map[PipelineState]struct{}, len(CanonicalPipelineStateValues()))
	for _, s := range CanonicalPipelineStateValues() {
		m[s] = struct{}{}
	}
	return m
}()

// Valid returns true if s is one of the canonical PipelineState
// values. Defensive against ad-hoc string values; mirrors the
// pattern in LifecycleState.Valid and IndexState.Valid.
func (s PipelineState) Valid() bool {
	_, ok := validPipelineStateSet[s]
	return ok
}

// IsTerminal reports whether the state is a terminal value (no
// further automatic transitions expected unless the operator
// explicitly restarts the pipeline). The 3 terminal states are
// INDEXED (success), FAILED (failure), SKIPPED (skip). All
// non-terminal states are mid-flow.
func (s PipelineState) IsTerminal() bool {
	switch s {
	case StatePipelineIndexed, StatePipelineFailed, StatePipelineSkipped:
		return true
	}
	return false
}

// IsFailedTerminal reports whether the state is the failure
// terminal. Distinct from IsTerminal because the FAILED state
// requires operator intervention (or explicit retry
// configuration) while INDEXED is self-sustaining.
func (s PipelineState) IsFailedTerminal() bool {
	return s == StatePipelineFailed
}

// IsSucceededTerminal reports whether the state is the success
// terminal. The INDEXED state.
func (s PipelineState) IsSucceededTerminal() bool {
	return s == StatePipelineIndexed
}

// IsSkipped reports whether the state is the skip terminal.
func (s PipelineState) IsSkipped() bool {
	return s == StatePipelineSkipped
}

// IsPending reports whether the state is a non-terminal in-flight
// state (the 9 happy-path intermediate states). Operators use this
// to query "how many clips are currently in flight?" without
// enumerating the 9 individual states.
func (s PipelineState) IsPending() bool {
	return !s.IsTerminal()
}

// String makes PipelineState satisfy fmt.Stringer so the
// canonical log/diagnostic tag (zap.Stringer(...) rendering)
// shows the wire-format value without explicit casts.
func (s PipelineState) String() string { return string(s) }

// IsValidTransition reports whether moving from `from` to `to` is
// one of the allowed edges of the per-item pipeline state machine
// (Fase 4, July 2026).
//
// Strict-machine contract:
//
//	Happy path (10 edges):
//	    DISCOVERED          → DOWNLOAD_PENDING
//	    DOWNLOAD_PENDING    → DOWNLOADING
//	    DOWNLOADING         → DOWNLOADED
//	    DOWNLOADED          → PROCESSING
//	    PROCESSING          → PROCESSED
//	    PROCESSED           → PUBLISHING
//	    PUBLISHING          → PUBLISHED
//	    PUBLISHED           → INDEX_PENDING
//	    INDEX_PENDING       → INDEXED
//
//	Failure / skip exits (any non-terminal → FAILED or SKIPPED):
//	    <any non-terminal>   → FAILED
//	    <any non-terminal>   → SKIPPED
//
//	Self-loops are IDEMPOTENT (writing the same state twice is
//	harmless; the retry-safe AppendPipelineEvent uses this for
//	safe re-entry on partial-write recovery).
//
//	Unknown target values (e.g. "discovered", "DOWNLOAD",
//	"") are REJECTED (Valid() check on `to`).
//
// All other transitions (including out of the terminal INDEXED /
// FAILED / SKIPPED) are rejected. The writer
// (AppendPipelineEvent) gates on IsValidTransition so a programmer
// error becomes a typed error rather than a runtime tombstone of
// an in-flight clip.
//
// Design decision (godlike/07 fail-closed): FAILED is treated as
// TERMINAL with no out-edge in this commit. A future
// "retry-from-FAILED" commit can extend the matrix to allow
// FAILED → <previous-non-terminal>; the wire shape
// (media_assets_pipeline_events) supports it without migration.
func (s PipelineState) IsValidTransition(to PipelineState) bool {
	// Zero-value from-state guard (godlike/07 fail-closed,
	// stricter than the existing state machines): a caller
	// that hasn't initialized a PipelineState and calls
	// IsValidTransition must NOT get a silent false-positive.
	// Placed BEFORE the self-loop check so the guard also
	// rejects the (zero, zero) self-loop — stricter than
	// UploadState.IsValidTransition and
	// WorkflowState.IsValidTransition (which allow
	// (zero, zero) = true via the self-loop). The stricter
	// semantic is the godlike/07 fail-closed contract: an
	// uninitialized state must not silently pass any
	// IsValidTransition check. The regression test
	// TestPipelineState_ZeroValueFromStateRejected pins the
	// guard verbatim.
	if !s.Valid() {
		return false
	}
	if s == to {
		return true // idempotent self-loop (both must be valid canonical)
	}
	if !to.Valid() {
		return false // unknown target state
	}
	// Failure / skip exits: any non-terminal can move to FAILED
	// or SKIPPED. The terminal states (INDEXED, FAILED, SKIPPED)
	// cannot be re-entered.
	if to == StatePipelineFailed || to == StatePipelineSkipped {
		return !s.IsTerminal()
	}
	// Happy-path edges. Explicit table; reject any pair not
	// listed. New edges land here, not in the FAILED/SKIPPED
	// branch above, so the explicit-list-as-source-of-truth
	// discipline is preserved.
	switch s {
	case StatePipelineDiscovered:
		return to == StatePipelineDownloadPending
	case StatePipelineDownloadPending:
		return to == StatePipelineDownloading
	case StatePipelineDownloading:
		return to == StatePipelineDownloaded
	case StatePipelineDownloaded:
		return to == StatePipelineProcessing
	case StatePipelineProcessing:
		return to == StatePipelineProcessed
	case StatePipelineProcessed:
		return to == StatePipelinePublishing
	case StatePipelinePublishing:
		return to == StatePipelinePublished
	case StatePipelinePublished:
		return to == StatePipelineIndexPending
	case StatePipelineIndexPending:
		return to == StatePipelineIndexed
	}
	// Terminal states (INDEXED, FAILED, SKIPPED) only allow
	// self-loops, already handled above. Any other from->to is
	// rejected.
	return false
}

// MaxSafeMessageLen is the canonical maximum length for a
// safe_message field on a PipelineEvent. The cap balances
// operator-triage richness (a sentence or stack-trace fragment)
// against DB column size (TEXT columns are unbounded but the
// JSON wire shape is more compact with a sane upper bound).
// 1024 chars is enough for a diagnostic line + a grep-friendly
// stack-trace fragment; longer diagnostic narratives are
// better suited to a dedicated log pipeline.
const MaxSafeMessageLen = 1024

// SanitizeSafeMessage returns a sanitized copy of s suitable for
// persistence in the media_assets_pipeline_events.safe_message
// column.
//
// Sanitization rules (godlike/07 fail-closed, applied in order):
//
//  1. Control character strip: ASCII control chars 0x00-0x08,
//     0x0B, 0x0C, 0x0E-0x1F, 0x7F are removed. The newline
//     0x0A and carriage-return 0x0D are replaced with single
//     space (preserves single-line rendering in log/dashboard
//     surfaces). The horizontal tab 0x09 is kept verbatim
//     (useful for column-aligned operator output).
//  2. Multi-space collapse: consecutive ASCII spaces collapse
//     to one (so a multi-line stack trace with indentation
//     collapses cleanly into one log line). Tabs do NOT
//     collapse against spaces — tab+space is preserved as
//     `\t ` (the tab breaks the run).
//  3. Trim leading/trailing whitespace.
//  4. Length cap: truncate to MaxSafeMessageLen chars with a
//     trailing "...(truncated)" marker so the cap is
//     observable (silent truncation is the silent-loss
//     anti-pattern SanitizeSafeMessage is meant to avoid).
//
// PII / secret detection (URLs, emails, API keys) is
// intentionally NOT part of this function. Detecting those
// reliably requires domain context (a 6-letter token in one
// workflow is a placeholder; in another it's a credential);
// callers that need PII-stripping semantics should layer a
// dedicated sanitizer on top. This function is the "safe
// enough for operator-log rendering" baseline; tightening the
// rules is a future migration that does not break the wire
// shape (the column is TEXT, the JSON key is "safe_message").
//
// Unicode preservation: non-ASCII runes (italian, chinese,
// emoji, etc.) are preserved verbatim. The control-character
// strip applies to ASCII control chars only — unicode line
// separators (U+2028) and paragraph separators (U+2029) are
// preserved so the operator sees the original message.
//
// Pure function: no I/O, no global state. The unit test
// SanitizeSafeMessage_StripsControlChars + ..._CollapsesSpaces
// + ..._LengthCap + ..._PreservesUnicode + ..._ShortCircuitsOnLongInput
// pin the rules verbatim.
func SanitizeSafeMessage(s string) string {
	if s == "" {
		return ""
	}
	// Step 0: short-circuit on long inputs. A worker that
	// pipes a multi-MB ffmpeg log into safe_message would
	// otherwise pay O(n) allocation across the two Builder
	// passes before the length cap. Pre-truncating to 4× the
	// cap bounds the work to ~4096 chars without changing
	// the wire shape (the final step still truncates to
	// MaxSafeMessageLen with the marker). The 4× constant is
	// empirically chosen: large enough that the control-strip
	// + space-collapse passes don't lose information that the
	// cap-with-marker step would have surfaced anyway, small
	// enough to bound allocation in the hot path.
	if len(s) > MaxSafeMessageLen*4 {
		s = s[:MaxSafeMessageLen*4]
	}
	// Step 1: control char strip + newline/CR replacement.
	// Pre-allocate the builder with the original length to
	// minimise re-allocations; the sanitized string is
	// usually shorter.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r':
			b.WriteByte(' ')
		case r == '\t':
			// tab is allowed verbatim.
			b.WriteRune(r)
		case unicode.IsControl(r):
			// drop other control chars (0x00-0x08, 0x0B,
			// 0x0C, 0x0E-0x1F, 0x7F).
			continue
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	// Step 2: collapse runs of spaces (tabs are NOT spaces;
	// tab+space+space collapses to tab+space).
	var b2 strings.Builder
	b2.Grow(len(out))
	prevSpace := false
	for _, r := range out {
		if r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
			b2.WriteRune(r)
			continue
		}
		prevSpace = false
		b2.WriteRune(r)
	}
	out = b2.String()
	// Step 3: trim leading/trailing whitespace.
	out = strings.TrimSpace(out)
	// Step 4: length cap. The trailing "(truncated)" marker
	// makes the cap observable; the alternative (silent
	// truncation) is the silent-loss anti-pattern
	// SanitizeSafeMessage is meant to avoid.
	if len(out) > MaxSafeMessageLen {
		const marker = "...(truncated)"
		if MaxSafeMessageLen > len(marker) {
			out = out[:MaxSafeMessageLen-len(marker)] + marker
		} else {
			out = out[:MaxSafeMessageLen]
		}
	}
	return out
}
