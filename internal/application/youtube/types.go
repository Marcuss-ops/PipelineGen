// Deprecated: import youtube/types/ directly instead.
//
// This file contains zero-copy type aliases that re-export the canonical
// youtube/types/ package. It exists ONLY for backward compatibility.
// Once all importers are migrated, DELETE this file.
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

type TopicSearchRequest struct {
	Q     string `form:"q" json:"q" binding:"required"`
	Limit int    `form:"limit" json:"limit"`
	Sort  string `form:"sort" json:"sort"`
}
