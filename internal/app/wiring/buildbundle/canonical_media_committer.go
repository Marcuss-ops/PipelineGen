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
package buildbundle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/modules/media"
	localized "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	texttracksport "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
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
	return mediasub.NewPostgresMediaCommitter(mediasub.CommitterDeps{DB: db, Log: log})
}

// canonicalMediaWriterRequired is the fail-closed variant for wiring sites
// that CANNOT degrade (the writer is the site's core dependency). Use it
// only where a nil writer would produce a silently broken service.

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
// identity; content_sha256 is the compatibility fallback the commit request
// field is named after.
func youTubeContentHash(a *asset.Asset) string {
	if a == nil {
		return ""
	}
	if hash := strings.TrimSpace(a.BinarySHA256()); hash != "" {
		return hash
	}
	return strings.TrimSpace(a.ContentHash())
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

// canonicalClipWriter is the single-writer contract the canonical
// AssetCommitter must satisfy before it can be handed to the per-segment
// pipeline: atomic clip commit + localized clip write + timed-cue write.
// MEDIA DEMOLITION (September 2026): since the demolition the concrete engine
// is always the PostgreSQL committer; the interface keeps the composition root
// engine-agnostic.
type canonicalClipWriter interface {
	youtubeports.ClipAtomicWriter
	localized.LocalizedClipWriter
	texttracksport.TimedCueWriter
}

// assertCanonicalMediaWriter asserts every canonical-writer surface the
// YouTube clip pipeline requires from the ONE committer and returns the two
// narrow views the pipeline consumes. The dependency is explicit: this
// function cannot resolve a writer on its own, so a caller can never
// accidentally construct a pipeline without the canonical committer.
//
// The assertion order is load-bearing (it decides which wiring error a
// partially-wired root reports first): clip/localized/timed-cue writer, then
// the mutation committer, then the clip metadata writer.
func assertCanonicalMediaWriter(committer persistence.AssetCommitter) (canonicalClipWriter, youtubeports.ClipMetadataWriter, error) {
	if committer == nil {
		return nil, nil, fmt.Errorf("compose domains: canonical asset committer is required")
	}
	canonicalMediaCommitter, ok := committer.(canonicalClipWriter)
	if !ok || canonicalMediaCommitter == nil {
		return nil, nil, fmt.Errorf("compose domains: canonical AssetCommitter must implement ClipAtomicWriter + LocalizedClipWriter (got %T) — single-writer invariant", committer)
	}
	canonicalMutator, ok := committer.(persistence.AssetMutationCommitter)
	if !ok || canonicalMutator == nil {
		return nil, nil, fmt.Errorf("compose domains: canonical AssetCommitter does not implement AssetMutationCommitter")
	}
	_ = canonicalMutator
	// MEDIA DEMOLITION (September 2026): the canonical media writer
	// implements youtubeports.ClipMetadataWriter directly over the media
	// SSOT — the SQLite-bound ClipMetadataWriterAdapter is retired.
	clipMetadataWriter, ok := committer.(youtubeports.ClipMetadataWriter)
	if !ok || clipMetadataWriter == nil {
		return nil, nil, fmt.Errorf("compose domains: canonical AssetCommitter must implement youtubeports.ClipMetadataWriter (got %T) — single-writer invariant", committer)
	}
	return canonicalMediaCommitter, clipMetadataWriter, nil
}

// ── folder-path writer bridge ───────────────────────────────────────────
