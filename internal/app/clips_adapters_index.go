package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	clips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// clipsIndexerAdapter wraps *clipindexer.Service to satisfy
// clips.ClipIndexerPort. IsEnabled + IndexClip are the only methods
// the clips bulk-upload worker + EnrichUseCase call.
type clipsIndexerAdapter struct {
	inner *clipindexer.Service
}

// Compile-time assertion: clipsIndexerAdapter satisfies clips.ClipIndexerPort.
var _ clips.ClipIndexerPort = (*clipsIndexerAdapter)(nil)

func newClipsIndexerAdapter(svc *clipindexer.Service) clips.ClipIndexerPort {
	if svc == nil {
		return nil
	}
	return &clipsIndexerAdapter{inner: svc}
}

func (a *clipsIndexerAdapter) IsEnabled() bool {
	if a.inner == nil {
		return false
	}
	return a.inner.IsEnabled()
}

func (a *clipsIndexerAdapter) IndexClip(ctx context.Context, id string) error {
	if a.inner == nil {
		return fmt.Errorf("clipsIndexerAdapter: indexer not wired")
	}
	return a.inner.IndexClip(ctx, id)
}

func (a *clipsIndexerAdapter) BatchReindex(ctx context.Context, source, mediaType string, limit int) (*clips.ClipIndexBatchResultDTO, error) {
	if a.inner == nil {
		return nil, fmt.Errorf("clipsIndexerAdapter: indexer not wired")
	}
	res, err := a.inner.BatchReindex(ctx, source, mediaType, limit)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return &clips.ClipIndexBatchResultDTO{}, nil
	}
	return &clips.ClipIndexBatchResultDTO{
		Total:    res.Total,
		Indexed:  res.Indexed,
		Skipped:  res.Skipped,
		Failed:   res.Failed,
		AssetIDs: res.AssetIDs,
	}, nil
}

// ── Folder memory adapter (empty marker) ─────────────────────────

// clipsFolderMemoryAdapter wraps *foldermemory.Service to satisfy
// clips.ClipFolderMemoryPort. The interface is currently empty —
// the handler stores the dependency but does not call any method,
// and the adapter is the seam that future consumers extend with
// LoadManifest / SaveManifest / UpdateManifestTXT /
// ComputeManifestStats as needed (one PR at a time).
type clipsFolderMemoryAdapter struct {
	inner *foldermemory.Service
}

// Compile-time assertion: clipsFolderMemoryAdapter satisfies clips.ClipFolderMemoryPort.
var _ clips.ClipFolderMemoryPort = (*clipsFolderMemoryAdapter)(nil)

func newClipsFolderMemoryAdapter(svc *foldermemory.Service) clips.ClipFolderMemoryPort {
	if svc == nil {
		return nil
	}
	return &clipsFolderMemoryAdapter{inner: svc}
}

// ── Hash adapter ─────────────────────────────────────────────────

// clipsHashAdapter wraps hashutil.MD5File behind clips.ClipHashPort
// so the bulk_upload_worker code path doesn't have to import
// "internal/infrastructure/files" wholesale. MD5File is the only
// call site; expanding the surface must land via a new port method.
type clipsHashAdapter struct{}

// Compile-time assertion: clipsHashAdapter satisfies clips.ClipHashPort.
var _ clips.ClipHashPort = (*clipsHashAdapter)(nil)

func newClipsHashAdapter() clips.ClipHashPort {
	return &clipsHashAdapter{}
}

func (a *clipsHashAdapter) MD5File(path string) (string, error) {
	return files.MD5File(path)
}

// ── Source resolver adapter ──────────────────────────────────────

// sourceResolverAdapter wraps the composition-root
// *artifacts.SourceResolver and re-projects its 3 internal repo
// pointers through clips.ClipRepositoryPort, so the handler's
// repoForSource(source string) ClipRepositoryPort stays port-pure.
// The 3 adapter slots MUST be created from the same concrete repos
// the resolver holds internally; otherwise the canonical-source
// mime types desync from the actual repo the resolver returns.
type sourceResolverAdapter struct {
	artlist clips.ClipRepositoryPort
	clips   clips.ClipRepositoryPort
	stock   clips.ClipRepositoryPort
}

// Compile-time assertion: sourceResolverAdapter satisfies clips.SourceResolverPort.
var _ clips.SourceResolverPort = (*sourceResolverAdapter)(nil)

func newSourceResolverAdapter(
	artlistRepo clips.ClipRepositoryPort,
	clipsRepo clips.ClipRepositoryPort,
	stockRepo clips.ClipRepositoryPort,
) clips.SourceResolverPort {
	return &sourceResolverAdapter{
		artlist: artlistRepo,
		clips:   clipsRepo,
		stock:   stockRepo,
	}
}

