package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
)

// ErrMediaAssetNotFound is the typed not-found sentinel for the media read
// repository. Callers MUST distinguish it from a transport/dialect error.
var ErrMediaAssetNotFound = errors.New("postgres media: asset not found")

// MediaAssetRecord is the canonical full media read model: a media_assets row
// plus the location/provenance projection. It is the ONLY media read surface
// for non-semantic consumers (clip.render, localization, source resolvers,
// YouTube sourcing dedupe, local search). Semantic search keeps using
// GetMany/VectorStorePort on the same adapter, so retrieval and hydration
// cannot split across databases.
type MediaAssetRecord struct {
	ID             string
	Name           string
	Filename       string
	Title          string
	Source         string
	MediaType      string
	Category       string
	LifecycleState string
	IndexState     string
	DurationMS     int64
	LocalPath      string
	DriveFileID    string
	DriveLink      string
	DownloadLink   string
	SHA256         string
	ThumbnailURL   string
	Tags           []string
	SourceURL      string
	SourceProvider string
	SourceVideoID  string
	YouTubeVideoID string
	StartMS        int64
	EndMS          int64
	FolderID       string
	ParentFolderID string
	FolderPath     string
	CreatedAt      string
	MetadataJSON   string
	// AssetKind / SemanticRole are the canonical taxonomy dimensions
	// (media_assets.asset_kind / semantic_role).
	AssetKind    string
	SemanticRole string
	// SearchTerms is the JSON-encoded keyword array (media_assets.search_terms)
	// and ReviewStatus the governance column; both are part of the admin
	// console's editable surface, so the canonical read model must carry them
	// or a read-modify-write would silently drop them.
	SearchTerms  string
	ReviewStatus string
	// Language is the asset language (media_assets.language, migration 099);
	// OriginalLanguage is its source-language sibling (migration 152). Both
	// are read by the local search filter and the Rust local scorer.
	Language         string
	OriginalLanguage string

	// metadata is the lazily-decoded metadata_json object. It backs the typed
	// accessors so callers never re-implement key knowledge.
	metadata map[string]any
}

// TitleOrName returns the display title with the name fallback, matching the
// legacy detail.Service projection so callers can replace it 1:1.
func (r *MediaAssetRecord) TitleOrName() string {
	if r == nil {
		return ""
	}
	if strings.TrimSpace(r.Title) != "" {
		return r.Title
	}
	return r.Name
}

// MetadataString returns a string metadata_json value (empty when absent).
func (r *MediaAssetRecord) MetadataString(key string) string {
	if r == nil {
		return ""
	}
	r.ensureMetadata()
	if value, ok := r.metadata[key].(string); ok {
		return value
	}
	return ""
}

