package mediapipeline

import (
	"fmt"

	"velox/go-master/pkg/hashutil"
	"velox/go-master/pkg/pathutil"
)

type stableIDGenerator struct{}

func (g *stableIDGenerator) GenerateID(sourceURL string, req *PipelineRequest) string {
	return stableSlugID(pathutil.Slug(req.Source), req.Source, req.MediaType, sourceURL)
}

func (s *Service) StableClipID(item *WorkItem, req *PipelineRequest) string {
	parts := []string{
		req.Source,
		req.MediaType,
		item.SourceURL,
	}
	if item.SegmentSpec != nil {
		parts = append(parts, item.SegmentSpec.Start, item.SegmentSpec.End)
	}
	return stableSlugID(pathutil.Slug(req.Source), parts...)
}

// stableSlugID generates a stable ID with a slug prefix and truncated MD5 hash of parts.
func stableSlugID(prefix string, parts ...string) string {
	shortHash := hashutil.ShortMD5(parts, 12)
	return fmt.Sprintf("%s_%s", prefix, shortHash)
}
