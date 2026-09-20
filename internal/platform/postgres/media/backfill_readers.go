// Package media — backfill_readers.go: the media reads the one-shot operator
// backfill/repair commands depend on (search_text composition, source_url
// convergence, embedding-contract stamping).
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): these readers replace the
// media_assets candidate scans those commands ran on the operational SQLite
// handle. media_assets is PostgreSQL-owned and that mirror holds no committed
// media rows, so a repair could only ever report zero candidates while the
// SSOT held them. Writes stay where they were — every repaired row is patched
// through the canonical persistence.AssetMutator — so read and write agree on
// one engine. Both readers are read-only by construction.
package media

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// SourceURLMetadataCandidate is one asset whose url column is not yet mirrored
// into metadata_json.source_url.
type SourceURLMetadataCandidate struct {
	AssetID   string
	SourceURL string
}

// BackfillReader answers one-shot media backfill/repair commands from the
// PostgreSQL media SSOT.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): this replaces the candidate
// scans that cmd/admin's backfill commands ran on the operational SQLite handle.
// Every one of those scans selected media_assets rows, and media_assets is
// PostgreSQL-owned; the operational mirror holds no committed media rows, so a
// repair could only ever report zero candidates while the SSOT held them. The
// writes stay where they were (each command patches through the canonical
// persistence.AssetMutator), so the read and the write now agree on one engine.
//
// This reader is read-only by construction.
type BackfillReader struct {
	db *sql.DB
}

// NewBackfillReader binds the reader to the media SSOT handle. A nil handle
// returns nil so callers fail closed rather than reading a second engine
// (godlike/07 no-fake-availability).
func NewBackfillReader(db *sql.DB) *BackfillReader {
	if db == nil {
		return nil
	}
	return &BackfillReader{db: db}
}

func (r *BackfillReader) ready(op string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("postgres media backfill: media reader unavailable (%s)", op)
	}
	return nil
}

// sourceURLMetadataPredicate mirrors the retired SQLite predicate: non-image
// rows with a populated url column whose metadata_json has no source_url key
// yet. `metadata_json::jsonb->>'key' IS NULL` is the PostgreSQL form of
// `json_extract(...) IS NULL`; the NULLIF guard keeps a blank jsonb cast from
// failing on the empty-string default.
const sourceURLMetadataPredicate = `
	COALESCE(media_type, '') <> 'image'
	  AND TRIM(COALESCE(url, '')) <> ''
	  AND COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'source_url' IS NULL`

// CountSourceURLMetadataCandidates returns the full candidate count, which the
// command reports even when a --limit bounds the repaired set.
func (r *BackfillReader) CountSourceURLMetadataCandidates(ctx context.Context) (int, error) {
	if err := r.ready("count source_url"); err != nil {
		return 0, err
	}
	var count int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE `+sourceURLMetadataPredicate).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres media backfill: count source_url candidates: %w", err)
	}
	return count, nil
}

// ListSourceURLMetadataCandidates returns the candidates in id order, bounded
// by limit when limit > 0.
func (r *BackfillReader) ListSourceURLMetadataCandidates(ctx context.Context, limit int) ([]SourceURLMetadataCandidate, error) {
	if err := r.ready("list source_url"); err != nil {
		return nil, err
	}
	query := `SELECT id, url FROM media_assets WHERE ` + sourceURLMetadataPredicate + ` ORDER BY id`
	var args []any
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media backfill: query source_url candidates: %w", err)
	}
	defer rows.Close()

	var out []SourceURLMetadataCandidate
	for rows.Next() {
		var rec SourceURLMetadataCandidate
		if err := rows.Scan(&rec.AssetID, &rec.SourceURL); err != nil {
			return nil, fmt.Errorf("postgres media backfill: scan source_url candidate: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media backfill: iterate source_url candidates: %w", err)
	}
	return out, nil
}

// ProviderTimestampCandidate is one asset whose canonical metadata_json is
// missing a key that a legacy column can still supply.
type ProviderTimestampCandidate struct {
	AssetID  string
	RawValue string
}

// providerTimestampRules is the canonical column → metadata-key convergence
// rule set. The SQL lives HERE rather than in the admin command (godlike/06
// SSOT): the command names a key, and this map is the only place that knows
// which column feeds it and how the value is stringified for the patch.
//
// The "missing key" test is the PostgreSQL jsonb arrow form with a NULLIF
// guard on the column default, replacing the retired json_extract(...) IS NULL
// predicate from the SQLite scan.
var providerTimestampRules = map[string]struct{ predicate, valueExpr string }{
	"source_provider": {
		`TRIM(COALESCE(source_provider, '')) <> '' AND COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'source_provider' IS NULL`,
		`source_provider`,
	},
	"source_video_id": {
		`TRIM(COALESCE(source_video_id, '')) <> '' AND COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'source_video_id' IS NULL`,
		`source_video_id`,
	},
	"start_sec": {
		`COALESCE(start_ms, 0) <> 0 AND COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'start_sec' IS NULL`,
		`(COALESCE(start_ms, 0) / 1000.0)`,
	},
	"end_sec": {
		`COALESCE(end_ms, 0) <> 0 AND COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'end_sec' IS NULL`,
		`(COALESCE(end_ms, 0) / 1000.0)`,
	},
}

// ProviderTimestampRuleKeys returns the canonical rule keys in report order, so
// a caller never iterates the map itself.
func ProviderTimestampRuleKeys() []string {
	return []string{"source_provider", "source_video_id", "start_sec", "end_sec"}
}

// CountProviderTimestampCandidates counts the union of the rule predicates —
// the "matched" figure the command reports.
func (r *BackfillReader) CountProviderTimestampCandidates(ctx context.Context) (int, error) {
	if err := r.ready("count provider timestamps"); err != nil {
		return 0, err
	}
	predicates := make([]string, 0, len(providerTimestampRules))
	for _, key := range ProviderTimestampRuleKeys() {
		predicates = append(predicates, providerTimestampRules[key].predicate)
	}
	var count int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE (`+strings.Join(predicates, " OR ")+`)`).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres media backfill: count provider timestamp candidates: %w", err)
	}
	return count, nil
}