// MetadataStringSlice returns a string-slice metadata_json value. Both the
// native JSON array form and a []any of strings are accepted; malformed input
// degrades to nil (a single legacy row must not fail the read).
func (r *MediaAssetRecord) MetadataStringSlice(key string) []string {
	if r == nil {
		return nil
	}
	r.ensureMetadata()
	switch value := r.metadata[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// MetadataFloat returns a numeric metadata_json value (0 when absent or not
// numeric). Numbers arrive as float64 from encoding/json; other numeric forms
// are tolerated.
func (r *MediaAssetRecord) MetadataFloat(key string) float64 {
	if r == nil {
		return 0
	}
	r.ensureMetadata()
	switch value := r.metadata[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		if f, err := value.Float64(); err == nil {
			return f
		}
	}
	return 0
}

// MetadataMap returns a copy of the decoded metadata_json object. Callers that
// reconstruct a kernel asset merge the typed columns over this map.
func (r *MediaAssetRecord) MetadataMap() map[string]any {
	if r == nil {
		return map[string]any{}
	}
	r.ensureMetadata()
	out := make(map[string]any, len(r.metadata))
	for k, v := range r.metadata {
		out[k] = v
	}
	return out
}

// CreatedAtTime parses the RFC3339 created_at column (zero when absent or
// unparsable).
func (r *MediaAssetRecord) CreatedAtTime() time.Time {
	if r == nil {
		return time.Time{}
	}
	return parseRFC3339(r.CreatedAt)
}

func parseRFC3339(raw string) time.Time {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return t
	}
	return time.Time{}
}

func (r *MediaAssetRecord) ensureMetadata() {
	if r.metadata != nil {
		return
	}
	r.metadata = map[string]any{}
	raw := strings.TrimSpace(r.MetadataJSON)
	if raw == "" || raw == "{}" {
		return
	}
	if err := json.Unmarshal([]byte(raw), &r.metadata); err != nil {
		r.metadata = map[string]any{}
	}
}

// mediaAssetReadColumns is the single column projection for every read path so
// GetAsset / GetAssetsByIDs / FindByHash cannot drift in field coverage.
//
// SHA-256 resolution order mirrors the legacy registry precedence
// (binary_sha256 → content_sha256 → content_sha256) via a single COALESCE.
const mediaAssetReadColumns = `
	id, name, filename, title, source, media_type, category, tags,
	lifecycle_state, index_state, duration_ms,
	local_path, drive_file_id, drive_link, download_link,
	COALESCE(NULLIF(binary_sha256, ''), NULLIF(content_sha256, ''), content_sha256),
	thumbnail_url,
	source_url, source_provider, source_video_id, youtube_video_id,
	start_ms, end_ms, folder_id, parent_folder_id, folder_path, created_at, metadata_json,
	search_terms, review_status,
	-- Taxonomy dimensions (asset_kind = asset FAMILY, semantic_role = usage
	-- intent). They are distinct from source (physical provenance): a stock
	-- clip acquired from YouTube is source='youtube' AND
	-- asset_kind='stock_video', so a caller can no longer be forced to infer the
	-- family from the id prefix.
	asset_kind, semantic_role,
	COALESCE(language, ''), COALESCE(original_language, '')
`

// TimestampExpr returns the effective typed timestamp expression for a TEXT column
// carrying a migration-004 typed mirror (<column>_ts). Readers prefer the
// mirror and fall back to the parsed legacy TEXT sibling for rows written
// before the backfill (their mirror is NULL). The trailing '-infinity'
// sentinel preserves the legacy empty-string placement ("unset is oldest",
// the same shape as enrichStateUpdatedAtExpr) once the value is typed.
//
// This is the CONTRACT-phase read shape of migration 004: once every read
// goes through here, the legacy TEXT columns can be dropped and the
// dual-write retired without touching the readers again — only the NULLIF
// fallback has to shrink at that point.
//
// PARAMETER TYPING PITFALL. A statement that ALSO writes the legacy TEXT
// columns from a string parameter ($N) must compare through
// `NULLIF($N, ”)::timestamptz`, never `$N::timestamptz`: the bare cast
// re-types the parameter as TIMESTAMPTZ at parse time, and every other use
// of $N in the statement then coerces its literals — `NULLIF($N, ”)`
// fails with "invalid input syntax for type timestamp with time zone: \"\"".
// The NULLIF form keeps $N TEXT-typed, matching the dual-write setters.
func TimestampExpr(column string) string {
	return `COALESCE(` + column + `_ts, NULLIF(` + column + `, '')::timestamptz, '-infinity'::timestamptz)`
}

// tsCastExpr is the mirror-less form of TimestampExpr, for timestamp columns that
// never received a migration-004 typed mirror (media_asset_sources.discovered_at,
// asset_processing.updated_at). It still removes the lexicographic TEXT
// comparison — the ordering becomes typed — but there is no mirror to prefer,
// so the parsed legacy value is the only source.
func tsCastExpr(column string) string {
	return `COALESCE(NULLIF(` + column + `, '')::timestamptz, '-infinity'::timestamptz)`
}

type mediaAssetScanner interface {
	Scan(dest ...any) error
}

func scanMediaAssetRecord(row mediaAssetScanner) (*MediaAssetRecord, error) {
	var rec MediaAssetRecord
	var tags string
	if err := row.Scan(
		&rec.ID, &rec.Name, &rec.Filename, &rec.Title, &rec.Source, &rec.MediaType, &rec.Category,
		&tags,
		&rec.LifecycleState, &rec.IndexState, &rec.DurationMS,
		&rec.LocalPath, &rec.DriveFileID, &rec.DriveLink, &rec.DownloadLink,
		&rec.SHA256, &rec.ThumbnailURL,
		&rec.SourceURL, &rec.SourceProvider, &rec.SourceVideoID, &rec.YouTubeVideoID,
		&rec.StartMS, &rec.EndMS, &rec.FolderID, &rec.ParentFolderID, &rec.FolderPath, &rec.CreatedAt,
		&rec.MetadataJSON, &rec.SearchTerms, &rec.ReviewStatus,
		&rec.AssetKind, &rec.SemanticRole,
		&rec.Language, &rec.OriginalLanguage,
	); err != nil {
		return nil, err
	}
	rec.Tags = decodeTagsJSON(tags)
	return &rec, nil
}

// GetAsset returns the full canonical record for one asset id from the
// PostgreSQL media SSOT. A missing row returns ErrMediaAssetNotFound.
func (s *MediaSearcher) GetAsset(ctx context.Context, assetID string) (*MediaAssetRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres media: media read repository not wired")
	}
	id := strings.TrimSpace(assetID)
	if id == "" {
		return nil, fmt.Errorf("postgres media: asset id is required")
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+mediaAssetReadColumns+` FROM media_assets WHERE id = $1`, id)
	rec, err := scanMediaAssetRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrMediaAssetNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres media: get asset %q: %w", id, err)
	}
	return rec, nil
}

