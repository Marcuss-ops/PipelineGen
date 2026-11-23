package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// assetFilterPredicates builds the shared WHERE clause + positional arguments
// for every media asset listing/count, so the summary projection, the
// full-asset projection and the count cannot drift apart.
//
// The predicate set mirrors the legacy SQLite repository exactly (source,
// media_type, lifecycle states, id include/exclude, category, group). `IsFolder`
// is a legacy SQLite media-plane column with no PostgreSQL counterpart and is
// therefore not applied; `HasEmbedding`/`WorkspaceID`/`IsAdmin` are ignored by
// the legacy repository too, so ignoring them preserves behaviour.
func assetFilterPredicates(filter asset.Filter) (string, []any) {
	conds := []string{"lifecycle_state <> 'DELETED'"}
	args := []any{}

	addEq := func(column, value string) {
		if value == "" {
			return
		}
		args = append(args, value)
		conds = append(conds, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	addEq("source", filter.Source)
	addEq("media_type", filter.MediaType)
	addEq("category", filter.Category)
	addEq("group_name", filter.Group)

	addSet := func(column string, values []string, negated bool) {
		if len(values) == 0 {
			return
		}
		placeholders := make([]string, 0, len(values))
		for _, value := range values {
			args = append(args, value)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		operator := "IN"
		if negated {
			operator = "NOT IN"
		}
		conds = append(conds, column+" "+operator+" ("+strings.Join(placeholders, ", ")+")")
	}
	addSet("lifecycle_state", filter.States, false)
	addSet("id", filter.IDs, false)
	addSet("id", filter.ExcludeIDs, true)

	return strings.Join(conds, " AND "), args
}

// PostgresAssetStore is the PostgreSQL media-SSOT implementation of the
// consumer-facing asset read contract used by the admin console and the
// operator API: `Get(ctx, id) (*asset.Details, error)`,
// `List(ctx, filter) ([]*asset.Summary, error)` and
// `Count(ctx, filter) (int64, error)`.
//
// It is the PostgreSQL counterpart of the SQLite `*detail.Service` (over
// imagesregistry.AssetStoreSQLite): ONE read contract, two engine
// implementations, selected by composition. Mirroring the legacy filter
// semantics is deliberate — a caller must not be able to tell which engine
// backs it except by the data it sees.
//
// It is READ-ONLY by construction: media mutations stay owned by
// *PostgresMediaCommitter, so this type cannot become a second media writer.
type PostgresAssetStore struct {
	reader *MediaSearcher
}

// NewPostgresAssetStore wires the read store over the media read repository.
// A nil reader returns nil so composition can treat "no media SSOT" as the
// documented degrade signal instead of a half-wired store.
func NewPostgresAssetStore(reader *MediaSearcher) *PostgresAssetStore {
	if reader == nil {
		return nil
	}
	return &PostgresAssetStore{reader: reader}
}

// Get returns the canonical asset plus its location projection for one id,
// delegating to the single hydration site. A missing row returns (nil, nil),
// the same contract as detail.Service.Get.
func (s *PostgresAssetStore) Get(ctx context.Context, id string) (*asset.Details, error) {
	if s == nil || s.reader == nil {
		return nil, errors.New("postgres media: asset store not wired")
	}
	return s.reader.GetAssetDetails(ctx, id)
}

// List returns the canonical asset summaries matching the filter. The
// projection, predicates and ordering mirror the legacy SQLite List exactly.
func (s *PostgresAssetStore) List(ctx context.Context, filter asset.Filter) ([]*asset.Summary, error) {
	if s == nil || s.reader == nil || s.reader.db == nil {
		return nil, errors.New("postgres media: asset store not wired")
	}

	where, args := assetFilterPredicates(filter)
	query := "SELECT id, COALESCE(source, ''), COALESCE(name, ''), COALESCE(filename, ''), " +
		"COALESCE(media_type, ''), COALESCE(category, ''), COALESCE(lifecycle_state, 'ACTIVE'), " +
		"COALESCE(created_at, ''), COALESCE(updated_at, '') " +
		"FROM media_assets WHERE " + where +
		" ORDER BY " + TimestampExpr("created_at") + " DESC"
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
		if filter.Offset > 0 {
			args = append(args, filter.Offset)
			query += fmt.Sprintf(" OFFSET $%d", len(args))
		}
	}

	rows, err := s.reader.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media: list assets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*asset.Summary
	for rows.Next() {
		var summary asset.Summary
		var source, name, filename, mediaType, category, lifecycleState, createdAt, updatedAt sql.NullString
		if err := rows.Scan(
			&summary.ID, &source, &name, &filename,
			&mediaType, &category, &lifecycleState,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres media: list assets scan: %w", err)
		}
		summary.Source = asset.Source(source.String)
		summary.Name = name.String
		summary.Filename = filename.String
		summary.MediaType = asset.MediaType(mediaType.String)
		summary.Category = category.String
		summary.LifecycleState = asset.LifecycleState(lifecycleState.String)
		if createdAt.Valid {
			summary.CreatedAt = parseRFC3339(createdAt.String)
		}
		if updatedAt.Valid {
			summary.UpdatedAt = parseRFC3339(updatedAt.String)
		}
		out = append(out, &summary)
	}
	return out, rows.Err()
}

// Count returns the number of canonical assets matching the filter, using the
// same predicates and soft-delete exclusion as List (pagination is ignored,
// exactly like the legacy asset repository Count).
func (s *PostgresAssetStore) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	if s == nil || s.reader == nil || s.reader.db == nil {
		return 0, errors.New("postgres media: asset store not wired")
	}
	where, args := assetFilterPredicates(filter)
	var n int64
	if err := s.reader.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE "+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres media: count assets: %w", err)
	}
	return n, nil
}