// ListProviderTimestampCandidates returns every row the named rule can repair,
// in id order, as the stringified column value the patch will carry.
func (r *BackfillReader) ListProviderTimestampCandidates(ctx context.Context, key string) ([]ProviderTimestampCandidate, error) {
	if err := r.ready("list provider timestamps"); err != nil {
		return nil, err
	}
	rule, ok := providerTimestampRules[key]
	if !ok {
		return nil, fmt.Errorf("postgres media backfill: unknown provider timestamp rule %q", key)
	}
	query := `SELECT id, CAST(` + rule.valueExpr + ` AS TEXT) FROM media_assets WHERE ` + rule.predicate + ` ORDER BY id`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres media backfill: query provider timestamp candidates (%s): %w", key, err)
	}
	defer rows.Close()

	var out []ProviderTimestampCandidate
	for rows.Next() {
		var rec ProviderTimestampCandidate
		if err := rows.Scan(&rec.AssetID, &rec.RawValue); err != nil {
			return nil, fmt.Errorf("postgres media backfill: scan provider timestamp candidate (%s): %w", key, err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media backfill: iterate provider timestamp candidates (%s): %w", key, err)
	}
	return out, nil
}

// DurationBackfillQuery bounds the duration-repair candidate scan.
type DurationBackfillQuery struct {
	// FolderID selects assets in that Drive folder or catalog path. Empty means
	// no folder filter.
	FolderID string
	// IDs restricts the scan to an explicit asset set. Empty means no id filter.
	IDs []string
	// Limit is the row cap; zero means all.
	Limit int
	// Force includes rows that already carry a positive duration_ms.
	Force bool
}

// ListDurationBackfillCandidates returns the ACTIVE/INDEXED video assets whose
// duration still needs measuring, in id order.
//
// NOTE ON THE FOLDER FILTER: the retired SQLite scan matched
// `parent_folder_id = ? OR drive_folder_id = ? OR folder_id = ?`. PostgreSQL's
// media SSOT has NO drive_folder_id column (the folder projection is
// parent_folder_id + folder_id), so that disjunct is dropped rather than
// invented. The retired mirror column was a legacy leftover that the canonical
// folder resolver already stopped reading.
func (r *BackfillReader) ListDurationBackfillCandidates(ctx context.Context, q DurationBackfillQuery) ([]string, error) {
	if err := r.ready("list duration candidates"); err != nil {
		return nil, err
	}
	where := []string{
		"media_type IN ('video', 'clip')",
		"UPPER(COALESCE(lifecycle_state, '')) = 'ACTIVE'",
		"UPPER(COALESCE(index_state, '')) = 'INDEXED'",
		"(TRIM(COALESCE(drive_file_id, '')) <> '' OR TRIM(COALESCE(local_path, '')) <> '')",
	}
	if !q.Force {
		where = append(where, "COALESCE(duration_ms, 0) <= 0")
	}

	args := []any{}
	if len(q.IDs) > 0 {
		marks := make([]string, len(q.IDs))
		for i, id := range q.IDs {
			args = append(args, id)
			marks[i] = fmt.Sprintf("$%d", len(args))
		}
		where = append(where, "id IN ("+strings.Join(marks, ",")+")")
	}
	if folderID := strings.TrimSpace(q.FolderID); folderID != "" {
		args = append(args, folderID)
		mark := fmt.Sprintf("$%d", len(args))
		where = append(where, "(parent_folder_id = "+mark+" OR folder_id = "+mark+")")
	}

	query := "SELECT id FROM media_assets WHERE " + strings.Join(where, " AND ") + " ORDER BY id"
	if q.Limit > 0 {
		args = append(args, q.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media backfill: query duration candidates: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres media backfill: scan duration candidate: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media backfill: iterate duration candidates: %w", err)
	}
	return out, nil
}

// ClipFolderCandidate is one canonical YouTube clip whose folder projection the
// clip-folder realignment may need to match its physical Drive location.
type ClipFolderCandidate struct {
	ID          string
	DriveFileID string
	FolderID    string
	FolderPath  string
}

// ListYouTubeClipFolderCandidates returns the canonical YouTube clips that
// carry a Drive identity pointer, in id order, bounded by limit when limit > 0.
//
// Scope is deliberately the same as the retired SQLite scan: source='youtube'
// AND id LIKE 'yt_%'. The planner/stock bindings that share source='youtube' but
// carry raw Drive ids live under a different Drive root, so their folder fields
// belong to the stock pipeline and are excluded here.
func (r *BackfillReader) ListYouTubeClipFolderCandidates(ctx context.Context, limit int) ([]ClipFolderCandidate, error) {
	if err := r.ready("list clip folder candidates"); err != nil {
		return nil, err
	}
	query := `SELECT id, COALESCE(drive_file_id, ''), COALESCE(folder_id, ''), COALESCE(folder_path, '')
		FROM media_assets
		WHERE source = 'youtube'
		  AND id LIKE 'yt_%'
		  AND TRIM(COALESCE(drive_file_id, '')) <> ''
		ORDER BY id`
	var args []any
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media backfill: query clip folder candidates: %w", err)
	}
	defer rows.Close()

	var out []ClipFolderCandidate
	for rows.Next() {
		var rec ClipFolderCandidate
		if err := rows.Scan(&rec.ID, &rec.DriveFileID, &rec.FolderID, &rec.FolderPath); err != nil {
			return nil, fmt.Errorf("postgres media backfill: scan clip folder candidate: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media backfill: iterate clip folder candidates: %w", err)
	}
	return out, nil
}

// embeddingContractScope is the searchable-media scope plus the published-state
// filter the embedding-contract backfill applies. godlike/06 SSOT: the taxonomy
// half is capregistry.SearchIndexTaxonomySQL, the same constant the projection
// planes use, so the backfill cannot grade a different boundary than the
// indexer it is repairing.
const embeddingContractScope = `(` + capregistry.SearchIndexTaxonomySQL + `)
	AND lifecycle_state IN ('ACTIVE', 'PUBLISHED')`

// CountEmbeddingContractEligible counts rows whose observed model and revision
// already match the canonical contract — the rows the backfill is allowed to
// stamp. An unknown vector is never upgraded by assertion.
func (r *BackfillReader) CountEmbeddingContractEligible(ctx context.Context, modelID, modelRevision string) (int, error) {
	if err := r.ready("count embedding contract"); err != nil {
		return 0, err
	}
	query := `SELECT COUNT(*) FROM media_assets WHERE ` + embeddingContractScope + `
		  AND COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'embedding_model' = $1
		  AND COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'embedding_model_version' = $2`
	var count int
	if err := r.db.QueryRowContext(ctx, query, modelID, modelRevision).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres media backfill: count eligible embeddings: %w", err)
	}
	return count, nil
}

// CountEmbeddingContractStamped counts eligible rows that already carry the
// canonical contract hash.
func (r *BackfillReader) CountEmbeddingContractStamped(ctx context.Context, contractHash string) (int, error) {
	if err := r.ready("count embedding contract"); err != nil {
		return 0, err
	}
	query := `SELECT COUNT(*) FROM media_assets WHERE ` + embeddingContractScope + `
		  AND COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'embedding_contract_hash' = $1`
	var count int
	if err := r.db.QueryRowContext(ctx, query, contractHash).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres media backfill: count stamped embeddings: %w", err)
	}
	return count, nil
}