// GetAssetsByIDs returns the canonical records for the supplied ids, deduped
// and in input order. Unknown ids are skipped (hydration is best-effort; a
// single missing row must not fail a batch read).
func (s *MediaSearcher) GetAssetsByIDs(ctx context.Context, assetIDs []string) ([]MediaAssetRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres media: media read repository not wired")
	}
	ids := dedupeHydrationIDs(assetIDs)
	out := make([]MediaAssetRecord, 0, len(ids))
	for _, id := range ids {
		rec, err := s.GetAsset(ctx, id)
		if err != nil {
			if errors.Is(err, ErrMediaAssetNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, nil
}

// FindByHash resolves an asset by content fingerprint. It accepts a raw hex
// digest or a `sha256:`-prefixed digest and matches the canonical binary,
// content and legacy hash columns.
func (s *MediaSearcher) FindByHash(ctx context.Context, hash string) (*MediaAssetRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres media: media read repository not wired")
	}
	candidates := mediaHashCandidates(hash)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("postgres media: hash is required")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+mediaAssetReadColumns+`
		FROM media_assets
		WHERE binary_sha256 = $1 OR content_sha256 = $1 OR content_sha256 = $1
		   OR binary_sha256 = $2 OR content_sha256 = $2 OR content_sha256 = $2
		LIMIT 1
	`, candidates[0], candidates[len(candidates)-1])
	rec, err := scanMediaAssetRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: hash %s", ErrMediaAssetNotFound, hash)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres media: find by hash: %w", err)
	}
	return rec, nil
}

// FindByYouTubeID resolves an asset by its YouTube video identity.
func (s *MediaSearcher) FindByYouTubeID(ctx context.Context, videoID string) (*MediaAssetRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres media: media read repository not wired")
	}
	id := strings.TrimSpace(videoID)
	if id == "" {
		return nil, fmt.Errorf("postgres media: youtube video id is required")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+mediaAssetReadColumns+`
		FROM media_assets
		WHERE youtube_video_id = $1 OR source_video_id = $1
		LIMIT 1
	`, id)
	rec, err := scanMediaAssetRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: youtube %s", ErrMediaAssetNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres media: find by youtube id: %w", err)
	}
	return rec, nil
}

// mediaHashCandidates returns the raw hash and, when present, its unprefixed
// form so writers that store either `sha256:<hex>` or `<hex>` both resolve.
func mediaHashCandidates(hash string) []string {
	trimmed := strings.TrimSpace(hash)
	if trimmed == "" {
		return nil
	}
	raw := strings.TrimPrefix(trimmed, "sha256:")
	if raw == trimmed {
		return []string{trimmed, trimmed}
	}
	return []string{trimmed, raw}
}

