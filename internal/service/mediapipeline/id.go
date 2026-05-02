package mediapipeline

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"

	"velox/go-master/pkg/pathutil"
)

type stableIDGenerator struct{}

func (g *stableIDGenerator) GenerateID(sourceURL string, req *PipelineRequest) string {
	components := []string{
		req.Source,
		req.MediaType,
		sourceURL,
	}

	hashInput := strings.Join(components, "|")
	hash := md5.Sum([]byte(hashInput))
	shortHash := hex.EncodeToString(hash[:])[:12]

	return fmt.Sprintf("%s_%s", pathutil.Slug(req.Source), shortHash)
}

func (s *Service) StableClipID(item *WorkItem, req *PipelineRequest) string {
	components := []string{
		req.Source,
		req.MediaType,
		item.SourceURL,
	}

	if item.SegmentSpec != nil {
		components = append(components, item.SegmentSpec.Start, item.SegmentSpec.End)
	}

	hashInput := strings.Join(components, "|")
	hash := md5.Sum([]byte(hashInput))
	shortHash := hex.EncodeToString(hash[:])[:12]

	return fmt.Sprintf("%s_%s", pathutil.Slug(req.Source), shortHash)
}
