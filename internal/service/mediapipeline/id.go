package mediapipeline

import (
	"velox/go-master/pkg/idutil"
	"velox/go-master/pkg/pathutil"
)

type stableIDGenerator struct{}

func (g *stableIDGenerator) GenerateID(sourceURL string, req *PipelineRequest) string {
	return idutil.StableSlugID(pathutil.Slug(req.Source), req.Source, req.MediaType, sourceURL)
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
	return idutil.StableSlugID(pathutil.Slug(req.Source), parts...)
}