// ── absorbed from media_read_repository.go ──

// Compile-time cutover pin: the PostgreSQL MediaSearcher is the ONE media
// read adapter. It owns both pgvector retrieval and media_assets hydration.
var _ appsearch.MediaReadRepository = (*MediaSearcher)(nil)

// GetMany implements the canonical MediaReadRepository on the same concrete
// adapter that implements VectorStorePort. This is intentionally not a second
// repository: BuildSearchBackends requires one object to satisfy both ports,
// making pgvector -> PostgreSQL media_assets the only semantic media read path.
func (s *MediaSearcher) GetMany(
	ctx context.Context,
	actor appsearch.Actor,
	assetIDs []string,
) ([]appsearch.MediaAsset, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres media searcher: hydration not wired")
	}

	ids := dedupeHydrationIDs(assetIDs)
	if len(ids) == 0 {
		return []appsearch.MediaAsset{}, nil
	}

	out := make([]appsearch.MediaAsset, 0, len(ids))
	for _, id := range ids {
		row, err := s.fetchAssetForWorkspace(ctx, id, strings.TrimSpace(actor.WorkspaceID))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, fmt.Errorf("postgres media searcher: hydrate asset %q: %w", id, err)
		}
		if !isSearchableMediaLifecycle(row.LifecycleState) {
			continue
		}
		out = append(out, appsearch.MediaAsset{
			ID:             row.ID,
			Name:           row.Name,
			Title:          row.Title,
			Source:         row.Source,
			MediaType:      row.MediaType,
			Category:       row.Category,
			Tags:           decodeTagsJSON(row.Tags),
			Language:       row.Language,
			DurationMs:     int(row.DurationMs),
			SearchText:     row.SearchText,
			LifecycleState: row.LifecycleState,
			AssetKind:      row.AssetKind,
			SemanticRole:   row.SemanticRole,
		})
	}
	return out, nil
}

// fetchAssetForWorkspace repeats the workspace predicate when the verified
// search actor carries one. Empty workspace is reserved for admin/system
// searches: the vector leg has already enforced IsSystem there, while normal
// user searches always carry the authenticated workspace.
func (s *MediaSearcher) fetchAssetForWorkspace(ctx context.Context, assetID, workspaceID string) (*assetRow, error) {
	query := `
		SELECT id, name, title, source, media_type, category, language,
		       tags, search_text, duration_ms, lifecycle_state,
		       youtube_video_id, youtube_url, start_time, end_time, style,
		       asset_kind, semantic_role
		FROM media_assets
		WHERE id = $1
	`
	args := []any{assetID}
	if workspaceID != "" {
		query += " AND workspace_id = $2"
		args = append(args, workspaceID)
	}

	row := s.db.QueryRowContext(ctx, query, args...)
	var a assetRow
	if err := row.Scan(&a.ID, &a.Name, &a.Title, &a.Source, &a.MediaType, &a.Category, &a.Language,
		&a.Tags, &a.SearchText, &a.DurationMs, &a.LifecycleState,
		&a.YouTubeVideoID, &a.YouTubeURL, &a.StartTime, &a.EndTime, &a.Style,
		&a.AssetKind, &a.SemanticRole); err != nil {
		return nil, err
	}
	return &a, nil
}

