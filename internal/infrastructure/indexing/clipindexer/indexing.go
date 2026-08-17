package clipindexer

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

const (
	// embeddingModel and embeddingModelVersion are appended to metadata_json
	// as $.embedding_model and $.embedding_model_version when a clip reaches
	// the "indexed" state. They are separate from vectorstore's
	// CurrentEmbeddingVersion / CurrentSearchTextVersion because those
	// track the *content schema* version, whereas these track the
	// *model identity* (which model produced the vector).
	//
	// SSOT (godlike/06): the canonical embedding identity lives in
	// internal/kernel/embedding (CanonicalText). These now anchor to the
	// canonical constants so the airlock stamps the real model identity.
	embeddingModel        = coreembedding.ModelIDMultilingualE5
	embeddingModelVersion = coreembedding.ModelRevisionMultilingualE5

	// collectionVersion tracks the Qdrant collection schema/alias binding.
	// When the collection schema changes (e.g. new named vector, payload
	// field, BM25 tokenization rules), bump this and all clips will be
	// identified as needing re-indexing via content hash mismatch.
	// QDRANT-003: bumped to v3 to match the new versioned collection schema
	// with real SigLIP visual vectors and CLAP audio (no synthetic placeholders).
	collectionVersion = "v3"
)

// EmbeddingModel returns the current embedding model name.
func EmbeddingModel() string { return embeddingModel }

// EmbeddingModelVersion returns the current embedding model version.
func EmbeddingModelVersion() string { return embeddingModelVersion }

// CollectionVersion returns the current collection version.
func CollectionVersion() string { return collectionVersion }

// IndexAsset is the canonical entry point of MediaIndexer. It resolves the
// searchability decision via the single IndexEligibilityResolver and indexes
// the asset only when it is SEARCHABLE. Registered-but-not-searchable assets
// (voiceover, final_audio, bgm, sfx, …) are skipped without any embedding work.
func (s *Service) IndexAsset(ctx context.Context, assetID string) error {
	eligibility, err := s.Eligibility(ctx, assetID)
	if err != nil {
		if errors.Is(err, capregistry.ErrTaxonomySchemaUnavailable) {
			// Compatibility window for databases predating the taxonomy
			// migration. Once the columns exist, every lookup error is
			// fail-closed below.
			s.log.Debug("taxonomy columns unavailable; using legacy indexing path",
				zap.String("asset_id", assetID))
			return s.indexAsset(ctx, assetID)
		}
		// Taxonomy is the canonical searchability gate. If it cannot be
		// read, fail closed: do not guess that a registered row is
		// searchable and do not start embedding work. The caller/outbox can
		// retry after the registry becomes readable.
		return fmt.Errorf("resolve index eligibility for %q: %w", assetID, err)
	}
	if eligibility == capregistry.IndexEligibilityRegistered {
		s.log.Debug("asset registered but not semantic-searchable, skipping indexing",
			zap.String("asset_id", assetID),
			zap.String("eligibility", string(eligibility)))
		return nil
	}
	return s.indexAsset(ctx, assetID)
}

// IndexClip is the legacy compatible wrapper. It delegates to the canonical
// IndexAsset so existing clip-vocabulary callers converge without a rename.
func (s *Service) IndexClip(ctx context.Context, clipID string) error {
	return s.IndexAsset(ctx, clipID)
}

