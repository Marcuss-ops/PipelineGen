package asset

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// SearchByTerms searches clips using the indexed clip_search_terms table.
// This is O(log n) per term instead of O(n) full table scan with LIKE.
//
// Splits cleanly from search.go so the fast-path index lookup can evolve
// independently of the LIKE fallback in SearchClips.
func (s *AssetStoreSQLite) SearchByTerms(ctx context.Context, source string, keywords []string, limit int) ([]*Asset, error) {
	filtered := make([]string, 0, len(keywords))
	for _, k := range keywords {
		k = strings.TrimSpace(k)
		if len(k) >= 2 {
			filtered = append(filtered, strings.ToLower(k))
		}
	}
	if len(filtered) == 0 {
		return []*Asset{}, nil
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
		return []*Asset{}, nil
	}
	if limit > 0 && len(clipIDs) > limit {
		clipIDs = clipIDs[:limit]
	}

	return s.fetchClipsByIDs(ctx, source, clipIDs)
}

// fetchClipsByIDs fetches full MediaAsset records for a list of clip IDs.
// Private to this file because callers should always go through
// SearchByTerms (which respects the search_terms index); batch-fetch helpers
// belong only with the search path that knows the index schema.
func (s *AssetStoreSQLite) fetchClipsByIDs(ctx context.Context, source string, clipIDs []string) ([]*Asset, error) {
	if len(clipIDs) == 0 {
		return []*Asset{}, nil
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

		var results []*Asset
		for rows.Next() {
			clip, err := scanCanonicalAssetRows(rows)
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

	var results []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, clip)
	}
	return results, rows.Err()
}

// UpdateSearchTerms tokenizes a clip's text fields and populates the clip_search_terms table.
// Call this after upserting a clip or after semantic enrichment updates search_text.
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

// ── Companion to migration 091 — derived search_terms backfill ───────
//
// DeriveSearchTerms produces a normalized keyword list from an Asset's
// text-bearing columns and metadata_json fields, intended to backfill
// the `media_assets.search_terms` JSON column when callers don't
// supply one. Today the only ingest path that DOES supply it is the
// semantic_enricher (Artlist); YouTube / image / stock paths left the
// column at the schema default '[]', which made LIKE-based search
// visibility drop after the indexed companion table was added.
//
// mergeSearchTerms combines caller-supplied a.SearchTerms (from
// semantic_enricher, manual API updates via /api/media/:source/clips,
// etc.) on the LEFT with derived tokens on the RIGHT so caller values
// take precedence in the order. Both pre-sets are lowercased, trimmed,
// length-filtered (≥2 chars), and deduplicated — same contract as the
// clip_search_terms inverted index in UpdateSearchTerms.
//
// Wired into store.Save so every ingest path now populates the column
// without the caller having to think about it; matches the spirit of
// AGENTS.md Pattern 7 ("Reusing existing services": the asset
// processor is the canonical Writer — extensions belong here, not at
// each caller).

// deriveStripper is the punctuation-strip set used by
// DeriveSearchTerms / mergeSearchTerms before rune-count filtering.
//
// IMPORTANT: this is an *abbreviation-preserving variant* — it does
// NOT strip `.` or `,`, while UpdateSearchTerms (the
// clip_search_terms inverted-index helper above) DOES strip them. The
// two helpers are therefore NOT in lockstep on dotted / comma content
// by design; the JSON column media_assets.search_terms favors recall
// (`A.I.` survives as `a.i.`) while the inverted index favors
// precision (each letter under the ≥2-byte gate probably drops out).
//
// Real asset names and topic metadata carry `A.I.`, `U.S.A.`, `Ph.D.`,
// `c/o`, `repubblica.it`, `4.5K`, etc.; per-char `.`/`,` strip would
// collapse those to empty (each letter is a 1-rune token dropped by
// the length filter). The cost: sentence-end periods like
// `Tokyo. Smith.` keep the periods inline — acceptable for a
// substring-search index since substring recall on `Tokyo` still
// matches `Tokyo.`.
//
// Apostrophes (`'`) and double-quotes (`"`) become a single space
// so contractions tokenize cleanly: `O'Connor` → `[O connor]` →
// `connor` survives (the 1-rune `O` drops out cleanly); `it's`
// → `[it s]` → `it` survives. Quoted-pair glyphs become spaces,
// not empty strings, so word fragments don't glue across punctuation
// boundaries.
var deriveStripper = strings.NewReplacer(
	"!", " ", "?", " ", ";", " ", ":", " ",
	"(", " ", ")", " ", "[", " ", "]", " ",
	"{", " ", "}", " ", "<", " ", ">", " ",
	"-", " ", "/", " ", "\\", " ",
	"'", " ", "\"", " ",
)

