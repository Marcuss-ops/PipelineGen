package script

import (
	"context"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/clips"
)

func BuildScriptDocument(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, dataDir, clipTextDir, nodeScraperDir string, StockDriveRepo, ArtlistRepo *clips.Repository) (*ScriptDocument, error) {
	return &ScriptDocument{
		Title:   req.Topic,
		Content: "Generated script for " + req.Topic,
		Sections: []ScriptSection{},
	}, nil
}
