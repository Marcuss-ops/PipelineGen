// Package artlist — SemanticEnricher. PR 7 (codex/asset-manifest-cutover,
// June 2026): per-asset metadata writes no longer run through a single
// shared lock + private []map[string]any merge inline. They route
// through internal/application/assets/manifest.Service which owns the
// per-path lock + atomic merge-by-AssetID + temp+fsync+rename
// local writes + drive-then-replace remote writes.
//
// Pre-cutover symbols removed in this file:
//   - the global shared mutex (removed)
//   - the (e *SemanticEnricher) private metadata merge method (removed)
//
// Pre-cutover imports cleaned up:
//   - encoding/json (manifest owns marshaling)
//   - os (manifest owns atomic writes)
//   - path/filepath (manifest owns path computation)
//   - sync (manifest owns locking; the per-path lock registry in
//           manifest.pathLockRegistry replaces the global mutex.)
package artlist

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/manifest"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// SemanticEnricher arricchisce un clip Artlist con metadati semantici.
// Usa il semantic_tagger.py per generare search_text, concept_tags, subjects, mood,
// e un embedding compatto (concept_tags serializzati come JSON) per la ricerca ibrida.
//
// L'enrichment viene eseguito in background dopo il salvataggio iniziale del clip,
// quindi non blocca mai la pipeline principale di download.
//
// PR2.5: dispatcher is now a constructor argument (was SetDispatcher
// setter previously — removed). PR2.7: driveUploader (*drive.Uploader concrete)
// is replaced by driveManager (DriveFolderManager port). PR7:
// manifestSvc added; the legacy updateCumulativeMetadataJSON +
// enrichMetaMu mutex dance is gone.
type SemanticEnricher struct {
	repo         AssetStore
	indexer      Indexer
	metaWriter   *semantic.MetadataWriter
	driveManager DriveFolderManager
	log          *zap.Logger
	// dispatcher is the canonical media_index_outbox dispatcher used by
	// Enrich() to combine UpsertClip + indexed-Qdrant in a single tx.
	// PR2.5: constructor argument (no SetDispatcher anymore).
	dispatcher Dispatcher
	// PR 7: manifestSvc is the canonical AssetManifestService. The
	// enricher delegates all per-fold/per-local metadata writes
	// here; legacy local-file + Drive-side writes are gone.
	manifestSvc manifest.Service
}

// NewSemanticEnricher creates a new enricher.
//
// PR 7: manifestSvc added. Pass nil for test fixtures / opt-out
// flows where the enrichment result is desired but per-fold
// manifest update is not.
func NewSemanticEnricher(
	repo AssetStore,
	indexer Indexer,
	metaWriter *semantic.MetadataWriter,
	driveManager DriveFolderManager,
	dispatcher Dispatcher,
	manifestSvc manifest.Service,
	log *zap.Logger,
) *SemanticEnricher {
	return &SemanticEnricher{
		repo:         repo,
		indexer:      indexer,
		metaWriter:   metaWriter,
		driveManager: driveManager,
		dispatcher:   dispatcher,
		manifestSvc:  manifestSvc,
		log:          log,
	}
}

// EnrichAsync avvia l'enrichment in background (fire-and-forget).
func (e *SemanticEnricher) EnrichAsync(parentCtx context.Context, clip *asset.Asset, term string) {
	if clip == nil || clip.ID == "" {
		return
	}
	clipCopy := *clip
	concurrent.SafeGo("artlist-enrich", func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parentCtx), 30*time.Second)
		defer cancel()
		if err := e.Enrich(ctx, &clipCopy, term); err != nil {
			e.log.Warn("artlist semantic enrichment failed",
				zap.String("clip_id", clipCopy.ID),
				zap.String("term", term),
				zap.Error(err),
			)
		}
	})
}

// dispatchOrIndexAndUpsert performs UpsertClip + IndexClip atomically via
// the canonical media_index_outbox dispatcher.
func (e *SemanticEnricher) dispatchOrIndexAndUpsert(ctx context.Context, clip *asset.Asset, hash string) {
	bridge, err := e.newDispatchBridge()
	if err != nil {
		e.log.Warn("dispatchOrIndexAndUpsert: dispatcher not wired", zap.Error(err))
		return
	}
	if err := bridge.Dispatch(ctx, clip, hash); err != nil {
		e.log.Warn("dispatchOrIndexAndUpsert: dispatch failed",
			zap.String("clip_id", clip.ID), zap.Error(err))
	}
}

func (e *SemanticEnricher) newDispatchBridge() (*dispatchBridge, error) {
	if e.dispatcher == nil {
		return nil, fmt.Errorf("artlist: dispatcher is required")
	}
	return &dispatchBridge{
		dispatcher: e.dispatcher,
		assetStore: e.repo,
		indexer:    e.indexer,
		log:        e.log,
	}, nil
}

