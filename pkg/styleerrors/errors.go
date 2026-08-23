// Package styleerrors — canonical fail-closed sentinels for the
// StyleRegistry.ApplyStyle contract.
//
// Step-2 typed migration (PR-IMAGES-AI-VS-NORMAL-PLAN, action A2,
// July 2026): the pre-A2 StyleRegistry.ApplyStyle signature was
// (prompt, styleName string) string with silent fallback on every
// failure mode — unknown style, disabled style, empty prompt +
// empty suffix, and version drift all surfaced as a re-emit of the
// user prompt unchanged, with no error signal. A2 closes that
// fail-open by changing the return shape to
// (*StyleComposedPrompt, error) and pegging each failure mode to
// a typed pkg/styleerrors sentinel.
//
// godlike/06 one-owner-per-fact (canonical authority pointer):
//
//	This file is the SOLE canonical owner of the 4 sentinels below.
//	Image/styles/types.go re-exports them as Go value-aliases so
//	application-layer code that already imports image/styles compiles
//	unchanged and dispatches via errors.Is transparently. The alias
//	chain preserves byte-stable error identity:
//
//	    image/styles.ErrUnknownStyle          == pkg/styleerrors.ErrUnknownStyle
//	    image/styles.ErrStyleDisabled         == pkg/styleerrors.ErrStyleDisabled
//	    image/styles.ErrEmptyPrompt           == pkg/styleerrors.ErrEmptyPrompt
//	    image/styles.ErrStyleVersionMismatch  == pkg/styleerrors.ErrStyleVersionMismatch
//
//	resolver.go (image/styles) continues to emit image/styles.ErrStyleDisabled,
//	which IS pkg/styleerrors.ErrStyleDisabled at the value-identity level.
//	A future wave-tracker entry (CONTRACT phase, after EXPAND→BACKFILL→CUTOVER)
//	will fold the image/styles package's re-export surface into the
//	canonical pkg/styleerrors import path.
//
// godlike/07 fail-closed contract:
//
//	Every sentinel is `errors.New(...)` and is emitted ONLY through
//	`fmt.Errorf("%w: ...", ErrXxx, ...)` wrap chains at the seam that
//	decides dispatch. Callers pattern-match via errors.Is; message
//	text is informational only.
//
// The ApplyStyle fail-closed contract (A2) — every input condition
// that cannot produce a ComposedPrompt emits a typed error:
//   - ErrUnknownStyle        styleName empty OR absent from registry
//   - ErrStyleDisabled       style found but Enabled=false
//   - ErrEmptyPrompt         prompt whitespace-only AND style's PromptSuffix whitespace-only
//     (no composed text can be produced)
//   - ErrStyleVersionMismatch caller-supplied version > 0 AND style.Version differs
package styleerrors

import "errors"

// ErrUnknownStyle is returned when ApplyStyle cannot find (or cannot
// interpret) the supplied styleName. Triggered by either:
//
//   - styleName is empty (was a passthrough silent-fallback pre-A2;
//     A2 surfaces this as a typed error so the caller learns that
//     a style was implicitly "none" rather than "queried-and-found")
//   - styleName is non-empty but no registry entry matches (also
//     was a passthrough silent-fallback pre-A2)
//
// Per godlike/07 no-fake-availability: even if the prompt itself
// is well-formed, an unknown style means the caller asked for a
// rendering suffix the canonical YAML did not declare. Return the
// prompt unchanged would silently swallow the operator's intent.
var ErrUnknownStyle = errors.New("styleerrors: style is unknown")

// ErrStyleDisabled is returned when the requested style IS present
// in the registry but Enabled=false. Triggered by Style.Enabled == false.
//
// Remediation path: enable the style in YAML (flip true + reload)
// OR pick a different style. This is distinct from ErrUnknownStyle
// (which means "fix the config or the call site name") so callers
// can log the right actionable hint.
//
// Identity: pkg/styleerrors.ErrStyleDisabled IS
// image/styles.ErrStyleDisabled at the Go value-equality level (the
// image/styles package re-exports the same `errors.New(...)` value
// via a value-alias, not a re-declaration). A future wave-tracker
// phase will fully retire the image/styles re-export; for now both
// import paths dispatch identically via errors.Is.
var ErrStyleDisabled = errors.New("styleerrors: style is disabled")