// MediaClipAssetReader adapts the PostgreSQL media read repository to the clips
// capability's consumer-owned read contract: asset by id, filtered asset list,
// count.
//
// It exists as a distinct type (rather than reusing PostgresAssetStore) because
// the admin/operator surface reads `*asset.Details` while the clips use cases
// read `*asset.Asset`; one type cannot honestly serve both signatures. Both sit
// on the SAME *MediaSearcher over the SAME media SSOT — no second media read
// registry is created.
type MediaClipAssetReader struct {
	reader *MediaSearcher
}

// NewMediaClipAssetReader wires the clips read adapter over the media read
// repository. A nil reader returns nil (degrade signal).
func NewMediaClipAssetReader(reader *MediaSearcher) *MediaClipAssetReader {
	if reader == nil {
		return nil
	}
	return &MediaClipAssetReader{reader: reader}
}

// Get returns the canonical kernel asset for one id. A missing row returns
// (nil, nil), the same contract as the legacy detail.Repository.Get.
func (r *MediaClipAssetReader) Get(ctx context.Context, id string) (*asset.Asset, error) {
	if r == nil || r.reader == nil {
		return nil, errors.New("postgres media: clip asset reader not wired")
	}
	return r.reader.GetClip(ctx, id)
}

// List returns the canonical kernel assets matching the filter, mirroring the
// legacy detail.Repository.List projection and ordering.
func (r *MediaClipAssetReader) List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error) {
	if r == nil || r.reader == nil || r.reader.db == nil {
		return nil, errors.New("postgres media: clip asset reader not wired")
	}

	where, args := assetFilterPredicates(filter)
	query := "SELECT " + mediaAssetReadColumns + " FROM media_assets WHERE " + where + " ORDER BY " + TimestampExpr("created_at") + " DESC"
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
		if filter.Offset > 0 {
			args = append(args, filter.Offset)
			query += fmt.Sprintf(" OFFSET $%d", len(args))
		}
	}

	rows, err := r.reader.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media: list clip assets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]*asset.Asset, 0)
	for rows.Next() {
		rec, err := scanMediaAssetRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres media: list clip assets scan: %w", err)
		}
		out = append(out, rec.HydrateAsset())
	}
	return out, rows.Err()
}

