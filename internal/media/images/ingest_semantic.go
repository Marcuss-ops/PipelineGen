package images

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/realtime"
	"velox/go-master/internal/media/vectorstore"
)

func (s *Service) callSemanticTagger(ctx context.Context, prompt, style, mediaType, generator string) (*SemanticMetadataPayload, error) {
	scriptPath := filepath.Join(s.scriptsDir, "semantic_tagger.py")
	args := []string{
		scriptPath,
		"--prompt", prompt,
		"--style", style,
		"--media-type", mediaType,
		"--generator", generator,
	}
	// Pass Ollama config for LLM enrichment at ingest time (one-shot, not at search time)
	if s.cfg != nil && s.cfg.External.OllamaURL != "" {
		args = append(args, "--ollama-url", s.cfg.External.OllamaURL)
	}
	if s.cfg != nil && s.cfg.External.OllamaModel != "" {
		args = append(args, "--ollama-model", s.cfg.External.OllamaModel)
	}

	cmd := exec.CommandContext(ctx, "python3", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("semantic_tagger failed: %w (output: %s)", err, string(output))
	}

	var payload SemanticMetadataPayload
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("decode semantic_tagger output: %w", err)
	}

	return &payload, nil
}

func (s *Service) callLLMFallback(ctx context.Context, mediaType, prompt, style string) string {
	if s.llmGen == nil {
		return prompt
	}
	desc, err := s.llmGen.GenerateDescription(ctx, mediaType, prompt, style)
	if err != nil {
		s.log.Warn("LLM fallback failed", zap.Error(err))
		return prompt
	}
	return desc
}

func (s *Service) indexAssetInVectorStore(ctx context.Context, assetID, source, name, localPath, driveLink, style, mediaType, searchText string, tags []string) {
	if s.vectorSvc == nil {
		return
	}

	// 1. Get embedding from Python server
	adapter := realtime.NewPythonEmbeddingAdapter(s.cfg.ClipIndexer.ServerURL)
	embedding, err := adapter.EmbedText(ctx, searchText)
	if err != nil {
		s.log.Warn("Failed to generate embedding for search_text", zap.String("asset_id", assetID), zap.Error(err))
		return
	}

	// Convert to float32
	vec := make([]float32, len(embedding))
	for i, f := range embedding {
		vec[i] = float32(f)
	}

	// 2. Upsert to Qdrant
	vAsset := vectorstore.VectorAsset{
		AssetID:       assetID,
		Source:        source,
		Name:          name,
		LocalPath:     localPath,
		DriveLink:     driveLink,
		Style:         style,
		MediaType:     mediaType,
		TextEmbedding: vec,
		Tags:          tags,
		CreatedAt:     time.Now(),
	}

	if err := s.vectorSvc.UpsertAsset(ctx, vAsset); err != nil {
		s.log.Warn("Failed to upsert to vector store", zap.String("asset_id", assetID), zap.Error(err))
		return
	}

	// 3. Update DB status if it's an image
	if mediaType == "image" {
		_ = s.repo.UpdateEmbeddingStatus(ctx, assetID, "ready")
	}

	s.log.Info("Asset indexed in vector store", zap.String("asset_id", assetID), zap.String("media_type", mediaType))
}
