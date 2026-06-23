// Package metadata provides YouTube clip metadata orchestration.
// The metadata subsystem is split across multiple files for clarity and
// bounded diff surface:
//
//   - core.go             : package doc + shared alias-free entry point
//   - enrich.go           : orchestration (writeClipMetadataFile)
//   - persist.go          : field accessors and content helpers
//   - service.go          : MetadataWriter surface + WriteClipMetadataFile
//
// Canonical types live in internal/application/youtube/types/; this
// package only consumes them. No type aliases are defined here —
// callers should import `youtube/types` directly when they need
// ClipMetadataFile (the prior metadata.ClipMetadataFile alias was
// removed in W16-PR4).
package metadata
