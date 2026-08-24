package capabilities

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	clips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	ytadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
)

// PR-CLIPS-INDEXER-PORT-RETIRE (August 2026): clipsIndexerAdapter +
// newClipsIndexerAdapter + clips.ClipIndexerPort are REMOVED. The port
// existed only to serve the clips HTTP reindex transports
// (ReindexClip + BatchReindex); both were retired in favor of the
// canonical /api/assets/operator/assets/:id/reindex surface and the
// media.reindex job (enqueueable via POST /api/jobs). The concrete
// *clipindexer.Service remains wired independently as the media.reindex
// job handler (wireClipIndexerJobBinding) and the outbox indexing
// handler — no application-layer clips port is needed for those paths.

// PR-DEADC-CLIPS-FOLDER-MEMORY-PORT-RETIRE (July 2026): the
// `clipsFolderMemoryAdapter` + `newClipsFolderMemoryAdapter` + the
// `clips.ClipFolderMemoryPort` interface + the `FolderMemSvc` field on
// clipsAdapterBundle are all REMOVED. The empty-marker
// ClipFolderMemoryPort (interface{}) was never invoked by any handler
// or use case; the canonical `*foldermemory.Service` consumer at
// `internal/api/assets/clips/handler.go:76::FolderMemSvc *foldermemory.Service`
// is PRESERVED (the real OpsHandler consumer, not the dead-code port
// adapter). Future typed-port additions must land as a new
// `clips.<X>Port` interface + concrete adapter per godlike/06 SSOT
// one-canonical-owner-per-fact.

// ── Hash adapter ─────────────────────────────────────────────────

// clipsHashAdapter wraps checksum.LegacyMD5File behind clips.ClipHashPort
// so the bulk_upload_worker code path doesn't have to import
// "internal/platform/checksum" wholesale. MD5File is the only
// call site; expanding the surface must land via a new port method.
type clipsHashAdapter struct{}

// Compile-time assertion: clipsHashAdapter satisfies clips.ClipHashPort.
var _ clips.ClipHashPort = (*clipsHashAdapter)(nil)

func newClipsHashAdapter() clips.ClipHashPort {
	return &clipsHashAdapter{}
}

func (a *clipsHashAdapter) MD5File(path string) (string, error) {
	return checksum.LegacyMD5File(path)
}

// ── Source resolver adapter ──────────────────────────────────────
//
// PR-CLIPS-DAPTER-RESOLVER-RETIRE (July 2026): the sourceResolverAdapter
// is REMOVED. All clip-type sources share a single canonical clips.ClipRepositoryPort
// in production; the per-source discriminator moves to the QUERY layer
// (the repo methods accept `source` as a filter parameter), not at
// port-selection time the resolver enabled before. The 2 production
// adapters (sourceResolverAdapter in this file + clipOpsSourceResolverAdapter
// in clips_adapters_ops.go) and the SourceResolverPort interface are
// all retired in this wave — the canonical-clip-repo distillation is
// now exposed directly via `clipsOpsPorts.ClipsRepo` in the composition
// root at wire_assets_clips.go::buildClipsBundle.

// ── Vector store adapter ─────────────────────────────────────────
//
// PG-034 (June 2026): clipsVectorAdapter removed — the Qdrant
// capability was deleted.

// ── Asset tree adapter ───────────────────────────────────────────

// clipsAssetTreeAdapter wraps *assettree.Service to satisfy
// clips.ClipTreeBuilderPort. UpsertFromAsset bridges the domain
// *asset.Asset → concrete *assets.AssetNode at the infra seam, so
// internal/application/clips has zero infra imports. The node shape
// conversion lives here (not in the use case) because it touches the
// infrastructure type.
type clipsAssetTreeAdapter struct {
	inner *assettree.Service
}

// Compile-time assertion: clipsAssetTreeAdapter satisfies clips.ClipTreeBuilderPort.
var _ clips.ClipTreeBuilderPort = (*clipsAssetTreeAdapter)(nil)

func newClipsAssetTreeAdapter(svc *assettree.Service) clips.ClipTreeBuilderPort {
	if svc == nil {
		return nil
	}
	return &clipsAssetTreeAdapter{inner: svc}
}

// UpsertFromAsset converts *asset.Asset → *assets.AssetNode and calls
// the underlying assettree.Service.UpsertNode. Nil-tolerant: nil clip
// is a no-op so callers don't need defensive nil checks before every
// call.
func (a *clipsAssetTreeAdapter) UpsertFromAsset(ctx context.Context, clip *asset.Asset) error {
	if a.inner == nil || clip == nil {
		return nil
	}
	return a.inner.UpsertNode(ctx, clipToAssetNode(clip))
}

