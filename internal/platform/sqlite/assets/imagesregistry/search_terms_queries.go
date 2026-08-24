// Package assets — search_terms index SQL queries (Wave C: moved from
// internal/kernel/asset/search_terms.go).
//
// The PURE Go helpers (DeriveSearchTerms/normalizeToken/addNormalized/
// deriveStripper/mergeSearchTerms) STAY in the domain package — they
// have no SQL dependencies. The 4 SQL receivers (SearchByTerms/
// fetchClipsByIDs/UpdateSearchTerms/RebuildSearchTerms) migrate here.
package imagesregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── SQL receivers (migrated from search_terms.go) ────────────────────

// SearchByTerms searches clips using the indexed clip_search_terms
// table. This is O(log n) per term instead of O(n) full table scan
// with LIKE.
//
// Splits cleanly from search_queries.go so the fast-path index lookup
// can evolve independently of the LIKE fallback in SearchClips.
func (s *AssetStoreSQLite) SearchByTerms(ctx context.Context, source string, keywords []string, limit int) ([]*asset.Asset, error) {
	filtered := make([]string, 0, len(keywords))
	for _, k := range keywords {
		k = strings.TrimSpace(k)
		if len(k) >= 2 {
			filtered = append(filtered, strings.ToLower(k))
		}
	}
	if len(filtered) == 0 {
		return []*asset.Asset{}, nil
	}

	placeholders := make([]string, len(filtered))
	args := make([]any, len(filtered))
	for i, term := range filtered {
		placeholders[i] = "?"
		args[i] = term
	}

	termQuery := fmt.Sprintf(`
		SELECT clip_id
		FROM clip_search_terms
		WHERE term IN (%s)
		GROUP BY clip_id
		HAVING COUNT(DISTINCT term) = ?
	`, strings.Join(placeholders, ","))

	fullArgs := append(args, len(filtered))

	rows, err := s.db.QueryContext(ctx, termQuery, fullArgs...)
	if err != nil {
		return nil, fmt.Errorf("clip_search_terms query: %w", err)
	}
	defer rows.Close()

	var clipIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		clipIDs = append(clipIDs, id)
	}

	if len(clipIDs) == 0 {
		return []*asset.Asset{}, nil
	}
	if limit > 0 && len(clipIDs) > limit {
		clipIDs = clipIDs[:limit]
	}

	return s.fetchClipsByIDs(ctx, source, clipIDs)
}

// fetchClipsByIDs fetches full MediaAsset records for a list of clip
// IDs. Private to this file because callers should always go through
// SearchByTerms (which respects the search_terms index).
func (s *AssetStoreSQLite) fetchClipsByIDs(ctx context.Context, source string, clipIDs []string) ([]*asset.Asset, error) {
	if len(clipIDs) == 0 {
		return []*asset.Asset{}, nil
	}

	idPlaceholders := make([]string, len(clipIDs))
	idArgs := make([]any, len(clipIDs))
	for i, id := range clipIDs {
		idPlaceholders[i] = "?"
		idArgs[i] = id
	}

	query := buildMediaAssetQuery(source)

	if source != "" && source != "all" && source != "unified" {
		query += " AND id IN (" + strings.Join(idPlaceholders, ",") + ")"
		args := []any{source}
		args = append(args, idArgs...)
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var results []*asset.Asset
		for rows.Next() {
			clip, err := ScanCanonicalAssetRowsPublic(rows)
			if err != nil {
				return nil, err
			}
			results = append(results, clip)
		}
		return results, rows.Err()
	}

	query += " AND id IN (" + strings.Join(idPlaceholders, ",") + ")"
	rows, err := s.db.QueryContext(ctx, query, idArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*asset.Asset
	for rows.Next() {
		clip, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, clip)
	}
	return results, rows.Err()
}