// ListEmbeddingContractToStamp returns the ids of eligible rows whose observed
// contract hash differs from the canonical one, in id order.
func (r *BackfillReader) ListEmbeddingContractToStamp(ctx context.Context, modelID, modelRevision, contractHash string) ([]string, error) {
	if err := r.ready("list embedding contract"); err != nil {
		return nil, err
	}
	query := `SELECT id FROM media_assets WHERE ` + embeddingContractScope + `
		  AND COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'embedding_model' = $1
		  AND COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'embedding_model_version' = $2
		  AND COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'embedding_contract_hash' <> $3
		ORDER BY id`
	rows, err := r.db.QueryContext(ctx, query, modelID, modelRevision, contractHash)
	if err != nil {
		return nil, fmt.Errorf("postgres media backfill: query embeddings to stamp: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres media backfill: scan embedding to stamp: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media backfill: iterate embeddings to stamp: %w", err)
	}
	return out, nil
}

// SearchTextBackfillCandidate is one asset whose search_text is empty and that
// is therefore a repair candidate: the composing inputs come from the row
// itself plus its current transcript track.
type SearchTextBackfillCandidate struct {
	ID          string
	Source      string
	Name        string
	Category    string
	TagsJSON    string
	SourceURL   string
	Description string
	Summary     string
	Title       string
	Transcript  string
}