// ResolveRepo returns the canonical-source repo as a port.
// Mirrors *artifacts.SourceResolver.ResolveRepo with one swap:
// the return type is clips.ClipRepositoryPort (port), not
// *assets.ClipsRepository (concrete infra). The handoff table maps
// aliases "youtube"/"clips"/"sound_effect" to the clipsRepo slot and
// "all"/"unified" to the clipsRepo primary access point. Voiceover
// and images resolve to nil (the handler deals with those via the
// separate VoiceoverRepositoryPort + ImageRepositoryPort slots).
func (r *sourceResolverAdapter) ResolveRepo(source string) clips.ClipRepositoryPort {
	canonical := artifacts.CanonicalSource(source)
	switch canonical {
	case "artlist":
		return r.artlist
	case "clips", "youtube", "sound_effect":
		return r.clips
	case "stock":
		return r.stock
	case "all", "unified":
		return r.clips
	default:
		return nil
	}
}

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

// PG-005 (June 2026): newClipsAdapterBundle is the canonical
// composition-root constructor for the clips API deps. It returns
// a bundle of typed ports so module_assets.go can hand them to
// clipsapi.NewHandler in a single struct literal. Nil-tolerant —
// production wiring passes all concrete deps; tests can pass nil
// for any subset and observe the matching `if h.xy != nil` short-
// circuit behaviour the handler code has long relied on.

type clipsAdapterBundle struct {
	Cfg            clips.ClipConfigPort
	SourceResolver clips.SourceResolverPort
	ClipsRepo      clips.ClipRepositoryPort
	StockRepo      clips.ClipRepositoryPort
	ArtlistRepo    clips.ClipRepositoryPort
	VoiceoverRepo  clips.VoiceoverRepositoryPort
	ImagesRepo     clips.ImageRepositoryPort
	DriveUploader  clips.ClipDriveUploaderPort
	MetaWriter     clips.ClipMetaWriterPort
	ClipIndexer    clips.ClipIndexerPort
	FolderMemSvc   clips.ClipFolderMemoryPort
	HashSvc        clips.ClipHashPort
	TreeBuilderSvc clips.ClipTreeBuilderPort
}

// newClipsAdapterBundle wires the 11 concrete deps into typed ports.
// PG-034 (June 2026): vectorSvc arg removed — Qdrant capability deleted.
//
// The configuration/log parameters are only retained for future
// adapters that need them; today's 11 adapters are bootstrap-pure.
func newClipsAdapterBundle(
	cfg *config.Config,
	log *zap.Logger,
	artlistRepo *assets.ClipsRepository,
	clipsRepo *assets.ClipsRepository,
	stockRepo *assets.ClipsRepository,
	voiceoverRepo *assets.VoiceoversRepository,
	imagesRepo *assets.ImagesRepository,
	driveUp *drive.Uploader,
	metaWriter *semantic.MetadataWriter,
	clipIndexer *clipindexer.Service,
	folderMemSvc *foldermemory.Service,
	assetTreeSvc *assettree.Service,
	_ /* vectorSvc removed PG-034 */ any,
	timeouts appjobs.TimeoutResolver,
) clipsAdapterBundle {
	_ = log // reserved for future adapters that need a logger
	artPort := newClipsRepoAdapter(artlistRepo)
	clpPort := newClipsRepoAdapter(clipsRepo)
	stockPort := newClipsRepoAdapter(stockRepo)
	return clipsAdapterBundle{
		// HC-1 (June 2026): pass the typed TimeoutResolver (canonical
		// impl: jobs.Compose() — *jobs.Registry) to the cfg adapter so
		// the bulk_upload worker can resolve per-job-type timeouts
		// through the typed port instead of the pre-HC-1 hard-coded
		// 2*time.Hour literal in bulk_upload_worker.go.
		Cfg:            newClipsCfgAdapter(cfg, timeouts),
		SourceResolver: newSourceResolverAdapter(artPort, clpPort, stockPort),
		ClipsRepo:      clpPort,
		StockRepo:      stockPort,
		ArtlistRepo:    artPort,
		VoiceoverRepo:  newVoiceoverRepoAdapter(voiceoverRepo),
		ImagesRepo:     newImageRepoAdapter(imagesRepo),
		DriveUploader:  newClipsDriveAdapter(driveUp),
		MetaWriter:     newClipMetaWriterAdapter(metaWriter),
		ClipIndexer:    newClipsIndexerAdapter(clipIndexer),
		FolderMemSvc:   newClipsFolderMemoryAdapter(folderMemSvc),
		HashSvc:        newClipsHashAdapter(),
		TreeBuilderSvc: newClipsAssetTreeAdapter(assetTreeSvc),
	}
}
