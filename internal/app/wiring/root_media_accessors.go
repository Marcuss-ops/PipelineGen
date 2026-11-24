package wiring

// root_media_accessors.go keeps the ComposeRoot-facing media read/admin
// surface in the composition root while the bundle builders live in
// buildbundle. PostgreSQL remains the only media authority.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/modules/media"
	appadminconsole "github.com/Marcuss-ops/PipelineGen/internal/capabilities/adminconsole"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

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
	return mediasub.NewPostgresMediaCommitter(mediasub.CommitterDeps{DB: root.MediaPostgres, Log: log})
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
	if tags := detail.EncodeStringArrayColumn(a.Tags); tags != "" {
		patch.Tags = &tags
	}
	if terms := detail.EncodeStringArrayColumn(a.SearchTerms); terms != "" {
		patch.SearchTerms = &terms
	}
	if meta := mediaMetadataPatchJSON(a); meta != "" {
		patch.MetadataPatchJSON = &meta
	}
	return s.mutator.PatchAsset(ctx, patch)
}

// MediaAssetReader resolves the read-only view (operator API). It returns nil
// when the media SSOT is closed, which leaves the operator module unregistered
// instead of serving a divergent catalog.
func (r *ComposeRoot) MediaAssetReader() MediaAssetReader {
	store, err := r.MediaAssetStore()
	if err != nil || store == nil {
		return nil
	}
	return store
}

// MediaAssetStore resolves the admin console store from the PostgreSQL media
// SSOT, and fails closed when that plane is closed.
//
// PostgreSQL is the ONLY media authority. RequireMediaPostgres is the single
// engine decision point for the media plane and documents the invariant: an
// enabled plane "means PostgreSQL is mandatory ... There is no SQLite/pgvector
// fallback". The SQLite-backed store (sqliteMediaAssetStore) was therefore
// DELETED on 2026-09-13: behind the SAME interface it read
// media_assets.admin_version from the operational mirror and wrote media rows
// through detail.Service.Save, so the admin console could read a stale version
// and patch a row on an engine the canonical committer does not own — the read
// half of the split-brain plus a write seam whose engine was invisible to the
// caller. With the plane closed the admin-console and operator modules are now
// simply not registered, which is the fail-closed outcome the rules require
// (never represent an unavailable backend as a working one).
func (r *ComposeRoot) MediaAssetStore() (MediaAssetStore, error) {
	if r == nil {
		return nil, errors.New("media asset store: composition root is nil")
	}
	if r.MediaPostgres == nil {
		return nil, errors.New("media asset store: media PostgreSQL SSOT is required (no second media catalog)")
	}
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
// SQLite/pgvector fallback." Returning the legacy SQLite detail.Service
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
// port. PostgreSQL owns media_assets.admin_version, so this is
// PostgreSQL-ONLY and returns nil when the media plane is closed (the same
// contract as MediaAssetDetailsLookup): the admin-console module already
// refuses to register without the media SSOT, so a nil store cannot starve a
// supported deployment. The SQLite fallback
// (adminconsolesqlite.NewVersionStore) was DELETED in the MEDIA LEGACY
// READ-PLANE DEMOLITION (2026-10-26) — it read admin_version off the
// operational mirror that holds no committed media rows.
func (r *ComposeRoot) MediaAssetVersionStore() appadminconsole.EntityVersionStore {
	if r == nil || r.MediaPostgres == nil {
		return nil
	}
	return pgmedia.NewPostgresEntityVersionStore(r.MediaPostgres)
}

// ── helpers ─────────────────────────────────────────────────────────────

func mediaStringPtr(v string) *string {
	return &v
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
