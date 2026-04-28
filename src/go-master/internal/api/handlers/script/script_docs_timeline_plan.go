package script

import (
	"context"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/clips"
)

func buildTimelinePlan(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, narrative string, analysis *types.FullEntityAnalysis, dataDir string, repo *clips.Repository, nodeScraperDir string, artlistSvc interface{}) (*TimelinePlan, error) {
	return nil, nil
}