// UpdateSearchTerms tokenizes a clip's text fields and populates the
// clip_search_terms table. Call this after upserting a clip or after
// semantic enrichment updates search_text.
func (s *AssetStoreSQLite) UpdateSearchTerms(ctx context.Context, clipID, source string, name string, tags []string, searchText string) error {
	termSet := make(map[string]struct{})

	addTerms := func(text string) {
		text = strings.ToLower(text)
		text = strings.NewReplacer(
			",", " ", ".", " ", "!", " ", "?", " ", ";", " ", ":", " ",
			"(", " ", ")", " ", "[", " ", "]", " ", "-", " ", "'", "",
			"\"", "", "/", " ", "\\", " ",
		).Replace(text)
		for _, word := range strings.Fields(text) {
			word = strings.TrimSpace(word)
			word = strings.NewReplacer(
				"à", "a", "è", "e", "é", "e", "ì", "i", "ò", "o", "ù", "u",
			).Replace(word)
			if len(word) >= 2 {
				termSet[word] = struct{}{}
			}
		}
	}

	addTerms(name)
	for _, t := range tags {
		addTerms(t)
	}
	addTerms(searchText)

	if len(termSet) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, "DELETE FROM clip_search_terms WHERE clip_id = ?", clipID)
	if err != nil {
		return fmt.Errorf("delete old terms: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO clip_search_terms (term, clip_id, source) VALUES (?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for term := range termSet {
		if _, err := stmt.ExecContext(ctx, term, clipID, source); err != nil {
			return fmt.Errorf("insert term %q: %w", term, err)
		}
	}

	return tx.Commit()
}

// RebuildSearchTerms re-indexes all existing clips' search terms from
// name, tags, search_text, and the clipindexer search helpers stored
// in metadata_json. This is used to populate the index for existing
// data after migration.
func (s *AssetStoreSQLite) RebuildSearchTerms(ctx context.Context, source string, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = 100
	}

	// After migration 059, search_text is a canonical column; the
	// rest are still in metadata_json (clipindexer output:
	// clean_title, hook, topics, etc).
	query := `
		SELECT
			id,
			COALESCE(name, ''),
			COALESCE(tags, '[]'),
			TRIM(
				COALESCE(search_text, '') || ' ' ||
				COALESCE(json_extract(COALESCE(metadata_json,'{}'), '$.clean_title'), '') || ' ' ||
				COALESCE(json_extract(COALESCE(metadata_json,'{}'), '$.clip_summary'), '') || ' ' ||
				COALESCE(json_extract(COALESCE(metadata_json,'{}'), '$.hook'), '') || ' ' ||
				COALESCE(json_extract(COALESCE(metadata_json,'{}'), '$.topics'), '') || ' ' ||
				COALESCE(json_extract(COALESCE(metadata_json,'{}'), '$.speakers'), '') || ' ' ||
				COALESCE(json_extract(COALESCE(metadata_json,'{}'), '$.mentioned_people'), '') || ' ' ||
				COALESCE(json_extract(COALESCE(metadata_json,'{}'), '$.people'), '') || ' ' ||
				COALESCE(json_extract(COALESCE(metadata_json,'{}'), '$.clip_tags'), '') || ' ' ||
				COALESCE(json_extract(COALESCE(metadata_json,'{}'), '$.search_keywords'), '') || ' ' ||
				COALESCE(json_extract(COALESCE(metadata_json,'{}'), '$.embedding_text'), '')
			)
		FROM media_assets`
	var args []any
	if source != "" && source != "all" {
		query += " WHERE source = ?"
		args = append(args, source)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("query clips: %w", err)
	}
	defer rows.Close()

	var total int
	for rows.Next() {
		var id, name, tagsJSON, searchText string
		if err := rows.Scan(&id, &name, &tagsJSON, &searchText); err != nil {
			continue
		}

		var tags []string
		_ = json.Unmarshal([]byte(tagsJSON), &tags)

		if err := s.UpdateSearchTerms(ctx, id, source, name, tags, searchText); err != nil {
			continue
		}
		total++

		if batchSize > 0 && total >= batchSize {
			break
		}
	}

	return total, rows.Err()
}