// Count returns the number of canonical assets matching the filter.
func (r *MediaClipAssetReader) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	if r == nil || r.reader == nil || r.reader.db == nil {
		return 0, errors.New("postgres media: clip asset reader not wired")
	}
	where, args := assetFilterPredicates(filter)
	var n int64
	if err := r.reader.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE "+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres media: count clip assets: %w", err)
	}
	return n, nil
}

// Compile-time self-check: the adapter must always satisfy the very shape the
// clips capability declares, so a signature drift fails the build here rather
// than at the composition root.
var _ interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
	List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error)
	Count(ctx context.Context, filter asset.Filter) (int64, error)
} = (*MediaClipAssetReader)(nil)

// ── absorbed from asset_hydrator.go ──

// GetClip hydrates the canonical kernel asset for a media_assets id from the
// PostgreSQL media SSOT.
//
// It is the PostgreSQL counterpart of the legacy SQLite
// ClipsRepository.GetClip read surface, so admin/operator readers can run on
// the media SSOT without a second media registry. A missing row returns
// (nil, nil) — the SAME contract as the legacy repository — so callers keep
// their existing nil checks unchanged.
func (s *MediaSearcher) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres media: media read repository not wired")
	}
	rec, err := s.GetAsset(ctx, id)
	if err != nil {
		if errors.Is(err, ErrMediaAssetNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return rec.HydrateAsset(), nil
}

// AssetDetailsReader adapts the media read repository to the narrow
// `Get(ctx, id) (*asset.Details, error)` lookup surface consumed by the media
// preflight and the Drive location verifier. It is the PostgreSQL counterpart
// of *detail.Service for those ports, so both keep ONE implementation.
type AssetDetailsReader struct {
	reader *MediaSearcher
}

// NewAssetDetailsReader wires the adapter. A nil reader returns nil.
func NewAssetDetailsReader(reader *MediaSearcher) *AssetDetailsReader {
	if reader == nil {
		return nil
	}
	return &AssetDetailsReader{reader: reader}
}

// Get implements the narrow details lookup against the media SSOT.
func (r *AssetDetailsReader) Get(ctx context.Context, id string) (*asset.Details, error) {
	if r == nil || r.reader == nil {
		return nil, errors.New("postgres media: asset details reader not wired")
	}
	return r.reader.GetAssetDetails(ctx, id)
}

// FindByContentHash resolves an asset by canonical content fingerprint on the
// media SSOT. A missing row returns (nil, nil), mirroring Get's not-found
// contract for the ingest lifecycle's FindExisting surface.
func (r *AssetDetailsReader) FindByContentHash(ctx context.Context, sha256 string) (*asset.Details, error) {
	if r == nil || r.reader == nil {
		return nil, errors.New("postgres media: asset details reader not wired")
	}
	rec, err := r.reader.FindByHash(ctx, sha256)
	if err != nil {
		if errors.Is(err, ErrMediaAssetNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return r.reader.GetAssetDetails(ctx, rec.ID)
}

// GetAssetDetails returns the canonical asset plus its location projection,
// mirroring detail.Service.Get against the PostgreSQL media SSOT. A missing
// row returns (nil, nil) so the LocationVerifier orphan check keeps its exact
// contract (any error there means "not found", a nil means "lookup ok").
func (s *MediaSearcher) GetAssetDetails(ctx context.Context, id string) (*asset.Details, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres media: media read repository not wired")
	}
	rec, err := s.GetAsset(ctx, id)
	if err != nil {
		if errors.Is(err, ErrMediaAssetNotFound) {
			return nil, nil
		}
		return nil, err
	}
	details := &asset.Details{Asset: rec.HydrateAsset()}
	if details.Asset == nil {
		return nil, nil
	}
	if rec.DriveFileID != "" || rec.DriveLink != "" || rec.DownloadLink != "" {
		details.Locations = append(details.Locations, &asset.Location{
			AssetID:      rec.ID,
			LocationKind: asset.LocationKindDrive,
			URI:          rec.DriveLink,
			ExternalID:   rec.DriveFileID,
			AccessURL:    rec.DriveLink,
			DownloadURL:  rec.DownloadLink,
			IsPrimary:    true,
		})
	}
	return details, nil
}

// HydrateAsset projects the canonical read model onto the kernel asset type.
// It is the SINGLE translation site so every PostgreSQL-backed reader surfaces
// the same kernel shape and no caller re-implements field mapping.
func (r *MediaAssetRecord) HydrateAsset() *asset.Asset {
	if r == nil {
		return nil
	}
	out := &asset.Asset{
		ID:             r.ID,
		Source:         asset.Source(r.Source),
		Name:           r.Name,
		Filename:       r.Filename,
		MediaType:      asset.MediaType(r.MediaType),
		Category:       r.Category,
		SourceURL:      r.SourceURL,
		ThumbnailURL:   r.ThumbnailURL,
		Duration:       time.Duration(r.DurationMS) * time.Millisecond,
		Tags:           append([]string(nil), r.Tags...),
		LifecycleState: asset.LifecycleState(r.LifecycleState),
		CreatedAt:      r.CreatedAtTime(),
		Metadata:       r.MetadataMap(),
	}
	// The column projection wins over the metadata mirror: the columns are the
	// canonical post-cutover storage; the mirrors exist only for rows written
	// before the column projection existed.
	if r.DriveFileID != "" {
		out.SetDriveFileID(r.DriveFileID)
	}
	if r.DriveLink != "" {
		out.SetDriveLink(r.DriveLink)
	}
	if r.DownloadLink != "" {
		out.SetDownloadLink(r.DownloadLink)
	}
	if r.LocalPath != "" {
		out.SetLocalPath(r.LocalPath)
	}
	if r.SHA256 != "" {
		out.SetContentHash(r.SHA256)
		out.SetContentHash(r.SHA256)
	}
	if r.IndexState != "" {
		out.SetMetadataString("index_state", r.IndexState)
	}
	if r.SearchTerms != "" {
		out.SearchTerms = decodeTagsJSON(r.SearchTerms)
	}
	if r.ReviewStatus != "" {
		out.ReviewStatus = asset.ReviewStatus(r.ReviewStatus)
	}
	return out
}

// ── absorbed from media_clip_resolver.go ──

// ResolveByMediaAssetID returns the canonical asset for an id, or (nil, nil)
// when the row is absent or soft-deleted. It mirrors the legacy SQLite
// ClipsRepository.ResolveByMediaAssetID contract so the script clip resolver
// can run against the PostgreSQL media SSOT without behavioural drift.
func (s *MediaSearcher) ResolveByMediaAssetID(ctx context.Context, id string) (*asset.Asset, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres media: media read repository not wired")
	}
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+mediaAssetReadColumns+` FROM media_assets WHERE id = $1 AND COALESCE(lifecycle_state,'') <> 'DELETED' LIMIT 1`,
		trimmed)
	rec, err := scanMediaAssetRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres media: ResolveByMediaAssetID(%q): %w", trimmed, err)
	}
	return rec.HydrateAsset(), nil
}

