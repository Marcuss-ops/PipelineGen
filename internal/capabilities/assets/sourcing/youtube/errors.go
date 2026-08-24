// File errors.go — canonical typed-error sentinels for the
// YouTubeRegistrar service. Extracted from service.go per AGENTS.md
// Pattern 5 v2 (1 concetto per file; code-motion pura, zero logica
// cambiata).
//
// godlike/06 SSOT one-canonical-owner-per-fact: these sentinels live
// ONLY here. Callers in service.go / register_helpers.go / adapters.go
// resolve them by package-local reference.
//
// godlike/07 typed-error contract: callers can probe via errors.Is
// without unwrapping parse-fragments (no string matching).
package assets

import "errors"

// ErrYouTubeDriveRequired is returned when Drive upload is mandatory and
// the Publisher fails (P0.2, July 2026). Callers can probe with errors.Is.
var ErrYouTubeDriveRequired = errors.New("youtube.Register: Drive upload is required but Publisher failed")
