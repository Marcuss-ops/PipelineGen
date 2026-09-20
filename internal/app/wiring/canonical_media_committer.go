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
// *pgmedia.PostgresAssetStore is the ONLY concrete for the admin/operator read
// port. Its SQLite counterpart (sqliteMediaAssetStore) was DELETED on
// 2026-09-13 (MEDIA-SSOT P2-9): it read media_assets.admin_version from the
// operational mirror and wrote media rows through the generic detail.Service
// seam while the canonical committer wrote PostgreSQL, so the admin console
// could read a stale version and patch a row on an engine nobody owns. With
// the media plane closed the admin-console and operator modules are now simply
// not registered — degraded is honest, a second media catalog is not.
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
	youtube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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
// enabled plane "means PostgreSQL is mandatory ... There is no SQLite/Qdrant
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

// ── YouTube enrichment persistence ──────────────────────────────────────

// youTubeAssetDispatcher is the narrow canonical-writer surface the YouTube
// enrichment adapter consumes. *outbox.Dispatcher implements it.
type youTubeAssetDispatcher interface {
	EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error
	SaveDiscoveredAsset(ctx context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error
}

// youTubeAssetWriter routes YouTube enrichment persistence through the
// canonical media writer.
//
// WHY THIS EXISTS. youtube/usecase used to hold a detail.Repository and call
// Upsert. In the PostgreSQL media mode that repository is the SQLite media
// adapter, so enrichment wrote media_assets on SQLite while the media SSOT was
// PostgreSQL — a WRITE split-brain, invisible to
// percheck_media_assets_writer_canonical because no SQL appears at the call
// site (the split happens behind a method call).
//
// ROUTING RULE (fail-honest, never a fake success):
//   - content hash known   -> dispatcher.EnqueueAndIndex: the canonical
//     commit-and-index path (media_assets + the media index-request event in
//     ONE media-SSOT transaction).
//   - content hash missing -> dispatcher.SaveDiscoveredAsset: the asset is not
//     indexable yet, so the row is persisted through the SAME canonical writer
//     with its current lifecycle/index state and deliberately NO index request.
//     This keeps the enrichment durable without fabricating an indexable asset.
//
// A content hash should be present for any clip downstream of extraction; the
// second branch exists only to preserve the legacy tolerance for the
// discovery-time enrichment path.
type youTubeAssetWriter struct {
	dispatcher youTubeAssetDispatcher
}

// Upsert implements youtube.AssetWriter.
func (w *youTubeAssetWriter) Upsert(ctx context.Context, a *asset.Asset) error {
	if w == nil || w.dispatcher == nil {
		return errors.New("youtube asset writer: canonical dispatcher unavailable")
	}
	if a == nil || strings.TrimSpace(a.ID) == "" {
		return errors.New("youtube asset writer: asset with non-empty id is required")
	}
	if hash := youTubeContentHash(a); hash != "" {
		return w.dispatcher.EnqueueAndIndex(ctx, a, hash)
	}
	return w.dispatcher.SaveDiscoveredAsset(ctx, a, youTubeLifecycleState(a), youTubeIndexState(a))
}

// newYouTubeAssetWriter resolves the YouTube enrichment persistence port.
// The canonical dispatcher + canonical committer exist exactly when the media
// PostgreSQL SSOT is open; the SQLite repository is the documented
// media-disabled degrade fallback (and the only path when the dispatcher has
// no canonical committer to commit through).
func newYouTubeAssetWriter(outboxBundle *OutboxBundle, repos *RepoBundle, committer persistence.AssetCommitter) youtube.AssetWriter {
	if outboxBundle != nil && outboxBundle.Dispatcher != nil && committer != nil {
		return &youTubeAssetWriter{dispatcher: outboxBundle.Dispatcher}
	}
	if repos != nil && repos.Assets != nil {
		return repos.Assets.Repository()
	}
	return nil
}

// youTubeContentHash resolves the canonical content fingerprint for the
// commit-and-index path. BinarySHA256 (content_sha256) is the canonical byte
// identity; legacy_file_md5 is the compatibility fallback the commit request
// field is named after.
func youTubeContentHash(a *asset.Asset) string {
	if a == nil {
		return ""
	}
	if hash := strings.TrimSpace(a.BinarySHA256()); hash != "" {
		return hash
	}
	return strings.TrimSpace(a.LegacyFileMD5())
}

// youTubeLifecycleState keeps the asset's canonical lifecycle state, defaulting
// to ACTIVE when it is missing or invalid (SaveDiscoveredAsset validates it).
func youTubeLifecycleState(a *asset.Asset) asset.LifecycleState {
	if a == nil {
		return asset.StateActive
	}
	if a.LifecycleState.Valid() {
		return a.LifecycleState
	}
	return asset.StateActive
}

// youTubeIndexState keeps the asset's canonical index state, defaulting to
// DISCOVERED when it is missing or invalid (SaveDiscoveredAsset validates it).
func youTubeIndexState(a *asset.Asset) asset.IndexState {
	if a == nil {
		return asset.StateDiscovered
	}
	state := asset.IndexState(strings.TrimSpace(a.GetMetadataString("index_state")))
	if state.Valid() {
		return state
	}
	return asset.StateDiscovered
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
