package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/media/realtime"
)

type VisualFingerprint struct {
	Phash               string
	VisualEmbeddingJSON string
	Dimensions          int
	Width               int
	Height              int
}

func AnalyzeVisualFile(ctx context.Context, serverURL, imagePath string) (*VisualFingerprint, error) {
	serverURL = strings.TrimSpace(serverURL)
	imagePath = strings.TrimSpace(imagePath)
	if serverURL == "" || imagePath == "" {
		return nil, nil
	}
	adapter := realtime.NewPythonEmbeddingAdapter(serverURL)
	res, err := adapter.AnalyzeImage(ctx, imagePath)
	if err != nil {
		return nil, err
	}
	embeddingJSON, err := json.Marshal(res.Embedding)
	if err != nil {
		return nil, fmt.Errorf("marshal visual embedding: %w", err)
	}
	return &VisualFingerprint{Phash: res.PHash, VisualEmbeddingJSON: string(embeddingJSON), Dimensions: res.Dimensions, Width: res.Width, Height: res.Height}, nil
}

func AttachVisualFingerprint(meta *Payload, fp *VisualFingerprint) {
	if meta == nil || fp == nil {
		return
	}
	if fp.Phash != "" {
		meta.PHash = fp.Phash
	}
	if fp.VisualEmbeddingJSON != "" {
		meta.VisualEmbeddingJSON = fp.VisualEmbeddingJSON
	}
	if fp.Dimensions > 0 {
		meta.VisualDimensions = fp.Dimensions
	}
}
