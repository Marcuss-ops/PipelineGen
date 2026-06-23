// TYPES-TO-PACKAGE SHIM — internal-only (Phase 1 of PR4, June 2026).
//
// This file is now ZERO-EXTERNAL-IMPORTERS. All external callers (channel
// monitor, stock pipeline, YouTube HTTP handlers) have been migrated to
// import `youtube/types/` directly in Commit X (PR4 phase 1).
//
// The shim is RETAINED because the INTERNAL files inside the
// `internal/application/youtube/` parent package still reference the alias
// symbols via package-scope, plus the inline `TopicSearchRequest` struct
// (defined directly here, not yet moved to youtube/types/). Sweeping the
// internal references (and the inline type) is PR4-B scope.
//
// Per PR5 Phase 3 (June 2026): extraction DTOs (ExtractRequest,
// DestinationRequest, ExtractResponse, ExtractItem, FolderInfo, ExtractStats)
// moved to canonical `internal/application/youtube/types/types.go`. The
// zero-copy aliases here preserve backward compatibility for all in-package
// callers (extractor.go, metadata_enrich.go, extraction/segment_processor.go).
//
// Per PR4 (June 2026): External API surface is now CANONICAL.
//   - External: 0 importers
//   - Internal: ~6 files still use alias symbols (PR4-B sweep queued)
//
// Rule: do NOT add new fields to `TopicSearchRequest` — they belong in
// youtube/types/types.go.
package youtube

import types "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"

// Segment is a zero-copy alias for types.Segment. Internal to the youtube
// package only. External consumers MUST import types.Segment directly.
type Segment = types.Segment

// PR5 Phase 3 (June 2026): extraction DTOs moved to youtube/types/ so the
// extraction capability service can import them without an import cycle.
// These aliases preserve backward compatibility for all existing callers.

type ExtractRequest = types.ExtractRequest
type DestinationRequest = types.DestinationRequest
type ExtractResponse = types.ExtractResponse
type FolderInfo = types.FolderInfo
type ExtractStats = types.ExtractStats
type ExtractItem = types.ExtractItem

// PR4 Phase 1 (June 2026): the previous inline-defined TopicSearchRequest
// was MOVED to youtube/types/types.go so external HTTP handlers can
// reference it via the canonical sub-package. This file now re-exports
// the canonical type as a zero-copy alias for any in-package caller
// still using `youtube.TopicSearchRequest`. PR4-B will (a) sweep the ~6
// internal call sites (extractor.go, metadata_enrich.go, etc.) so they
// read `topics.TopicSearchRequest` directly; (b) drop this alias
// declaration AND remove ports.go + types.go entirely.
type TopicSearchRequest = types.TopicSearchRequest