// clipToAssetNode is the domain-to-infra asset-node converter. PG-005
// (June 2026): moved from internal/application/clips/bulk_tags.go
// into the adapter layer so the use case has zero infra imports.
// Updates to the asset-tree node shape land here as the only place
// that knows about both the domain *asset.Asset and the concrete
// *assets.AssetNode.
func clipToAssetNode(clip *asset.Asset) *assets.AssetNode {
	if clip == nil {
		return nil
	}
	nodeType := "file"
	if clip.IsFolder() {
		nodeType = "folder"
	} else if clip.MediaType != "" {
		nodeType = string(clip.MediaType)
	}
	return &assets.AssetNode{
		ID:          clip.ID,
		Source:      string(clip.Source),
		AssetID:     clip.ID,
		Name:        clip.Name,
		Type:        nodeType,
		ParentID:    clip.ParentFolderID(),
		Path:        clip.FolderPath(),
		Depth:       clip.Depth(),
		IsFolder:    clip.IsFolder(),
		DriveFileID: clip.DriveFileID(),
		DriveLink:   clip.DriveLink(),
		Metadata:    clip.MetadataJSON(),
		CreatedAt:   clip.CreatedAt,
		UpdatedAt:   clip.UpdatedAt,
		ChildCount:  clip.ChildCount(),
	}
}

// PR-CLIPS-DAPTER-BUNDLE-SLIM (July 2026): clipsAdapterBundle (10-field)
// and newClipsAdapterBundle (14-arg ctor) are RETIRED. The canonical
// build is now buildClipOpsPorts(clipRepo, jobs) — strict 2-arg per
// the user spec. The 5 port fields on clipsOpsPorts are the ONLY
// ones ClipOpsService consumes; every other field on the legacy
// bundle (Cfg + StockRepo + ArtlistRepo + MetaWriter + ClipIndexer +
// HashSvc + TreeBuilderSvc) was either dead-weight on the bundle struct
// or consumed inline at the wire_assets_clips.go::buildClipsBundle
// call site. The RetiredSurfaceADT section below documents the
// godlike/06 SSOT one-canonical-owner-per-fact invariants so future
// refactors don't reintroduce the dead ports.
//
// RetiredSurfaceADT (godlike/06 SSOT one-canonical-owner-per-fact,
// preserved for surgical-migration tracking):
//   - clipsAdapterBundle.Cfg → unused on the bundle; clipOpsService
//     untyped paths construct newClipsCfgAdapter inline.
//   - clipsAdapterBundle.StockRepo / ArtlistRepo → NEVER USED (dead
//     per PR-CLIPS-DAPTER-RESOLVER-RETIRE). The per-source discriminator
//     moved to QUERY-layer filters on the canonical repos.
//   - clipsAdapterBundle.MetaWriter / ClipIndexer / HashSvc /
//     TreeBuilderSvc → consumed inline by bulkUploadWorker +
//     UploadUseCase + ReuploadUseCase construction sites at
//     wire_assets_clips.go::buildClipsBundle. Each call site
//     constructs these adapters fresh for nil-tolerance semantics;
//     caching them on the bundle would over-allocate without benefit.

// clipsOpsPorts holds ONLY the 5 typed ports that ClipOpsService
// consumes. JobFacades is narrowed from *appjobs.Service (the canonical
// job-broker facade type) to clips.JobsServicePort (the narrow
// use-case surface) via newClipsJobsPortAdapter inline so
// ClipOpsService stays typed-port-monomorphic per AGENTS.md Pattern 0
// + godlike/06 SSOT (one canonical owner per port contract).
type clipsOpsPorts struct {
	clipRepo      clips.ClipRepositoryPort
	voiceoverRepo clips.VoiceoverRepositoryPort
	imageRepo     clips.ImageRepositoryPort
	driveUploader clips.ClipDriveUploaderPort
	jobsPort      clips.JobsServicePort
}

// buildClipOpsPorts is the canonical SLIM constructor per the user
// spec literal for PR-CLIPS-DAPTER-BUNDLE-SLIM (July 2026). Strict
// 2-arg surface: clipRepo (canonical clip-side repo port) + jobs
// (the wiring.JobsBundle aggregator, polluted with the 4 cross-domain deps
// per the godlike/06 SSOT pollute-at-the-bundle-stem trade-off). The
// 5 fields of clipsOpsPorts are constructed inline in one place
// (this function) so the dead-weight surface stays gone in lockstep.
//
// Nil-tolerance per godlike/07 minimum-blast-radius: drives off the
// typed-port constructors which are themselves nil-tolerant; any nil
// dep surfaces as a typed error at first use, never silent-success
// in production.
func buildClipOpsPorts(clipRepo clips.ClipRepositoryPort, jobs *wiring.JobsBundle) clipsOpsPorts {
	return clipsOpsPorts{
		clipRepo:      clipRepo,
		voiceoverRepo: newVoiceoverRepoAdapter(jobs.VoiceoverRepo),
		imageRepo:     newImageRepoAdapter(jobs.ImagesRepo),
		driveUploader: ytadapters.NewClipsDriveAdapter(jobs.DriveUploader, jobs.DriveUploader, jobs.DriveLifecycle),
		jobsPort:      newClipsJobsPortAdapter(jobs.Facade),
	}
}
