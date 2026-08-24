// Package generated (application/images/generated) — errors.go holds
// the typed sentinels for the generated-image territory.
// Per PR-IMG-SPLIT-5 (July 2026), errors live in their own file.
package generation

import "errors"

// ErrProviderUnavailable is returned when Google Slides is not wired or is
// temporarily unavailable.
var ErrProviderUnavailable = errors.New("generated image provider unavailable")
