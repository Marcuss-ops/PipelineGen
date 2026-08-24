// Package metadata — download placeholder.
//
// download.go is reserved for future download-related helpers
// (YouTube clip download, transcript fetch, subtitle retrieval).
// Currently no download functions live in this package — the
// download surface is owned by internal/infrastructure/youtube/.
// This file exists per the LONG-FILES-SPLIT-2026-07-06 user spec
// which requested a download.go capability file.
//
// Forward-pointer: when download helpers migrate into this package,
// they MUST respect the Pattern 0 typed-port discipline (call
// through ClipMetadataBuilder / ClipMetadataWriter, not direct
// repository access).
package metadata
