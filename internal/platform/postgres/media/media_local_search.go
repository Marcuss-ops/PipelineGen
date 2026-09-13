package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// LocalMediaSearchRequest is the canonical input for the PostgreSQL lexical
// (non-vector) media search: the local/hash/keyword path that previously read
// SQLite media_assets. All filters are optional; Text is matched against the
// name, search_text and search_terms columns.
type LocalMediaSearchRequest struct {
	Text string
	// AllTerms is the multi-keyword form: every term must match, which is the
	// exact AND semantics of the retired SQLite `clip_search_terms` inverted
	// index lookup (HAVING COUNT(DISTINCT term) = N). It is the PostgreSQL
	// replacement for that index: the canonical term corpus now lives on the
	// media_assets.search_terms column, so no secondary term table is needed.
	// A term only needs to appear (substring) in name, search_text or
	// search_terms; Text and AllTerms compose (both must hold).
	AllTerms            []string
	Source              string // empty or "all" means every source
	Category            string
	MediaType           string
	Limit               int
	ExcludeUnclassified bool
}

// SearchLocal performs a bounded lexical search over the PostgreSQL media
// SSOT. It is the single non-vector media search surface; callers must not
// re-introduce a SQLite media mirror.
func (s *MediaSearcher) SearchLocal(ctx context.Context, req LocalMediaSearchRequest) ([]MediaAssetRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres media: local search not wired")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	text := strings.TrimSpace(req.Text)
	pattern := "%" + strings.ToLower(text) + "%"
	source := strings.TrimSpace(req.Source)
	if source == "all" {
		source = ""
	}

	query := `
		SELECT ` + mediaAssetReadColumns + `
		FROM media_assets
		WHERE ($1 = '' OR LOWER(name) LIKE $2 OR LOWER(search_text) LIKE $2 OR LOWER(search_terms) LIKE $2)
		  AND ($3 = '' OR source = $3)
		  AND ($4 = '' OR category = $4)
		  AND ($5 = '' OR media_type = $5)`
	args := []any{text, pattern, source, strings.TrimSpace(req.Category), strings.TrimSpace(req.MediaType)}
	// One AND clause per term: the caller asked for every keyword to match.
	for _, term := range req.AllTerms {
		trimmed := strings.ToLower(strings.TrimSpace(term))
		if trimmed == "" {
			continue
		}
		args = append(args, "%"+trimmed+"%")
		index := len(args)
		query += fmt.Sprintf("\n\t\t  AND (LOWER(name) LIKE $%d OR LOWER(search_text) LIKE $%d OR LOWER(search_terms) LIKE $%d)", index, index, index)
	}
	if req.ExcludeUnclassified {
		query += " AND asset_kind <> ''"
	}
	query += `
		  AND lifecycle_state IN ('ACTIVE', 'INDEXED', 'READY', 'PUBLISHED')
		ORDER BY updated_at DESC, id ASC
		LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media: local search: %w", err)
	}
	defer rows.Close()
	return scanMediaAssetRows(rows)
}

// FindAllByHash returns every asset matching the supplied fingerprint (raw hex
// or `sha256:`-prefixed). This is the PostgreSQL counterpart of the retired
// SQLite hash search.
func (s *MediaSearcher) FindAllByHash(ctx context.Context, hash string, limit int) ([]MediaAssetRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres media: local search not wired")
	}
	candidates := mediaHashCandidates(hash)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("postgres media: hash is required")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+mediaAssetReadColumns+`
		FROM media_assets
		WHERE binary_sha256 = $1 OR content_sha256 = $1 OR legacy_file_md5 = $1
		   OR binary_sha256 = $2 OR content_sha256 = $2 OR legacy_file_md5 = $2
		ORDER BY updated_at DESC, id ASC
		LIMIT $3
	`, candidates[0], candidates[len(candidates)-1], limit)
	if err != nil {
		return nil, fmt.Errorf("postgres media: find all by hash: %w", err)
	}
	defer rows.Close()
	return scanMediaAssetRows(rows)
}

func scanMediaAssetRows(rows *sql.Rows) ([]MediaAssetRecord, error) {
	out := make([]MediaAssetRecord, 0, 16)
	for rows.Next() {
		rec, err := scanMediaAssetRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres media: scan media row: %w", err)
		}
		out = append(out, *rec)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("postgres media: iterate media rows: %w", err)
	}
	return out, nil
}
