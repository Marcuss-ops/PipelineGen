package clipindexer

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

const (
	// embeddingModel and embeddingModelVersion are appended to metadata_json
	// as $.embedding_model and $.embedding_model_version when a clip reaches
	// the "indexed" state. They are separate from vectorstore's
	// CurrentEmbeddingVersion / CurrentSearchTextVersion because those
	// track the *content schema* version, whereas these track the
	// *model identity* (which model produced the vector).
	// Bump these when switching models to force re-indexing of all assets.
	embeddingModel        = "multilingual-e5-base"
	embeddingModelVersion = "2026-06-16-v1"

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

// IndexClip generates embeddings for a clip and upserts it into Qdrant.
// Uses a state machine tracked in metadata_json.index_state:
//
//	pending → embedding → upserting → indexed
//	                      ↘ failed     ↘ retrying
//
// The fast path skips regeneration when BOTH embeddings exist AND the
// content hash matches AND index_state == "indexed".
// Clips without a transcript only require the semantic embedding to be valid.
func (s *Service) IndexClip(ctx context.Context, clipID string) error {
	if !s.cfg.Enabled {
		s.log.Debug("clipindexer disabled, skipping", zap.String("clip_id", clipID))
		return nil
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

	s.setIndexState(ctx, clipID, "embedding", "")

	if s.cfg.ServerURL != "" {
		err := s.indexViaAPI(ctx, clipID)
		if err == nil {
			return s.finalizeIndex(ctx, clipID, contentHash)
		}
		s.log.Warn("embedding server failed, falling back to script",
			zap.String("clip_id", clipID),
			zap.String("server_url", s.cfg.ServerURL),
			zap.Error(err))
	}

	err = s.indexViaScript(ctx, clipID)
	if err != nil {
		s.setIndexState(ctx, clipID, "failed", err.Error())
		return fmt.Errorf("indexViaScript failed for %s: %w", clipID, err)
	}
	return s.finalizeIndex(ctx, clipID, contentHash)
}

// tryFastPath returns true if the clip is already fully indexed with valid
// embeddings and a matching content hash. The caller short-circuits to nil;
// otherwise fast-path upsert is attempted and the result returned.
func (s *Service) tryFastPath(ctx context.Context, clipID, contentHash string, hasTranscript bool) bool {
	var hasSemantic bool
	var hasTranscriptEmb bool
	var storedHash string
	var indexState string
	err := s.db.QueryRowContext(ctx, `SELECT
		(embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' AND embedding_json != '{}'),
		(transcript_embedding IS NOT NULL AND transcript_embedding != '' AND transcript_embedding != '[]' AND transcript_embedding != '{}'),
		COALESCE(json_extract(metadata_json, '$.indexed_content_hash'), ''),
		COALESCE(json_extract(metadata_json, '$.index_state'), '')
		FROM media_assets WHERE id = ?`, clipID).Scan(&hasSemantic, &hasTranscriptEmb, &storedHash, &indexState)
	if err != nil {
		s.log.Warn("fast path check failed, will re-index",
			zap.String("clip_id", clipID), zap.Error(err))
		return false
	}

	embeddingsOK := hasSemantic && (hasTranscriptEmb || !hasTranscript)
	if !embeddingsOK || indexState != "indexed" || storedHash != contentHash {
		return false
	}

	s.log.Info("clip already indexed with valid content hash, fast-path upsert",
		zap.String("clip_id", clipID))

	if err := s.UpsertVectorStore(ctx, clipID); err != nil {
		s.setIndexState(ctx, clipID, "retrying", err.Error())
		s.log.Error("fast-path upsert failed", zap.String("clip_id", clipID), zap.Error(err))
		return false
	}

	if setErr := s.setIndexedAt(ctx, clipID, contentHash); setErr != nil {
		s.log.Error("fast-path: failed to persist indexed state, falling through to full re-index",
			zap.String("clip_id", clipID), zap.Error(setErr))
		return false
	}
	return true
}

// finalizeIndex upserts to vector store and persists the indexed state.
func (s *Service) finalizeIndex(ctx context.Context, clipID, contentHash string) error {
	s.setIndexState(ctx, clipID, "upserting", "")

	if err := s.UpsertVectorStore(ctx, clipID); err != nil {
		s.setIndexState(ctx, clipID, "failed", err.Error())
		return fmt.Errorf("Qdrant upsert failed for %s: %w", clipID, err)
	}

	if err := s.setIndexedAt(ctx, clipID, contentHash); err != nil {
		return fmt.Errorf("failed to persist indexed state for %s: %w", clipID, err)
	}
	s.log.Info("clip fully indexed and upserted to Qdrant", zap.String("clip_id", clipID))
	return nil
}

// IndexRunItems bulk-indexes a batch of clip items (from outside callers
// like the worker outbox dispatcher). Per-batch pre-filtering strips out
// metadata-only sidecars before any embedding work is done.
func (s *Service) IndexRunItems(ctx context.Context, items []map[string]any) error {
	if !s.cfg.Enabled {
		return nil
	}

	clipIDs := make([]string, 0, len(items))
	for _, item := range items {
		clipID, _ := item["clip_id"].(string)
		if clipID != "" {
			clipIDs = append(clipIDs, clipID)
		}
	}

	if len(clipIDs) == 0 {
		return nil
	}

	// Strip out metadata-only sidecars before we waste an embedding call on them.
	clipIDs = s.filterSkippableClipIDs(ctx, clipIDs)
	if len(clipIDs) == 0 {
		return nil
	}

	if s.cfg.ServerURL != "" {
		err := s.indexBulkAPI(ctx, clipIDs)
		if err == nil {
			if bulkErr := s.UpsertVectorStoreBulk(ctx, clipIDs); bulkErr != nil {
				s.log.Warn("bulk upsert to vector store failed", zap.Error(bulkErr))
				return bulkErr
			}
			return nil
		}
		s.log.Warn("bulk embedding server failed, falling back to individual indexing",
			zap.String("server_url", s.cfg.ServerURL),
			zap.Error(err))
	}

	for _, clipID := range clipIDs {
		if err := s.IndexClip(ctx, clipID); err != nil {
			s.log.Warn("failed to index clip", zap.String("clip_id", clipID), zap.Error(err))
		}
	}
	return nil
}
