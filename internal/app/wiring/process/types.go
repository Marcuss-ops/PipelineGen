package process

import (
	capindexing "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/vlm"
)

// ProcessBundle holds the heavy media-processing adapters.
type ProcessBundle struct {
	MediaProcessor     detail.MediaProcessor
	ClipIndexerService *capindexing.ReindexRequesterHolder
	VLMClient          *vlm.Client
}

// IndexDeps is the small pre-phase dependency bundle used by outbox wiring.
// It carries only the PostgreSQL reindex requester; the legacy pgvector vector
// store was removed on 2026-11-17.
type IndexDeps struct {
	ClipIndexerService *capindexing.ReindexRequesterHolder
}