// ResolveByYouTubeVideoID expands a YouTube video id into every live
// media_assets row whose id starts with `yt_<videoID>_`. It mirrors the legacy
// SQLite ClipsRepository.ResolveByYouTubeVideoID contract: each YouTube ingest
// segment is persisted with id = `yt_<videoID>_<start>_<n>`, so one video id
// fans out to N rows and the caller picks the subset to bind.
//
// LIKE metacharacters in the input are intentionally NOT escaped, exactly as in
// the retired SQLite form: a YouTube video id has a fixed 11-character
// base64url alphabet that contains no % or _, so a wildcard-bearing id is
// already malformed upstream.
func (s *MediaSearcher) ResolveByYouTubeVideoID(ctx context.Context, videoID string) ([]*asset.Asset, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres media: media read repository not wired")
	}
	trimmed := strings.TrimSpace(videoID)
	if trimmed == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+mediaAssetReadColumns+` FROM media_assets WHERE id LIKE $1 AND COALESCE(lifecycle_state,'') <> 'DELETED' ORDER BY id ASC`,
		"yt_"+trimmed+"_%")
	if err != nil {
		return nil, fmt.Errorf("postgres media: ResolveByYouTubeVideoID(%q): %w", trimmed, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]*asset.Asset, 0)
	for rows.Next() {
		rec, err := scanMediaAssetRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres media: ResolveByYouTubeVideoID scan: %w", err)
		}
		out = append(out, rec.HydrateAsset())
	}
	return out, rows.Err()
}

