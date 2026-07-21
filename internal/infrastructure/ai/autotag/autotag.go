package autotag

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/pkg/contextutil"
)

type Service struct {
	db          *sql.DB
	repo        asset.Repository
	vlmClient   *vlm.Client
	vectorStore clipindexer.VectorStoreIndexer
	log         *zap.Logger
}

// NewService constructs an autotag.Service. Vector store is wired via
// constructor injection (optional — pass nil when Qdrant indexing is
// disabled). Post-construction SetVectorStore setter has been removed in
// PR4-H Commit 4.
func NewService(db *sql.DB, repo asset.Repository, vlmClient *vlm.Client, vectorStore clipindexer.VectorStoreIndexer, log *zap.Logger) *Service {
	return &Service{
		db:          db,
		repo:        repo,
		vlmClient:   vlmClient,
		vectorStore: vectorStore,
		log:         log,
	}
}

// TagAsset analyzes a single asset with VLM and updates its metadata in DB and Qdrant.
func (s *Service) TagAsset(ctx context.Context, a *asset.Asset) error {
	s.log.Info("auto-tagging asset", zap.String("id", a.ID), zap.String("path", a.LocalPath()))

	// 1. Call VLM sidecar
	vTags, err := s.vlmClient.AutoTagLocal(ctx, a.LocalPath(), string(a.MediaType))
	if err != nil {
		// Mark as skipped in metadata so we don't keep retrying if it's a permanent failure (e.g. file corrupt)
		a.SetMetadataString("vlm_tag_error", err.Error())
		a.SetMetadataString("vlm_tagged", "failed")
		s.repo.Upsert(ctx, a)
		return fmt.Errorf("vlm autotag: %w", err)
	}

	// 2. Merge tags
	newTags := make(map[string]bool)
	for _, t := range a.Tags {
		newTags[strings.ToLower(t)] = true
	}
	for _, o := range vTags.VisualObjects {
		newTags[strings.ToLower(o)] = true
	}
	for _, m := range vTags.Mood {
		newTags[strings.ToLower(m)] = true
	}
	if vTags.SceneType != "" {
		newTags[strings.ToLower(vTags.SceneType)] = true
	}
	if vTags.Lighting != "" {
		newTags[strings.ToLower(vTags.Lighting)] = true
	}

	finalTags := make([]string, 0, len(newTags))
	for t := range newTags {
		finalTags = append(finalTags, t)
	}
	a.Tags = finalTags

	// 3. Update metadata with full structured VLM info
	a.SetMetadataString("vlm_tagged", "success")
	a.SetMetadataString("vlm_model", "nvidia/nemotron-nano-12b-v2-vl:free")
	a.SetMetadataString("scene_type", vTags.SceneType)
	a.SetMetadataString("lighting", vTags.Lighting)
	a.SetMetadataString("composition", vTags.Composition)

	if len(vTags.DominantColors) > 0 {
		colors, _ := json.Marshal(vTags.DominantColors)
		a.SetMetadataString("dominant_colors", string(colors))
	}
	if len(vTags.TextOnScreen) > 0 {
		text, _ := json.Marshal(vTags.TextOnScreen)
		a.SetMetadataString("text_on_screen", string(text))
	}

	// 4. Save to Repository
	if err := s.repo.Upsert(ctx, a); err != nil {
		return fmt.Errorf("repo upsert: %w", err)
	}

	// 5. Trigger Qdrant Re-index (post-write: must survive caller context cancellation)
	if s.vectorStore != nil {
		go func() {
			indexCtx, cancel := contextutil.PostWriteContext(ctx, s.log, "vector reindex", 2*time.Minute)
			defer cancel()
			if err := s.vectorStore.UpsertFromClip(indexCtx, a.ID); err != nil {
				s.log.Error("failed to trigger vector re-indexing for tagged asset", zap.String("id", a.ID), zap.Error(err))
			}
		}()
	}

	return nil
}
