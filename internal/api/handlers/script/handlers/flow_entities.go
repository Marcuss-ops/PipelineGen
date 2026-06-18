package handlers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/core"
	"github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// EntityScriptExtractor extracts entities from a script.
type EntityScriptExtractor interface {
	ExtractEntitiesFromScriptWithModel(ctx context.Context, segments []string, entityCount int, model string) (*core.FullEntityAnalysis, error)
}

// ExtractScriptEntities extracts entities from a script text and returns
// the JSON-serialized entity analysis.
func ExtractScriptEntities(ctx context.Context, extractor EntityScriptExtractor, script string, model string) (string, error) {
	if extractor == nil {
		return "", nil
	}

	segments := textutil.SplitScriptSentences(script)
	if len(segments) == 0 {
		script = strings.TrimSpace(script)
		if script != "" {
			segments = []string{script}
		}
	}
	if len(segments) > 12 {
		segments = sliceutil.GroupSentences(segments, 4)
	}

	analysis, err := extractor.ExtractEntitiesFromScriptWithModel(ctx, segments, 12, model)
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(analysis)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
