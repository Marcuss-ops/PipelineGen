// Package app — canonical_media_committer.go is the composition-root
// factory for every production asset writer AND for the admin/operator asset
// read+save store.
//
// DEMOLITION COMPLETE (media cutover, September 2026): the SQLite media
// engine (SQLiteMediaCommitter + the SQLite media_assets write path) has
// been removed. The ONLY canonical media writer is PostgresMediaCommitter
// over the PostgreSQL + pgvector media SSOT.
//
// Degradation contract (graceful-degrade mode): when the media PostgreSQL
// handle is unavailable the factory returns (nil, nil) instead of a writer.
// Every caller MUST handle the nil writer by skipping the media-dependent
// wiring it feeds — media features simply do not register (godlike/07:
// degraded is honest; a fake writer would not be). A non-nil writer is
// ALWAYS a working PostgresMediaCommitter.
//
// The read half follows the same rule: PostgreSQL is the media SSOT, so
// *pgmedia.PostgresAssetStore is the production concrete and the SQLite
// *detail.Service satisfies the same narrow interface ONLY for the documented
// media-PostgreSQL-disabled degrade mode. One port, one production
// implementation, selected by composition — never a per-consumer repository
// and never a silent second media registry.
package wiring

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	appadminconsole "github.com/Marcuss-ops/PipelineGen/internal/capabilities/adminconsole"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	adminconsolesqlite "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/adminconsole"
)

// newCanonicalAssetCommitter resolves the canonical media writer from an
// open media PostgreSQL handle. The database handle MUST come from
// RequireMediaPostgres / root.MediaPostgres — this constructor never opens
// connections itself and never consults SQLite.
//
// Returns (nil, nil) when db is nil: the composition-level signal that the
// media plane is degraded and media-consuming wiring must be skipped.
func newCanonicalAssetCommitter(db *sql.DB, log *zap.Logger) (persistence.CanonicalAssetWriter, error) {
	if db == nil {
		return nil, nil
	}
	return mediasub.NewPostgresMediaCommitterFromDB(db, log)
}

// canonicalCommitterForRoot resolves the canonical media writer from the
// composition root. root.MediaPostgres is opened FIRST in NewComposition
// (RequireMediaPostgres), so every wiring site sees a consistent engine.
//
// Returns (nil, nil) when the media plane is degraded — the caller skips
// its media wiring (modules that need media simply do not register).
func canonicalCommitterForRoot(root *ComposeRoot, log *zap.Logger) (persistence.CanonicalAssetWriter, error) {
	if root == nil || root.MediaPostgres == nil {
		return nil, nil
	}
	return mediasub.NewPostgresMediaCommitterFromDB(root.MediaPostgres, log)
}

// canonicalCommitterOrSkipped resolves the canonical writer for wiring
// sites that pass it into a bundle struct: the nil result is forwarded as
// a nil Committer and the consuming module decides whether that degrades
// the whole module or errors.
func canonicalCommitterOrSkipped(root *ComposeRoot, log *zap.Logger) persistence.CanonicalAssetWriter {
	w, err := canonicalCommitterForRoot(root, log)
	if err != nil {
		panic(fmt.Sprintf("canonical media committer: %v", err))
	}
	return w
}

// canonicalMediaWriterRequired is the fail-closed variant for wiring sites
// that CANNOT degrade (the writer is the site's core dependency). Use it
// only where a nil writer would produce a silently broken service.
func canonicalMediaWriterRequired(root *ComposeRoot, site string, log *zap.Logger) (persistence.CanonicalAssetWriter, error) {
	w, err := canonicalCommitterForRoot(root, log)
	if err != nil {
		return nil, fmt.Errorf("%s: canonical media writer: %w", site, err)
	}
	if w == nil {
		return nil, fmt.Errorf("%s: canonical media writer unavailable (media PostgreSQL degraded — composition started without a media SSOT handle)", site)
	}
	return w, nil
}

// ClipReader is the narrow media-clip READ surface shared by admin/operator
// readers. PostgreSQL is the media SSOT, so the production concrete is
// *pgmedia.MediaSearcher; the legacy SQLite *assets.ClipsRepository satisfies
// the same interface only for the media-PostgreSQL-disabled degrade mode.
//
// Keeping ONE interface (instead of a per-consumer reader) is what prevents a
// second media read registry from growing beside the SSOT.
type ClipReader interface {
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
}

// MediaClipReader resolves the canonical clip reader for a composition root.
// It mirrors newClipRenderMediaResolver: PostgreSQL wins whenever the media
// SSOT is open, and the SQLite repository is the documented graceful-degrade
// fallback only when the media plane is intentionally disabled.
func (r *ComposeRoot) MediaClipReader() ClipReader {
	if r == nil {
		return nil
	}
	if r.MediaPostgres != nil {
		return pgmedia.NewMediaSearcher(r.MediaPostgres)
	}
	if r.Repos != nil && r.Repos.ClipsRepo != nil {
		return r.Repos.ClipsRepo
	}
	return nil
}