// normalizeToken strips punctuation, lowercases, and trims. Returns the
// canonical form to feed into the seen-set, OR empty string when the
// token has zero meaningful words after stripping.
func normalizeToken(s string) string {
	s = deriveStripper.Replace(s)
	s = strings.ToLower(strings.TrimSpace(s))
	return s
}

// addNormalized stamps a token through normalizeToken + the dedupe-set;
// always tokenizes via Fields so multi-word post-strip output (e.g.
// `[BTS]` -> `BTS` after bracket strip, `Tokyo Tower` -> `tokyo tower`)
// collapses to per-word entries instead of one aliased blob.
func addNormalized(out []string, seen map[string]struct{}, token string) []string {
	t := normalizeToken(token)
	if t == "" {
		return out
	}
	for _, w := range strings.Fields(t) {
		if utf8.RuneCountInString(w) < 2 {
			continue
		}
		if _, ok := seen[w]; !ok {
			seen[w] = struct{}{}
			out = append(out, w)
		}
	}
	return out
}

// DeriveSearchTerms returns a normalized keyword slice from an Asset's
// text fields + metadata_json keys. Safe on nil Asset. Source is NOT
// derived — it's a faceted discriminator (`youtube`/`artlist`/`stock`/
// `image`), not content; folding it in would bloat every clip's column
// with non-semantic noise. Faceted filtering on source lives in
// SearchClipsAdvanced (search.go).
//
// Field call order (locks the JSON-array contract; substring recall is
// order-invariant so this documents sequencing for testability):
//
//	Name → Filename → SearchText → Category → Tags → metadata_json keys
//
// Order rationale: the curated title (Name) precedes auto-derived
// descriptive text (SearchText) precedes fine-grained labels (Tags).
// Substring search does not depend on order; this order only affects
// the JSON array rendered for human debug.
func DeriveSearchTerms(a *Asset) []string {
	if a == nil {
		return []string{}
	}
	seen := make(map[string]struct{}, 16)
	out := make([]string, 0, 16)

	out = addNormalized(out, seen, a.Name)
	out = addNormalized(out, seen, a.Filename)
	out = addNormalized(out, seen, a.SearchText)
	out = addNormalized(out, seen, a.Category)
	for _, t := range a.Tags {
		out = addNormalized(out, seen, t)
	}

	// metadata_json keys that the clipindexer / semantic enricher populate
	// — same shape as RebuildSearchTerms' SELECT projection below so the
	// JSON column and the indexed companion table stay conceptually aligned.
	if a.Metadata != nil {
		for _, k := range []string{
			"clean_title", "clip_summary", "hook", "topics",
			"speakers", "mentioned_people", "people",
			"clip_tags", "search_keywords", "embedding_text",
		} {
			v, ok := a.Metadata[k]
			if !ok {
				continue
			}
			switch val := v.(type) {
			case string:
				out = addNormalized(out, seen, val)
			case []string:
				for _, s := range val {
					out = addNormalized(out, seen, s)
				}
			case []any:
				for _, item := range val {
					if s, ok := item.(string); ok {
						out = addNormalized(out, seen, s)
					}
				}
			}
		}
	}

	return out
}

// mergeSearchTerms adds caller-supplied terms first (precedence), then
// derived terms (backfill). Both pre-sets go through the same
// per-char punctuation strip + Fields tokenization + rune-count ≥ 2 +
// dedupe contract as DeriveSearchTerms so the merged list is never
// bloated with garbage tokens or JSON literals. Returns []string{}
// (not nil) so json.Marshal renders "[]" rather than "null".
//
// Side effect: the returned slice is a fresh allocation; caller
// inputs are not mutated. Safe to call on slices that other code holds
// references to.
func mergeSearchTerms(callerSupplied, derived []string) []string {
	seen := make(map[string]struct{}, len(callerSupplied)+len(derived))
	out := make([]string, 0, len(callerSupplied)+len(derived))
	for _, s := range callerSupplied {
		out = addNormalized(out, seen, s)
	}
	for _, s := range derived {
		out = addNormalized(out, seen, s)
	}
	return out
}

// RebuildSearchTerms re-indexes all existing clips' search terms from name, tags, search_text, and the clipindexer search helpers stored in metadata_json.
// This is used to populate the index for existing data after migration.
func (s *AssetStoreSQLite) RebuildSearchTerms(ctx context.Context, source string, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = 100
	}

	// After migration 059, search_text is a canonical column; the rest are
	// still in metadata_json (clipindexer output: clean_title, hook, topics, etc).
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