// Enrich esegue il tagger e aggiorna il DB + manifest con i metadati
// semantici. PR 7: post-enrichment per-fold/per-local metadata
// writes route through manifest.Service. The pre-cutover shared
// lock + private metadata merge + []map[string]any patterns
// are gone.
func (e *SemanticEnricher) Enrich(ctx context.Context, clip *asset.Asset, term string) error {
	if e.metaWriter == nil {
		return fmt.Errorf("metadata writer not configured")
	}

	prompt := buildArtlistPrompt(clip.Name, term, clip.Tags)
	style := "cinematic"

	e.log.Debug("enriching artlist clip semantically",
		zap.String("clip_id", clip.ID),
		zap.String("prompt_preview", textutil.Truncate(prompt, 80)),
	)

	payload, _, err := e.metaWriter.GeneratePayload(ctx, semantic.WriteRequest{
		AssetID:   clip.ID,
		AssetType: "clip",
		MediaType: "video",
		Source:    "artlist",
		Generator: "artlist_scraper",
		Style:     style,
		Prompt:    prompt,
	})
	if err != nil {
		return fmt.Errorf("metaWriter.GeneratePayload: %w", err)
	}

	// Ricarichiamo il clip dal DB per non sovrascrivere campi aggiornati nel frattempo.
	existing, err := e.repo.Get(ctx, clip.ID)
	if err != nil || existing == nil {
		existing = clip
	}

	if payload.SearchText != "" {
		existing.SearchText = payload.SearchText
	}
	existing.Tags = deduplicateStrings(append(existing.Tags, payload.Tags...))
	if payload.Subjects != nil {
		existing.SearchTerms = deduplicateStrings(append(existing.SearchTerms, payload.Subjects...))
	}

	if existing.Metadata == nil {
		existing.Metadata = make(map[string]any)
	}
	if len(payload.ConceptTags) > 0 {
		existing.Metadata["concept_tags"] = payload.ConceptTags
	}
	if len(payload.Mood) > 0 {
		existing.Metadata["mood"] = payload.Mood
	}
	if len(payload.Categories) > 0 {
		existing.Metadata["categories"] = payload.Categories
	}
	if len(payload.VisualObjects) > 0 {
		existing.Metadata["visual_objects"] = payload.VisualObjects
	}
	if len(payload.EmotionalTone) > 0 {
		existing.Metadata["emotional_tone"] = payload.EmotionalTone
	}
	if payload.SemanticDescription != "" {
		existing.Metadata["semantic_description"] = payload.SemanticDescription
	}
	existing.Metadata["semantic_enriched"] = true
	if payload.RetrievalScore != nil {
		existing.Metadata["semantic_confidence"] = *payload.RetrievalScore
	} else {
		existing.Metadata["semantic_confidence"] = 0.0
	}

	if err := e.repo.Upsert(ctx, existing); err != nil {
		return fmt.Errorf("upsert after enrichment: %w", err)
	}

	// PR 7 cutover: per-asset manifest write is delegated to
	// manifest.Service using the SAME AssetToEntry mapper as the
	// pre-enrichment path. The upsert is best-effort: failures are
	// logged but do not abort the rest of Enrich — the clip's DB
	// row is the source of truth.
	if e.manifestSvc != nil && existing.LocalPath() != "" {
		entry := manifest.AssetToEntry(existing, "artlist", term, map[string]any{
			"filename":     existing.Filename,
			"clip_page_url": existing.ClipPageURL,
			"source_url":   existing.ExternalURL(),
		})
		if entry.Metadata == nil {
			entry.Metadata = make(map[string]any)
		}
		entry.Metadata["duration_sec"] = existing.Duration.Seconds()
		entry.Metadata["created_at"] = existing.CreatedAt.Format(time.RFC3339)

		// 1) Local manifest atomic write.
		if dir := filepath.Dir(existing.LocalPath()); dir != "" {
			if lerr := e.manifestSvc.UpsertLocal(ctx, dir, entry); lerr != nil {
				e.log.Warn("manifest: local upsert failed",
					zap.String("clip_id", existing.ID), zap.Error(lerr))
			}
		}
		// 2) Drive manifest (per-folder locked replace).
		folderID := existing.FolderID()
		if folderID == "" {
			folderID = existing.ParentFolderID()
		}
		if folderID != "" {
			if rerr := e.manifestSvc.UpsertRemote(ctx, folderID, entry); rerr != nil {
				e.log.Warn("manifest: remote upsert failed",
					zap.String("clip_id", existing.ID), zap.Error(rerr))
			}
		}
	}

	// Ricalcola embedding veri e indicizza in Qdrant via dispatcher.
	e.dispatchOrIndexAndUpsert(ctx, existing, existing.FileHash())

	// Update search terms index after semantic enrichment.
	if updateErr := e.repo.UpdateSearchTerms(ctx, existing.ID, "artlist", existing.Name, existing.Tags, existing.SearchText); updateErr != nil {
		e.log.Warn("failed to update search terms after enrichment",
			zap.String("clip_id", existing.ID),
			zap.Error(updateErr),
		)
	}

	e.log.Info("artlist clip enriched",
		zap.String("clip_id", existing.ID),
		zap.Int("tags_count", len(existing.Tags)),
		zap.String("search_text_preview", textutil.Truncate(existing.SearchText, 60)),
	)

	return nil
}

// buildArtlistPrompt costruisce un prompt descrittivo per il tagger
// combinando il titolo del clip con il termine di ricerca originale.
func buildArtlistPrompt(name, term string, tags []string) string {
	parts := []string{}
	if name != "" {
		parts = append(parts, name)
	}
	if term != "" && !strings.EqualFold(name, term) {
		parts = append(parts, "search term: "+term)
	}
	for _, t := range tags {
		if t != "" && !strings.EqualFold(t, term) && !strings.EqualFold(t, name) {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "stock video clip"
	}
	return strings.Join(parts, ", ")
}

// deduplicateStrings rimuove i duplicati preservando l'ordine.
func deduplicateStrings(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s == "" {
			continue
		}
		lc := strings.ToLower(s)
		if _, ok := seen[lc]; !ok {
			seen[lc] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