// GetMediaClip is the fail-closed convenience read used by admin/operator
// commands. It never returns a fake success: an unwired reader is a typed
// error, and a missing asset is (nil, nil) exactly like the legacy surface.
func (r *ComposeRoot) GetMediaClip(ctx context.Context, id string) (*asset.Asset, error) {
	reader := r.MediaClipReader()
	if reader == nil {
		return nil, fmt.Errorf("media clip reader unavailable (postgres media SSOT and legacy clips repository both absent)")
	}
	return reader.GetClip(ctx, id)
}

// ── Admin / operator asset store ────────────────────────────────────────

// MediaAssetReader is the narrow asset read contract shared by the admin
// console and the operator API: details by id, paged summaries, counts.
type MediaAssetReader interface {
	Get(ctx context.Context, id string) (*asset.Details, error)
	List(ctx context.Context, filter asset.Filter) ([]*asset.Summary, error)
	Count(ctx context.Context, filter asset.Filter) (int64, error)
}

// MediaAssetStore is MediaAssetReader plus the admin console mutation entry
// point and the optimistic-concurrency version read.
type MediaAssetStore interface {
	MediaAssetReader

	// Save persists the editable document. On PostgreSQL it is mapped onto the
	// canonical writer's typed patch contract — never raw media SQL.
	Save(ctx context.Context, details *asset.Details) error

	// AdminVersion reads the optimistic-concurrency version for an asset.
	AdminVersion(ctx context.Context, id string) (int, error)
}

// pgMediaAssetStore binds the PostgreSQL media read store to the canonical
// media writer for the admin console's Save entry point.
type pgMediaAssetStore struct {
	reader  *pgmedia.PostgresAssetStore
	mutator persistence.AssetMutator
}

// Get delegates to the single PostgreSQL hydration site.
func (s *pgMediaAssetStore) Get(ctx context.Context, id string) (*asset.Details, error) {
	return s.reader.Get(ctx, id)
}

// List delegates to the PostgreSQL canonical summary projection.
func (s *pgMediaAssetStore) List(ctx context.Context, filter asset.Filter) ([]*asset.Summary, error) {
	return s.reader.List(ctx, filter)
}

// Count delegates to the PostgreSQL canonical count.
func (s *pgMediaAssetStore) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	return s.reader.Count(ctx, filter)
}

// AdminVersion delegates to the PostgreSQL optimistic-concurrency read.
func (s *pgMediaAssetStore) AdminVersion(ctx context.Context, id string) (int, error) {
	return s.reader.AdminVersion(ctx, id)
}

// Save maps the admin console's full-document save onto the canonical typed
// patch. Fields with a dedicated canonical column go to their own patch field;
// description/language live in metadata_json and go through the shallow merge
// patch. Nothing here touches media SQL directly, so the single-writer
// invariant holds.
func (s *pgMediaAssetStore) Save(ctx context.Context, details *asset.Details) error {
	if s.mutator == nil {
		return errors.New("media asset store: canonical writer unavailable (media PostgreSQL degraded)")
	}
	if details == nil || details.Asset == nil {
		return errors.New("media asset store: asset is required")
	}
	a := details.Asset
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("media asset store: asset id is required")
	}

	patch := persistence.AssetPatch{
		AssetID:      a.ID,
		Name:         mediaStringPtr(a.Name),
		Category:     mediaStringPtr(a.Category),
		Group:        mediaStringPtr(a.Group),
		SearchText:   mediaStringPtr(a.SearchText),
		ReviewStatus: mediaStringPtr(string(a.ReviewStatus)),
	}
	if tags := encodeStringSliceJSON(a.Tags); tags != "" {
		patch.Tags = &tags
	}
	if terms := encodeStringSliceJSON(a.SearchTerms); terms != "" {
		patch.SearchTerms = &terms
	}
	if meta := mediaMetadataPatchJSON(a); meta != "" {
		patch.MetadataPatchJSON = &meta
	}
	return s.mutator.PatchAsset(ctx, patch)
}

// sqliteMediaAssetStore is the media-PostgreSQL-disabled implementation. It
// exists ONLY so the admin console/operator API keep working when the operator
// runs the pipeline in the intentional no-media-plane mode; it is never
// selected while the media SSOT is open.
type sqliteMediaAssetStore struct {
	service *detail.Service
	db      *sql.DB
}

func (s *sqliteMediaAssetStore) Get(ctx context.Context, id string) (*asset.Details, error) {
	return s.service.Get(ctx, id)
}

func (s *sqliteMediaAssetStore) List(ctx context.Context, filter asset.Filter) ([]*asset.Summary, error) {
	return s.service.List(ctx, filter)
}

func (s *sqliteMediaAssetStore) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	return s.service.Count(ctx, filter)
}

func (s *sqliteMediaAssetStore) Save(ctx context.Context, details *asset.Details) error {
	return s.service.Save(ctx, details)
}

