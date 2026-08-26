package clipindexer

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// bm25SchemaVersion is the hash-stamp field for the BM25 lexical-index
// schema. The sparse
// vector identity that used to live in qdrant.BM25SchemaVersion is now
// a local package const. Bumping this constant invalidates every
// previously-stored content_hash, forcing a one-time re-index.
const bm25SchemaVersion = "v0-removed-qdrant-pg034"

// computeContentHash builds a deterministic SHA-256 hash from the clip's
// searchable content (name + search_text + transcript) plus the embedding
// model/collection version stamps. Used by tryFastPath and setIndexedAt
// for idempotency — if the hash matches what's stored in metadata_json,
// the clip is already fully indexed and we can skip re-embedding.
func (s *Service) computeContentHash(ctx context.Context, clipID string) (hash string, hasTranscript bool, err error) {
	// search_text is a canonical column (migration 059); clean_transcript
	// stays in metadata_json because it's a clipindexer-derived helper,
	// not a column on the schema.
	var name, searchText, cleanTranscript string
	err = s.db.QueryRowContext(ctx,
		`SELECT
			COALESCE(name, ''),
			COALESCE(search_text, ''),
			COALESCE(json_extract(metadata_json, '$.clean_transcript'), '')
		FROM media_assets WHERE id = ?`, clipID).Scan(&name, &searchText, &cleanTranscript)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, fmt.Errorf("clip not found: %s", clipID)
		}
		return "", false, fmt.Errorf("compute content hash: %w", err)
	}
	content := strings.Join([]string{
		"name:" + name,
		"search_text:" + searchText,
		"transcript:" + cleanTranscript,
		"model:" + embeddingModel,
		"model_ver:" + embeddingModelVersion,
		"coll_ver:" + collectionVersion,
		"bm25_ver:" + bm25SchemaVersion,
	}, "|")
	contentParts := strings.SplitN(content, "|model:", 2)
	if len(contentParts) == 2 && strings.TrimSpace(contentParts[0]) == "name:|search_text:|transcript:" {
		return "", false, nil
	}
	return digest.SHA256String(content), strings.TrimSpace(cleanTranscript) != "", nil
}
