package autotag

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/enrichment"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
)

type Service struct {
	db          *sql.DB
	repo        asset.Repository
	vlmClient   *vlm.Client
	dispatcher  mutations.AssetMutationDispatcher
	enrichState enrichment.EnrichStateMachinePort
	log         *zap.Logger
}

// NewService constructs an autotag.Service. The dispatcher is the
// canonical AssetMutationDispatcher (atomic media_assets UPSERT +
// asset.index.requested outbox event) used to persist VLM metadata
// changes and trigger Qdrant re-indexing. enrichState is the
// canonical state-machine port for media_assets.enrich_state
// transitions (PR-ENRICHMENT-STATE-MACHINE).
func NewService(db *sql.DB, repo asset.Repository, vlmClient *vlm.Client, dispatcher mutations.AssetMutationDispatcher, enrichState enrichment.EnrichStateMachinePort, log *zap.Logger) *Service {
	return &Service{
		db:          db,
		repo:        repo,
		vlmClient:   vlmClient,
		dispatcher:  dispatcher,
		enrichState: enrichState,
		log:         log,
	}
}

// TagAsset analyzes a single asset with VLM and persists the updated
// metadata through the canonical AssetMutationDispatcher. The
// dispatcher performs an atomic SQLite UPSERT + emits an
// asset.index.requested outbox event; the outbox worker then handles
// Qdrant re-indexing, replacing the previous goroutine-based direct
// Qdrant call.
func (s *Service) TagAsset(ctx context.Context, a *asset.Asset) error {
	s.log.Info("auto-tagging asset", zap.String("id", a.ID), zap.String("path", a.LocalPath()))

	// 1. Call VLM sidecar and measure analysis duration.
	start := time.Now()
	vTags, usedModel, err := s.vlmClient.AutoTagLocal(ctx, a.LocalPath(), string(a.MediaType))
	duration := time.Since(start)
	if err != nil {
		// Mark as skipped in metadata so we don't keep retrying if it's a permanent failure (e.g. file corrupt)
		a.SetMetadataString("vlm_tag_error", err.Error())
		a.SetMetadataString("vlm_tagged", "failed")
		if derr := s.persistVLM(ctx, a); derr != nil {
			return fmt.Errorf("vlm autotag failed and dispatcher persistence failed: vlm=%w; dispatcher=%v", err, derr)
		}
		return fmt.Errorf("vlm autotag: %w", err)
	}

	// Prefer the model reported by the sidecar; fall back to the configured model.
	if usedModel == "" {
		usedModel = s.vlmClient.Model()
	}
	modelVersion := s.vlmClient.ModelVersion()
	if modelVersion == "" {
		modelVersion = usedModel
	}

	// 2. Build VLM-specific tags and keep the aggregated Tags view consistent.
	vlmTagSet := make(map[string]bool)
	for _, o := range vTags.VisualObjects {
		vlmTagSet[strings.ToLower(o)] = true
	}
	for _, m := range vTags.Mood {
		vlmTagSet[strings.ToLower(m)] = true
	}
	if vTags.SceneType != "" {
		vlmTagSet[strings.ToLower(vTags.SceneType)] = true
	}
	if vTags.Lighting != "" {
		vlmTagSet[strings.ToLower(vTags.Lighting)] = true
	}

	vlmTags := make([]string, 0, len(vlmTagSet))
	for t := range vlmTagSet {
		vlmTags = append(vlmTags, t)
	}
	a.VLMTags = vlmTags
	a.RebuildTags()

	// 3. Update metadata with full structured VLM info
	a.SetMetadataString("vlm_tagged", "success")
	a.SetMetadataString("vlm_model", usedModel)
	a.SetMetadataString("vlm_model_version", modelVersion)
	a.SetMetadataInt("vlm_analysis_duration_ms", int(duration.Milliseconds()))
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

	// 4. Persist atomically through the canonical mutation dispatcher.
	// The dispatcher UPSERTs the row and emits an asset.index.requested
	// outbox event in a single transaction; the outbox worker will drive
	// Qdrant re-indexing. This replaces the previous repo.Upsert +
	// goroutine vectorStore.UpsertFromClip pattern.
	if err := s.persistVLM(ctx, a); err != nil {
		return fmt.Errorf("dispatcher.EnqueueAndIndex after VLM: %w", err)
	}

	return nil
}

// persistVLM resolves a deterministic content hash for the asset and
// dispatches the canonical EnqueueAndIndex. If the asset has no
// file_hash in metadata, it computes SHA256 from the local file and
// stores it back into the asset so the dispatcher's supersede gate
// and the outbox event_key are stable.
func (s *Service) persistVLM(ctx context.Context, a *asset.Asset) error {
	hash, err := s.contentHashFor(a)
	if err != nil {
		return err
	}
	if err := s.dispatcher.EnqueueAndIndex(ctx, a, hash); err != nil {
		return fmt.Errorf("EnqueueAndIndex: %w", err)
	}
	return nil
}

// contentHashFor returns the asset's file_hash if present, otherwise
// computes SHA256 of the local file and caches it on the asset.
func (s *Service) contentHashFor(a *asset.Asset) (string, error) {
	if a == nil {
		return "", fmt.Errorf("asset is nil")
	}
	if h := a.FileHash(); h != "" {
		return h, nil
	}
	path := a.LocalPath()
	if path == "" {
		return "", fmt.Errorf("asset %s has no file_hash and no local_path", a.ID)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s for content hash: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	hash := hex.EncodeToString(h.Sum(nil))
	a.SetFileHash(hash)
	return hash, nil
}
