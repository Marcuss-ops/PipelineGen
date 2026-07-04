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