func dedupeHydrationIDs(assetIDs []string) []string {
	seen := make(map[string]struct{}, len(assetIDs))
	out := make([]string, 0, len(assetIDs))
	for _, raw := range assetIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func isSearchableMediaLifecycle(state string) bool {
	for _, allowed := range appsearch.SearchableLifecycleStates {
		if state == allowed {
			return true
		}
	}
	return false
}

// ── absorbed from renditions.go ──

// CommitRenditionTx persists a technical rendition and its physical
// location inside the caller-owned transaction.
func (c *PostgresAssetCommitter) CommitRenditionTx(
	ctx context.Context,
	tx *sql.Tx,
	assetID string,
	r persistence.RenditionCommit,
	nowStr string,
) error {
	if c == nil || tx == nil {
		return fmt.Errorf("asset committer: rendition transaction is required")
	}
	if assetID == "" || r.Kind == "" || r.URI == "" {
		return fmt.Errorf("asset committer: rendition asset id, kind and URI are required")
	}

	locationKind := r.Provider
	if locationKind != "drive" && locationKind != "object_storage" {
		locationKind = "local"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, content_sha256, is_primary, created_at, updated_at,
			 created_at_ts, updated_at_ts)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 0, $10, $11,
		        NULLIF($10, '')::timestamptz, NULLIF($11, '')::timestamptz)
		ON CONFLICT (asset_id, location_kind) DO UPDATE SET
			uri = excluded.uri,
			external_id = excluded.external_id,
			web_view_link = excluded.web_view_link,
			download_url = excluded.download_url,
			mime_type = excluded.mime_type,
			file_size_bytes = excluded.file_size_bytes,
			content_sha256 = excluded.content_sha256,
			-- A rendition NEVER redefines which location is primary. The INSERT
			-- hardcodes is_primary = 0, so copying excluded.is_primary here
			-- DEMOTED the asset's existing primary locator whenever a rendition
			-- shared its location_kind: registering the master rendition of a
			-- Drive-backed clip cleared is_primary on the very row a reader
			-- trusts to resolve the Drive object. Preserve the flag on
			-- conflict; the primary locator is decided by the asset commit, not
			-- by a rendition write.
			is_primary = asset_locations.is_primary,
			updated_at = excluded.updated_at,
			updated_at_ts = excluded.updated_at_ts
	`, assetID, locationKind, r.URI, r.FileID, r.WebViewLink, r.DownloadURL,
		r.MimeType, r.SizeBytes, r.SHA256, nowStr, nowStr); err != nil {
		return fmt.Errorf("asset committer: upsert rendition location %s/%s: %w", assetID, r.Kind, err)
	}

	var locationID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM asset_locations WHERE asset_id = $1 AND location_kind = $2`,
		assetID, locationKind).Scan(&locationID); err != nil {
		return fmt.Errorf("asset committer: resolve rendition location %s/%s: %w", assetID, r.Kind, err)
	}

	// gen_random_uuid() is the PostgreSQL analogue of the SQLite
	// lower(hex(randomblob(16))) rendition identity; both are opaque
	// server-side identifiers with no cross-engine meaning.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO asset_renditions
			(id, asset_id, location_id, kind, container, codec, width, height,
			 fps, bitrate, sha256, size_bytes, created_at, updated_at)
		VALUES (gen_random_uuid()::text, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (asset_id, kind) DO UPDATE SET
			location_id = excluded.location_id,
			container = excluded.container,
			codec = excluded.codec,
			width = excluded.width,
			height = excluded.height,
			fps = excluded.fps,
			bitrate = excluded.bitrate,
			sha256 = excluded.sha256,
			size_bytes = excluded.size_bytes,
			updated_at = excluded.updated_at
	`, assetID, locationID, r.Kind, r.Container, r.Codec, r.Width, r.Height,
		r.FPS, r.Bitrate, r.SHA256, r.SizeBytes, nowStr, nowStr); err != nil {
		return fmt.Errorf("asset committer: upsert rendition %s/%s: %w", assetID, r.Kind, err)
	}
	return nil
}

// CommitRenditionTx on the aggregate committer delegates to the embedded
// canonical asset committer while preserving the caller-owned transaction.
func (c *PostgresMediaCommitter) CommitRenditionTx(
	ctx context.Context,
	tx *sql.Tx,
	assetID string,
	r persistence.RenditionCommit,
	nowStr string,
) error {
	if c == nil || c.assets == nil {
		return fmt.Errorf("media committer: canonical asset committer is unavailable")
	}
	return c.assets.CommitRenditionTx(ctx, tx, assetID, r, nowStr)
}

// ── absorbed from media_sourcing_lookup.go ──

// FindClipIDByName returns the id of the most recent non-deleted asset with the
// exact name, or "" when none exists. PostgreSQL counterpart of the retired
// SQLite ClipsRepository.FindByName dedupe lookup.
func (s *MediaSearcher) FindClipIDByName(ctx context.Context, name string) (string, error) {
	needle := strings.TrimSpace(name)
	if needle == "" {
		return "", nil
	}
	return s.queryClipID(ctx, `
		SELECT id FROM media_assets
		WHERE name = $1 AND lifecycle_state <> 'DELETED'
		ORDER BY `+TimestampExpr("created_at")+` DESC LIMIT 1
	`, needle)
}

// FindClipIDByYouTubeVideoID returns the id of the most recent non-deleted
// asset registered from the given YouTube video id. When hasSegment is true the
// match is further restricted to the exact [startSec, endSec) segment via the
// canonical start_ms/end_ms columns (integer-millisecond comparison, matching
// the legacy json_extract + ROUND semantics).
func (s *MediaSearcher) FindClipIDByYouTubeVideoID(ctx context.Context, videoID string, hasSegment bool, startSec, endSec float64) (string, error) {
	vid := strings.TrimSpace(videoID)
	if vid == "" {
		return "", nil
	}
	if hasSegment {
		return s.queryClipID(ctx, `
			SELECT id FROM media_assets
			WHERE (youtube_video_id = $1 OR source_video_id = $1)
			  AND lifecycle_state <> 'DELETED'
			  AND start_ms = $2 AND end_ms = $3
			ORDER BY `+TimestampExpr("created_at")+` DESC LIMIT 1
		`, vid, int64(startSec*1000), int64(endSec*1000))
	}
	return s.queryClipID(ctx, `
		SELECT id FROM media_assets
		WHERE (youtube_video_id = $1 OR source_video_id = $1)
		  AND lifecycle_state <> 'DELETED'
		ORDER BY `+TimestampExpr("created_at")+` DESC LIMIT 1
	`, vid)
}

// FindClipIDBySourceURL returns the id of the most recent non-deleted asset
// registered with the given external URL (source_url or youtube_url).
func (s *MediaSearcher) FindClipIDBySourceURL(ctx context.Context, url string) (string, error) {
	needle := strings.TrimSpace(url)
	if needle == "" {
		return "", nil
	}
	return s.queryClipID(ctx, `
		SELECT id FROM media_assets
		WHERE (source_url = $1 OR youtube_url = $1)
		  AND lifecycle_state <> 'DELETED'
		ORDER BY `+TimestampExpr("created_at")+` DESC LIMIT 1
	`, needle)
}

func (s *MediaSearcher) queryClipID(ctx context.Context, query string, args ...any) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("postgres media: media read repository not wired")
	}
	var id string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("postgres media: dedupe lookup: %w", err)
	}
	return id, nil
}

// ── absorbed from name_search.go ──

// ClipNameSearchResult is the narrow PostgreSQL projection used by the
// lightweight script clip-discovery endpoint. MediaSearcher remains the
// single media read authority; callers never query a SQLite media mirror.
type ClipNameSearchResult struct {
	AssetID     string
	Name        string
	Source      string
	DriveFileID string
}

// SearchClipsByName performs a bounded lexical lookup against the canonical
// media_assets table. Only searchable video rows participate, matching the
// semantic media read lifecycle contract.
func (s *MediaSearcher) SearchClipsByName(ctx context.Context, query string, limit int) ([]ClipNameSearchResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres media searcher: name search not wired")
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(name, ''), COALESCE(source, ''), COALESCE(drive_file_id, '')
		FROM media_assets
		WHERE LOWER(name) LIKE $1
		  AND media_type IN ('video', 'clip')
		  AND lifecycle_state IN ('ACTIVE', 'INDEXED', 'READY', 'PUBLISHED')
		ORDER BY name
		LIMIT $2
	`, "%"+needle+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("postgres media searcher: name search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]ClipNameSearchResult, 0, limit)
	for rows.Next() {
		var hit ClipNameSearchResult
		if err := rows.Scan(&hit.AssetID, &hit.Name, &hit.Source, &hit.DriveFileID); err != nil {
			return nil, fmt.Errorf("postgres media searcher: scan name result: %w", err)
		}
		out = append(out, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media searcher: iterate name results: %w", err)
	}
	return out, nil
}