func (s *sqliteMediaAssetStore) AdminVersion(ctx context.Context, id string) (int, error) {
	if s.db == nil {
		return 0, nil
	}
	var version int
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(admin_version,0) FROM media_assets WHERE id = ?", id).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("sqlite media asset store: read admin version: %w", err)
	}
	return version, nil
}

// MediaAssetReader resolves the read-only view (operator API). PostgreSQL wins
// whenever the media SSOT is open; SQLite only in the media-disabled mode.
func (r *ComposeRoot) MediaAssetReader() MediaAssetReader {
	store, err := r.MediaAssetStore()
	if err != nil || store == nil {
		return nil
	}
	return store
}

// MediaAssetStore resolves the admin console store. It fails closed when no
// media read store is wired at all (rather than returning a half-wired store).
func (r *ComposeRoot) MediaAssetStore() (MediaAssetStore, error) {
	if r == nil {
		return nil, errors.New("media asset store: composition root is nil")
	}
	if r.MediaPostgres != nil {
		reader := pgmedia.NewPostgresAssetStore(pgmedia.NewMediaSearcher(r.MediaPostgres))
		if reader == nil {
			return nil, errors.New("media asset store: postgres read store unavailable")
		}
		var mutator persistence.AssetMutator
		if r.CanonicalAssetWriter != nil {
			mutator = r.CanonicalAssetWriter
		}
		return &pgMediaAssetStore{reader: reader, mutator: mutator}, nil
	}
	if r.Repos != nil && r.Repos.Assets != nil {
		var db *sql.DB
		if r.DB != nil {
			db = r.DB.DB
		}
		return &sqliteMediaAssetStore{service: r.Repos.Assets, db: db}, nil
	}
	return nil, errors.New("media asset store: no media read store wired (postgres media SSOT and legacy asset service both absent)")
}

// AssetDetailsLookup is the narrow single-asset detail lookup consumed by
// jobs.NewAssetTransferService. Declaring it here (rather than passing the
// concrete SQLite *detail.Service) is what lets the PostgreSQL media SSOT
// serve the worker asset-transfer path.
type AssetDetailsLookup interface {
	Get(ctx context.Context, id string) (*asset.Details, error)
}

// MediaAssetDetailsLookup resolves the single-asset detail lookup for the
// worker asset-transfer service. It is the SINGLE owner of that decision: the
// script media preflight consumes this method rather than repeating the engine
// branch, so the two cannot drift.
//
// PostgreSQL is the ONLY media read authority here. RequireMediaPostgres is
// the single engine decision point for the media plane, and it states the
// invariant explicitly: "Enabled means PostgreSQL is mandatory ... There is no
// SQLite/Qdrant fallback." Returning the legacy SQLite detail.Service
// contradicted that invariant and re-created the exact
// Postgres-writer/SQLite-reader split-brain this cutover removed: an asset
// committed by the canonical committer was invisible to the worker, which then
// either mistreated it as missing or hydrated a stale pre-cutover row.
//
// A closed media plane therefore yields nil, which is the fail-closed signal
// the consumer already handles (AssetTransferServiceImpl guards querySvc != nil
// and skips hydration) rather than a divergent second catalog.
func (r *ComposeRoot) MediaAssetDetailsLookup() AssetDetailsLookup {
	if r == nil || r.MediaPostgres == nil {
		return nil
	}
	return pgmedia.NewAssetDetailsReader(pgmedia.NewMediaSearcher(r.MediaPostgres))
}

// MediaAssetVersionStore resolves the admin console optimistic-concurrency
// port. PostgreSQL owns media_assets.admin_version whenever the media SSOT is
// open; the SQLite store is the degrade-mode fallback.
func (r *ComposeRoot) MediaAssetVersionStore() appadminconsole.EntityVersionStore {
	if r == nil {
		return nil
	}
	if r.MediaPostgres != nil {
		if store := pgmedia.NewPostgresEntityVersionStore(r.MediaPostgres); store != nil {
			return store
		}
	}
	if r.DB != nil && r.DB.DB != nil {
		return adminconsolesqlite.NewVersionStore(r.DB.DB)
	}
	return nil
}

// ── helpers ─────────────────────────────────────────────────────────────

func mediaStringPtr(v string) *string {
	return &v
}

// encodeStringSliceJSON returns the canonical JSON-array encoding for a string
// slice, or "" when there is nothing to write.
func encodeStringSliceJSON(values []string) string {
	if len(values) == 0 {
		return ""
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(raw)
}

// mediaMetadataPatchJSON builds the shallow metadata merge for the editable
// metadata-only fields (description, language). Both keys are always present so
// clearing a value persists as a clear rather than a no-op.
func mediaMetadataPatchJSON(a *asset.Asset) string {
	patch := map[string]string{
		"description": a.Description(),
		"language":    a.Language(),
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return ""
	}
	return string(raw)
}