// ErrEmptyPrompt is returned when the composed prompt cannot be
// produced because BOTH the user-supplied prompt and the style's
// PromptSuffix are empty (or whitespace-only). Triggered when
// strings.TrimSpace(prompt) == "" AND strings.TrimSpace(style.PromptSuffix) == "".
//
// Pre-A2 silent fallback: prompt empty → return PromptSuffix (or
// prompt unchanged if both were empty — silently emitting the empty
// string). Post-A2: empty prompt + empty suffix is a real
// "no composed text" condition and surfaces a typed error so the
// caller knows the render is empty rather than emitting an empty
// string downstream to a model with no surfaced signal.
var ErrEmptyPrompt = errors.New("styleerrors: prompt and style suffix are both empty")

// ErrStyleVersionMismatch is returned when ApplyStyle's version
// argument is non-zero (caller pinned a specific version) AND the
// loaded style's StyleVersion differs. Triggered by
// version > 0 AND int(style.Version) != version.
//
// version=0 means "wildcard" — caller opted out of the version-pin
// gate and accepts whatever the registry loaded. This mirrors the
// godlike/07 no-fake-availability contract: a non-zero version pin
// is an explicit caller-supplied intent that must be honoured, not
// silently absorbed.
var ErrStyleVersionMismatch = errors.New("styleerrors: style version does not match caller-supplied pin")

// ── PR-CS-1 / FASE 6 (July 2026, DoD #8) ───────────────────────────────
// Script-segment validation sentinels. Per godlike/06 SSOT, this
// file is the canonical SSoT for fail-closed validation sentinels
// across the script generation pipeline. Payload-layer validators
// wire these into HTTP 400 responses via *scriptpkg.PayloadValidationError
// (Code field, e.g. "TOO_MANY_SEGMENTS") and *scriptpkg.PlanInvalidError
// (Details list, mapped to code="INVALID_PAYLOAD"). The ApplyStyle
// sentinels above are unchanged; the script sentinels below are
// ADDITIVE and live alongside them.

// ErrSegmentsEmpty — caller explicitly sends `segments: []` on the
// wire (present-but-empty payload). Distinct from "segments field
// absent" (silent default, caller may have meant to omit). DoD #8
// fail-closed. Wire: invalid `segments`
// shape → 400 with detail "script_params.segments must not be empty
// when present".
var ErrSegmentsEmpty = errors.New("styleerrors: script_params.segments must not be empty when present")

// ErrSegmentTopicEmpty — per-block Topic is blank or whitespace-only
// for at least one ScriptSegment. Each per-block Topic is required
// at runtime (validator enforces non-empty). The validator surfaces
// the index in the error message so the operator can identify the
// offending row. Wire: 400 INVALID_PAYLOAD with detail like
// "script_params.segments[3].topic is required".
var ErrSegmentTopicEmpty = errors.New("styleerrors: script_params.segments[i].topic is required")

// ErrTargetWordsNotPositive — target_words <= 0 AND segments is
// absent (canonical "single-target mode"). In the new Segments
// mode the target is per-block, so the validator accepts
// TargetWords=0 when len(Segments) > 0; this sentinel fires only
// when no segments are present AND no target was supplied.
// Existing logic kept verbatim per user spec ("mantiene invariata
// logica esistente"): same Code (INVALID_TARGET_WORDS) + same
// message ("target_words must be > 0"), only the pre-condition
// `len(Segments)==0` is added to the conjunction.
var ErrTargetWordsNotPositive = errors.New("styleerrors: script_params.target_words must be > 0 when no segments are present")

// ErrTooManySegments — len(Segments) exceeds the operator cap
// MaxSegmentsCap (default 50, configurable via
// VELOX_SCRIPTS_MAX_SEGMENTS_CAP). The canonical cap lives in
// config.ScriptsConfig; this sentinel surfaces the typed identity
// so retries + classifiers can dispatch via errors.Is. Wire: 400
// PayloadValidationError Code="TOO_MANY_SEGMENTS" with extra
// {actual_segments, max_segments_cap}.
var ErrTooManySegments = errors.New("styleerrors: script_params.segments has too many entries")
