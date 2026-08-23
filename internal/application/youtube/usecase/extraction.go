// extraction.go — clip extraction facade.
//
// Split out of orchestrator.go in Step 4 so each usecase/ file owns exactly
// one responsibility. The full pipeline implementation lives in
// adapters/manifest_mgr.go + segment_processor.go; this file holds ONLY
// the thin orchestrator-side forwarder.
package usecase

import (
	"context"
	"fmt"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// Extract is the canonical clip-extraction entry point. The full pipeline
// implementation lives in adapters/manifest_mgr.go + segment_processor.go.
// This facade delegates to the extraction capability service.
//
// Phase 1b (June 2026): the inline implementation was removed from
// usecase/ because it referenced 7+ private methods from Service (same package).
// The thin facade preserves the call-site contract; the extraction service
// owns the real pipeline.
func (s *Service) Extract(ctx context.Context, req *youtubetypes.ExtractRequest) (*youtubetypes.ExtractResponse, error) {
	if s.extraction == nil {
		return nil, fmt.Errorf("youtube: extraction capability not wired (composition root must include extraction deps in ServiceDeps)")
	}
	return s.extraction.Extract(ctx, req)
}