// SearchTextBackfillReader answers the search_text repair's candidate scan from
// the PostgreSQL media SSOT.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): this replaces the retired
// `SELECT … FROM media_assets m … asset_text_tracks` statement that
// cmd/admin's repair-stock-metadata ran on the operational SQLite handle. Both
// media_assets and asset_text_tracks exist on PostgreSQL column-for-column
// (migration 001), so the query is a dialect translation rather than a split:
// the operational handle held no committed media rows, so the repair could only
// ever find zero candidates while the SSOT held them.
//
// The writes stay where they were — every repaired row goes through the
// canonical asset mutator (persistence.AssetMutator), never through this
// reader. This file is read-only.
type SearchTextBackfillReader struct {
	db *sql.DB
}

// NewSearchTextBackfillReader binds the reader to the media SSOT handle. A nil
// handle returns nil so callers fail closed rather than reading a second engine
// (godlike/07 no-fake-availability).
func NewSearchTextBackfillReader(db *sql.DB) *SearchTextBackfillReader {
	if db == nil {
		return nil
	}
	return &SearchTextBackfillReader{db: db}
}

// ListSearchTextBackfillCandidates returns every non-repaired asset of the
// given sources, ordered by id, with its current transcript resolved in the
// same query so the composer never has to issue per-asset reads.
//
// A non-positive limit means "no bound". At least one source is required: an
// unbounded IN () is a syntax error, and an empty source set would silently
// widen the repair to the whole catalog.
func (r *SearchTextBackfillReader) ListSearchTextBackfillCandidates(ctx context.Context, sources []string, limit int) ([]SearchTextBackfillCandidate, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres search_text backfill: media reader unavailable")
	}
	cleaned := make([]string, 0, len(sources))
	for _, source := range sources {
		if trimmed := strings.TrimSpace(source); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("postgres search_text backfill: at least one source is required")
	}

	placeholders := make([]string, len(cleaned))
	args := make([]any, 0, len(cleaned)+1)
	for i, source := range cleaned {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, source)
	}

	query := `
		SELECT m.id, COALESCE(m.source, ''), COALESCE(m.name, ''), COALESCE(m.category, ''),
		       COALESCE(m.tags, '[]'), COALESCE(m.source_url, ''),
		       COALESCE(m.metadata_json::jsonb->>'description', ''),
		       COALESCE(m.metadata_json::jsonb->>'summary', ''),
		       COALESCE(m.metadata_json::jsonb->>'title', ''),
		       COALESCE((SELECT t.text_content FROM asset_text_tracks t
		                 WHERE t.asset_id = m.id AND t.text_kind = 'transcript' AND t.is_current = 1
		                 ORDER BY t.id LIMIT 1), '')
		FROM media_assets m
		WHERE (m.search_text IS NULL OR TRIM(m.search_text) = '')
		  AND m.source IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY m.id`
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres search_text backfill: query candidates: %w", err)
	}
	defer rows.Close()

	var out []SearchTextBackfillCandidate
	for rows.Next() {
		var rec SearchTextBackfillCandidate
		if err := rows.Scan(&rec.ID, &rec.Source, &rec.Name, &rec.Category, &rec.TagsJSON,
			&rec.SourceURL, &rec.Description, &rec.Summary, &rec.Title, &rec.Transcript); err != nil {
			return nil, fmt.Errorf("postgres search_text backfill: scan candidate: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres search_text backfill: iterate candidates: %w", err)
	}
	return out, nil
}