// ResolveByExternalProviderID matches by provider + external_id, mirroring the
// retired SQLite ClipsRepository.ResolveByExternalProviderID contract: the
// google_drive provider resolves through the canonical drive_file_id column,
// every other provider through source + the metadata external_id projection.
//
// The metadata projection uses the same NULLIF(metadata_json,”)::jsonb cast as
// the other PostgreSQL media readers, guarded only against the empty-string
// NOT NULL default (which is not valid JSON).
func (s *MediaSearcher) ResolveByExternalProviderID(ctx context.Context, provider, externalID string) ([]*asset.Asset, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres media: media read repository not wired")
	}
	provider = strings.TrimSpace(provider)
	externalID = strings.TrimSpace(externalID)
	if provider == "" || externalID == "" {
		return nil, nil
	}
	query := `SELECT ` + mediaAssetReadColumns + ` FROM media_assets WHERE drive_file_id = $1 AND COALESCE(lifecycle_state,'') <> 'DELETED' ORDER BY ` + TimestampExpr("created_at") + ` ASC`
	args := []any{externalID}
	if provider != "google_drive" {
		query = `SELECT ` + mediaAssetReadColumns + ` FROM media_assets WHERE source = $1 AND (NULLIF(metadata_json, '')::jsonb)->>'external_id' = $2 AND COALESCE(lifecycle_state,'') <> 'DELETED' ORDER BY ` + TimestampExpr("created_at") + ` ASC`
		args = []any{provider, externalID}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media: ResolveByExternalProviderID(%q, %q): %w", provider, externalID, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]*asset.Asset, 0)
	for rows.Next() {
		rec, err := scanMediaAssetRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres media: ResolveByExternalProviderID scan: %w", err)
		}
		out = append(out, rec.HydrateAsset())
	}
	return out, rows.Err()
}

// ResolveByDriveFileID expands a Drive file id into every live
// media_assets row that references it, ordered by creation time. It mirrors
// the legacy SQLite ClipsRepository.ResolveByDriveFileID contract.
func (s *MediaSearcher) ResolveByDriveFileID(ctx context.Context, fileID string) ([]*asset.Asset, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres media: media read repository not wired")
	}
	trimmed := strings.TrimSpace(fileID)
	if trimmed == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+mediaAssetReadColumns+` FROM media_assets WHERE drive_file_id = $1 AND COALESCE(lifecycle_state,'') <> 'DELETED' ORDER BY `+TimestampExpr("created_at")+` ASC`,
		trimmed)
	if err != nil {
		return nil, fmt.Errorf("postgres media: ResolveByDriveFileID(%q): %w", trimmed, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]*asset.Asset, 0)
	for rows.Next() {
		rec, err := scanMediaAssetRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres media: ResolveByDriveFileID scan: %w", err)
		}
		out = append(out, rec.HydrateAsset())
	}
	return out, rows.Err()
}

// ── absorbed from media_asset_source_reader.go ──

// MediaAssetSourceReader returns the canonical `source` of one media asset.
type MediaAssetSourceReader struct {
	db *sql.DB
}

