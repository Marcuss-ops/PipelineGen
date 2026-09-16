// Package assets — search_terms index SQL queries (Wave C: moved from
// internal/kernel/asset/search_terms.go).
//
// The PURE Go helpers (DeriveSearchTerms/normalizeToken/addNormalized/
// deriveStripper/mergeSearchTerms) STAY in the domain package — they
// have no SQL dependencies. The SQL receivers (SearchByTerms/
// fetchClipsByIDs/UpdateSearchTerms) migrate here. RebuildSearchTerms was
// DELETED on 2026-09-16 (P2-9): it had zero callers and only re-populated the
// legacy clip_search_terms index, which no production reader consults.
package imagesregistry

import (
	"context"
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
