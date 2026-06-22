package youtube

import (
	"context"
	"fmt"
)

// Extract processes a YouTube clip extraction request.
// Delegates to the extraction capability service (PR5 Phase 3).
func (s *Service) Extract(ctx context.Context, req *ExtractRequest) (*ExtractResponse, error) {
	if s.extraction == nil {
		return nil, fmt.Errorf("youtube: extraction service not wired — composition root must include extraction deps")
	}
	return s.extraction.Extract(ctx, req)
}
