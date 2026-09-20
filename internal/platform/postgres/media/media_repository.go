package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
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
// (binary_sha256 → content_sha256 → legacy_file_md5) via a single COALESCE.
const mediaAssetReadColumns = `
	id, name, filename, title, source, media_type, category, tags,
	lifecycle_state, index_state, duration_ms,
	local_path, drive_file_id, drive_link, download_link,
	COALESCE(NULLIF(binary_sha256, ''), NULLIF(content_sha256, ''), legacy_file_md5),
	COALESCE(NULLIF(thumbnail_url, ''), thumb_url),
	source_url, source_provider, source_video_id, youtube_video_id,
	start_ms, end_ms, folder_id, parent_folder_id, folder_path, created_at, metadata_json,
	search_terms, review_status,
	-- Taxonomy dimensions (asset_kind = asset FAMILY, semantic_role = usage
	-- intent). They are distinct from source (physical provenance): a stock
	-- clip acquired from YouTube is source='youtube' AND
	-- asset_kind='stock_video', so a caller can no longer be forced to infer the
	-- family from the id prefix.
	asset_kind, semantic_role
`

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
		WHERE binary_sha256 = $1 OR content_sha256 = $1 OR legacy_file_md5 = $1
		   OR binary_sha256 = $2 OR content_sha256 = $2 OR legacy_file_md5 = $2
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
