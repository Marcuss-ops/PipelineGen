// Package app — QDRANT-004 mediasearch adapters extracted from registry.go.
//
// Per AGENTS.md Pattern 5 (June 2026): one concept per file. This file holds
// the inline adapter types that WireRegistry uses to bridge the mediasearch
// service surface with concrete infrastructure types.
package app

import (
	"context"
	"strings"
	"time"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	mediasearch "github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/delivery"

	"go.uber.org/zap"
)

// ── QDRANT-004 adapters ────────────────────────────────────────────────────

// mediasearchVectorAdapter implements mediasearch.VectorSearchPort by
// combining an OllamaClient for embedding with a search.VectorStorePort
// for vector-store operations.
type mediasearchVectorAdapter struct {
	embedder interface {
		Embed(ctx context.Context, text string) ([]float32, error)
	}
	store assetsearch.VectorStorePort
}

func (a *mediasearchVectorAdapter) EmbedTextForVector(ctx context.Context, text, _ string) ([]float32, error) {
	return a.embedder.Embed(ctx, text)
}

func (a *mediasearchVectorAdapter) VectorStore() assetsearch.VectorStorePort {
	return a.store
}

// mediasearchReadAdapter implements mediasearch.MediaReadRepository using
// the existing ClipsRepository for batched SQLite reads.
type mediasearchReadAdapter struct {
	clips *assets.ClipsRepository
}

func (a *mediasearchReadAdapter) GetMany(ctx context.Context, _ mediasearch.WorkspaceContext, assetIDs []string) ([]mediasearch.MediaAsset, error) {
	if a.clips == nil || len(assetIDs) == 0 {
		return nil, nil
	}
	// Batch query via ClipsRepository.List with filter.IDs — single
	// SQL statement with WHERE id IN (?, ?, ...).
	// TODO QDRANT-001: filter by workspace_id when the column lands.
	clips, err := a.clips.List(ctx, asset.Filter{IDs: assetIDs})
	if err != nil {
		return nil, err
	}
	out := make([]mediasearch.MediaAsset, 0, len(clips))
	for _, clip := range clips {
		if clip == nil {
			continue
		}
		// Exclude soft-deleted rows per MediaReadRepository contract.
		ls := strings.ToLower(string(clip.LifecycleState))
		if ls == "deleted" {
			continue
		}
		lang, _ := clip.Metadata["language"].(string)
		width, _ := clip.Metadata["width"].(float64)
		height, _ := clip.Metadata["height"].(float64)
		out = append(out, mediasearch.MediaAsset{
			ID:         clip.ID,
			Name:       clip.Name,
			Source:     string(clip.Source),
			MediaType:  string(clip.MediaType),
			Category:   clip.Category,
			Tags:       clip.Tags,
			Language:   lang,
			DurationMs: int(clip.Duration.Milliseconds()),
			Width:      int(width),
			Height:     int(height),
			SearchText: clip.SearchText,
		})
	}
	return out, nil
}

// mediasearchDeliveryAdapter implements mediasearch.AssetDeliveryService
// as a noop until HMAC delivery URL signing is wired (QDRANT-005).
type mediasearchDeliveryAdapter struct{}

func (a *mediasearchDeliveryAdapter) BuildAuthorizedURL(_ context.Context, _ mediasearch.WorkspaceContext, _ string) (string, error) {
	// QDRANT-005: implement HMAC-signed delivery URLs.
	return "", nil
}

// mediasearchLogger adapts zap.Logger to mediasearch.Logger.
type mediasearchLogger struct {
	sugar *zap.SugaredLogger
}

func (l mediasearchLogger) Info(msg string, kv ...any)  { l.sugar.Infow(msg, kv...) }
func (l mediasearchLogger) Warn(msg string, kv ...any)  { l.sugar.Warnw(msg, kv...) }
func (l mediasearchLogger) Debug(msg string, kv ...any) { l.sugar.Debugw(msg, kv...) }

// ── QDRANT-004 delivery signer helper ───────────────────────────────────────

// buildMediasearchDeliverySvc returns a delivery.Signer when the HMAC secret
// is configured (≥32 chars), or a noop mediasearchDeliveryAdapter otherwise.
// Extracted to keep WireRegistry clean of inline signer construction.
func buildMediasearchDeliverySvc(cfgDeliveryHMACSecret, veloxBaseURL string, deliveryReplayWindowSec int, log *zap.Logger) mediasearch.AssetDeliveryService {
	if secret := strings.TrimSpace(cfgDeliveryHMACSecret); len(secret) >= 32 {
		deliveryBaseURL := strings.TrimSpace(veloxBaseURL)
		replayWindow := time.Duration(deliveryReplayWindowSec) * time.Second
		if replayWindow <= 0 {
			replayWindow = 5 * time.Minute
		}
		signer, err := delivery.NewSigner(
			[]byte(secret),
			replayWindow,
			deliveryBaseURL,
			"/api/internal/v1/deliver",
		)
		if err != nil {
			log.Warn("QDRANT-004: delivery signer init failed, falling back to noop", zap.Error(err))
			return &mediasearchDeliveryAdapter{}
		}
		log.Info("QDRANT-004: delivery signer wired with HMAC secret")
		return signer
	}
	return &mediasearchDeliveryAdapter{}
}
