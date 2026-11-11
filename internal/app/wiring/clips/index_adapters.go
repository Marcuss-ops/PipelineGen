package clipswiring

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assettree"
	clips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	ytadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
)

// hashAdapter exposes the legacy file checksum through the clips port.
type hashAdapter struct{}

var _ clips.ClipHashPort = (*hashAdapter)(nil)

func NewHashAdapter() clips.ClipHashPort { return &hashAdapter{} }

func (*hashAdapter) MD5File(path string) (string, error) {
	return checksum.LegacyMD5File(path)
}

// assetTreeAdapter translates the kernel asset model at the platform seam.
type assetTreeAdapter struct{ inner *assettree.Service }

var _ clips.ClipTreeBuilderPort = (*assetTreeAdapter)(nil)

func NewAssetTreeAdapter(svc *assettree.Service) clips.ClipTreeBuilderPort {
	if svc == nil {
		return nil
	}
	return &assetTreeAdapter{inner: svc}
}

func (a *assetTreeAdapter) UpsertFromAsset(ctx context.Context, clip *asset.Asset) error {
	if a.inner == nil || clip == nil {
		return nil
	}
	return a.inner.UpsertNode(ctx, clipToAssetNode(clip))
}

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
		ID: clip.ID, Source: string(clip.Source), AssetID: clip.ID, Name: clip.Name,
		Type: nodeType, ParentID: clip.ParentFolderID(), Path: clip.FolderPath(),
		Depth: clip.Depth(), IsFolder: clip.IsFolder(), DriveFileID: clip.DriveFileID(),
		DriveLink: clip.DriveLink(), Metadata: clip.MetadataJSON(),
		CreatedAt: clip.CreatedAt, UpdatedAt: clip.UpdatedAt, ChildCount: clip.ChildCount(),
	}
}

// OpsPorts contains the narrow typed dependencies consumed by ClipOpsService.
type OpsPorts struct {
	ClipRepo      clips.ClipRepositoryPort
	VoiceoverRepo clips.VoiceoverRepositoryPort
	ImageRepo     clips.ImageRepositoryPort
	DriveUploader clips.ClipDriveUploaderPort
	JobsPort      clips.JobsServicePort
}

// NewOpsPorts assembles the ClipOps ports from their direct dependencies.
func NewOpsPorts(
	clipRepo clips.ClipRepositoryPort,
	voiceoverRepo *assets.VoiceoversRepository,
	imageRepo *imagesrepo.ImagesRepository,
	driveAdmin drive.Admin,
	driveReader drive.Reader,
	driveLifecycle drive.FileLifecycle,
	jobsFacade job.Service,
) OpsPorts {
	return OpsPorts{
		ClipRepo:      clipRepo,
		VoiceoverRepo: NewVoiceoverRepoAdapter(voiceoverRepo),
		ImageRepo:     NewImageRepoAdapter(imageRepo),
		DriveUploader: ytadapters.NewClipsDriveAdapter(driveAdmin, driveReader, driveLifecycle),
		JobsPort:      NewClipsJobsPortAdapter(jobsFacade),
	}
}