// indexAsset generates embeddings for an asset and upserts it into Qdrant.
// Uses the canonical state machine in media_assets.index_state (column,
// QDRANT-002 PR6 / migration 094) — see internal/kernel/asset/index_state.go
// for the IndexState enum:
//
//	DISCOVERED → EMBEDDING → EMBEDDED → INDEXING → INDEXED
//	                 ↓                       ↓
//	            EMBEDDING_FAILED        INDEXING_FAILED
//	                                                → DELETE_PENDING → DELETED
//
// Task 2 (July 2026): EMBEDDING means "generating embedding vectors".
// EMBEDDED means "vectors saved to SQLite, Qdrant NOT yet updated".
// INDEXING means "pushing to Qdrant". INDEXED is terminal success
// (point verified + vectors validated + payload verified).
//
// The fast path skips regeneration when BOTH embeddings exist AND the
// content hash matches AND index_state == asset.StateIndexed.
// Clips without a transcript only require the semantic embedding to be valid.
//
// Writers to media_assets.index_state:
//   - setIndexState (all transient + failure states; refuses to write INDEXED).
//   - setIndexedAt (terminal INDEXED + sidecar metadata in single atomic UPDATE).
func (s *Service) indexAsset(ctx context.Context, clipID string) error {
	if !s.cfg.Enabled {
		// godlike/07 no-fake-availability (PR-QDRANT-INDEXCLIP-GUARD,
		// July 2026): when cfg.Enabled=false but an
		// asset.index.requested event arrived anyway (the upstream
		// outbox emitted it before the operator flipped the bit),
		// return the typed sentinel so the IndexingHandler can
		// distinguish "indexer off" from a real success. The
		// sentinel triggers a transient skip+retry path
		// (state INDEXING_SKIPPED_NO_INDEXER + outbox retry) so
		// the event lands once the indexer is back online.
		// Pre-fix (return nil) was a silent fake-availability:
		// outbox marked Completed even though no embedding work
		// happened — operators only found out via downstream
		// Qdrant count drift.
		s.log.Warn("clipindexer disabled, returning sentinel for outbox retry",
			zap.String("clip_id", clipID))
		return ErrIndexClipDisabledButEventRequested
	}

	// Fast early-out: skip metadata-only asset names BEFORE any embedding work.
	// These rows exist in media_assets (sidecars ingested by Drive upload) but
	// are not real searchable media and would just pollute the vector store.
	if skippable, name := s.shouldSkipByName(ctx, clipID); skippable {
		s.log.Debug("skipping indexing for non-media asset name",
			zap.String("clip_id", clipID),
			zap.String("name", name))
		return nil
	}

	contentHash, hasTranscript, err := s.computeContentHash(ctx, clipID)
	if err != nil {
		s.log.Warn("failed to compute content hash, will re-index",
			zap.String("clip_id", clipID), zap.Error(err))
		contentHash = ""
		hasTranscript = false
	}

	if contentHash != "" {
		if s.tryFastPath(ctx, clipID, contentHash, hasTranscript) {
			return nil
		}
	}

	// Transition to EMBEDDING: embedding generation is about to start.
	if err := s.setIndexState(ctx, clipID, asset.StateEmbedding, ""); err != nil {
		return fmt.Errorf("setIndexState EMBEDDING for %s: %w", clipID, err)
	}

	// Read source_version from the DB for the CAS fence in setIndexedAt.
	// A failed read is fatal: continuing with sourceVersion="" would
	// silently degrade the CAS fence (BLOCKER #2) to a no-op.
	var sourceVersion string
	if svErr := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(source_version, '') FROM media_assets WHERE id = ?`,
		clipID,
	).Scan(&sourceVersion); svErr != nil {
		return fmt.Errorf("failed to read source_version for CAS fence on %s: %w", clipID, retry.WrapTransient(svErr))
	}

	if s.cfg.ServerURL == "" {
		err := retry.WrapTransient(fmt.Errorf("embedding server is not configured"))
		_ = s.setIndexState(ctx, clipID, asset.StateEmbeddingFailed, err.Error())
		return err
	}

	err = s.indexViaAPI(ctx, clipID)
	if err != nil {
		if setErr := s.setIndexState(ctx, clipID, asset.StateEmbeddingFailed, err.Error()); setErr != nil {
			s.log.Error("failed to persist embedding failed state", zap.String("clip_id", clipID), zap.Error(setErr))
		}
		return retry.WrapTransient(fmt.Errorf("embedding server failed for %s: %w", clipID, err))
	}
	// Embeddings are now in SQLite via the canonical API — transition to EMBEDDED.
	if setErr := s.setIndexState(ctx, clipID, asset.StateEmbedded, ""); setErr != nil {
		s.log.Error("failed to persist EMBEDDED state after script", zap.String("clip_id", clipID), zap.Error(setErr))
	}
	return s.finalizeIndex(ctx, clipID, contentHash, sourceVersion)
}

// tryFastPath returns true if the clip is already fully indexed or has
// valid embeddings ready for upsert.
//
// Task 2: the fast-path also accepts EMBEDDED state — a clip whose
// embeddings were saved but the Qdrant upsert never completed (e.g.
// crash between EMBEDDED and INDEXED). When the content hash matches
// and embeddings are valid, the fast path re-upserts without
// re-generating embeddings.
func (s *Service) tryFastPath(ctx context.Context, clipID, contentHash string, hasTranscript bool) bool {
	var hasSemantic bool
	var hasTranscriptEmb bool
	var storedHash string
	var indexState string
	var sourceVersion string
	err := s.db.QueryRowContext(ctx, `SELECT
		(embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' AND embedding_json != '{}'),
		(transcript_embedding IS NOT NULL AND transcript_embedding != '' AND transcript_embedding != '[]' AND transcript_embedding != '{}'),
		COALESCE(json_extract(metadata_json, '$.indexed_content_hash'), ''),
		COALESCE(index_state, ''),
		COALESCE(source_version, '')
		FROM media_assets WHERE id = ?`, clipID).Scan(&hasSemantic, &hasTranscriptEmb, &storedHash, &indexState, &sourceVersion)
	if err != nil {
		s.log.Warn("fast path check failed, will re-index",
			zap.String("clip_id", clipID), zap.Error(err))
		return false
	}

	embeddingsOK := hasSemantic && (hasTranscriptEmb || !hasTranscript)
	// Task 2: accept INDEXED (normal fast path) OR EMBEDDED (embeddings
	// saved but Qdrant upsert never completed). Re-upserting an EMBEDDED
	// clip skips embedding generation while recovering the Qdrant point.
	indexedOrEmbedded := indexState == string(asset.StateIndexed) || indexState == string(asset.StateEmbedded)
	if !embeddingsOK || !indexedOrEmbedded || storedHash != contentHash {
		return false
	}

	s.log.Info("clip has valid embeddings, fast-path upsert",
		zap.String("clip_id", clipID))

	if err := s.UpsertVectorStore(ctx, clipID); err != nil {
		if setErr := s.setIndexState(ctx, clipID, asset.StateIndexingFailed, err.Error()); setErr != nil {
			s.log.Error("fast-path: failed to persist index failed state", zap.String("clip_id", clipID), zap.Error(setErr))
		}
		s.log.Error("fast-path upsert failed", zap.String("clip_id", clipID), zap.Error(err))
		return false
	}

	if setErr := s.setIndexedAt(ctx, clipID, contentHash, sourceVersion); setErr != nil {
		s.log.Error("fast-path: failed to persist indexed state, falling through to full re-index",
			zap.String("clip_id", clipID), zap.Error(setErr))
		return false
	}
	return true
}

// finalizeIndex upserts to vector store and persists the indexed state.
//
// Task 2: this function now expects the row to be in EMBEDDED state
// (embeddings saved, Qdrant not yet updated). It transitions to INDEXING
// before the upsert, then to INDEXED on success, or INDEXING_FAILED on
// failure.
func (s *Service) finalizeIndex(ctx context.Context, clipID, contentHash, sourceVersion string) error {
	// Transition EMBEDDED → INDEXING: Qdrant upsert is about to start.
	if err := s.setIndexState(ctx, clipID, asset.StateIndexing, ""); err != nil {
		return fmt.Errorf("setIndexState INDEXING for %s: %w", clipID, err)
	}

	if err := s.UpsertVectorStore(ctx, clipID); err != nil {
		if setErr := s.setIndexState(ctx, clipID, asset.StateIndexingFailed, err.Error()); setErr != nil {
			s.log.Error("failed to persist indexing-failed state", zap.String("clip_id", clipID), zap.Error(setErr))
		}
		return fmt.Errorf("Qdrant upsert failed for %s: %w", clipID, err)
	}

	if err := s.setIndexedAt(ctx, clipID, contentHash, sourceVersion); err != nil {
		return fmt.Errorf("failed to persist indexed state for %s: %w", clipID, err)
	}
	s.log.Info("clip fully indexed and upserted to Qdrant", zap.String("clip_id", clipID))
	return nil
}
