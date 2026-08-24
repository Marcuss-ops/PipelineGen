// Package vlm — typed-error contract (godlike/07) for Azione 13 of
// CUTOVER-COMPLETE-WITH-ARTIFACTS (July 2026).
//
// ErrVLMDisabled is the canonical sentinel returned by every
// *Client.{VisualTagImage, ValidateScript, DedupCheck, AutoTagLocal}
// method when Client.IsEnabled() reports false. Production callers MUST
// probe via errors.Is(err, ErrVLMDisabled); the former
// fmt.Errorf("vlm client disabled") string-based site is now
// godlike/07-typed.
package vlm

import "errors"

// ErrVLMDisabled is the typed-error carrier for the Client.IsEnabled gate.
// Triggered when vlm.enabled is false OR the sidecar URL is empty.
// Use `errors.Is(err, ErrVLMDisabled)` at the caller site.
var ErrVLMDisabled = errors.New("vlm client disabled")