// NewMediaAssetSourceReader returns a reader bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path.
func NewMediaAssetSourceReader(db *sql.DB) *MediaAssetSourceReader {
	if db == nil {
		panic("media.NewMediaAssetSourceReader: db is required")
	}
	return &MediaAssetSourceReader{db: db}
}

// DB exposes the underlying handle so callers can assert the reader lives on
// the media SSOT.
func (r *MediaAssetSourceReader) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// AssetSource returns the canonical source for one asset.
//
// COALESCE matches the retired statement: a NULL source resolves to the empty
// string, which detail.RequiresSubtitles treats as "subtitles not required", so
// an unclassified source keeps the gate's historical meaning instead of turning
// it into an error.
//
// A missing row IS an error (sql.ErrNoRows), also matching the retired
// statement: an unknown candidate must fail the gate loudly rather than be
// silently classified as requiring no subtitles.
func (r *MediaAssetSourceReader) AssetSource(ctx context.Context, assetID string) (string, error) {
	if r == nil || r.db == nil {
		return "", fmt.Errorf("media asset source reader: media SSOT handle is not configured")
	}
	var source string
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(source, '') FROM media_assets WHERE id = $1`,
		assetID,
	).Scan(&source)
	if err != nil {
		return "", fmt.Errorf("media asset source reader: load source for %q: %w", assetID, err)
	}
	return source, nil
}

// ── absorbed from checkpoint_artifact_verifier.go ──

// MediaArtifactVerifier implements capcheckpoint.ArtifactVerifier against the
// PostgreSQL media SSOT (media_assets.content_sha256).
type MediaArtifactVerifier struct {
	resolver *PostgresCanonicalIdentityResolver
}

// NewMediaArtifactVerifier constructs the adapter. Fail-fast: a nil database
// is a programmer error, not a silent no-op (godlike/07) — the caller that
// has no media SSOT handle must not construct one at all.
func NewMediaArtifactVerifier(db *sql.DB) (*MediaArtifactVerifier, error) {
	if db == nil {
		return nil, errors.New("media artifact verifier (postgres): nil database")
	}
	resolver, err := NewPostgresCanonicalIdentityResolver(db)
	if err != nil {
		return nil, fmt.Errorf("media artifact verifier (postgres): %w", err)
	}
	return &MediaArtifactVerifier{resolver: resolver}, nil
}

var _ capcheckpoint.ArtifactVerifier = (*MediaArtifactVerifier)(nil)

// VerifyArtifact reports whether the recorded digest is still registered in
// the media SSOT. A missing asset is (Exists=false, nil) — a definitive
// staleness the resolver invalidates — while a transport/query failure is an
// error, which the resolver degrades to EXECUTE without invalidating.
func (v *MediaArtifactVerifier) VerifyArtifact(ctx context.Context, sha256, uri string) (capcheckpoint.ArtifactStatus, error) {
	if v == nil || v.resolver == nil {
		return capcheckpoint.ArtifactStatus{}, errors.New("media artifact verifier (postgres): not wired")
	}
	digest := strings.TrimSpace(sha256)
	if digest == "" {
		// The resolver only asks about units that recorded an artifact, so an
		// empty digest is a caller bug: report "missing" rather than a fake
		// pass. Never invent availability.
		return capcheckpoint.ArtifactStatus{Exists: false, SHA256Matches: false}, nil
	}
	_, err := v.resolver.ResolveContent(ctx, digest)
	if err == nil {
		return capcheckpoint.ArtifactStatus{Exists: true, SHA256Matches: true}, nil
	}
	if errors.Is(err, capregistry.ErrCanonicalIdentityNotFound) {
		return capcheckpoint.ArtifactStatus{Exists: false, SHA256Matches: false}, nil
	}
	return capcheckpoint.ArtifactStatus{}, fmt.Errorf("media artifact verifier (postgres): verify %s: %w", digest, err)
}
