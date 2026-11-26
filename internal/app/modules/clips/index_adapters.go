package clipswiring

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assettree"
	clips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	ytadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
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
	return a.inner.UpsertNode(ctx, clips.ClipToAssetNode(clip))
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
func NewOpsPorts(deps OpsPortsDeps) OpsPorts {
	return OpsPorts{
		ClipRepo:      deps.ClipRepo,
		VoiceoverRepo: NewVoiceoverRepoAdapter(deps.VoiceoverRepo),
		ImageRepo:     NewImageRepoAdapter(deps.ImageRepo),
		DriveUploader: ytadapters.NewClipsDriveAdapter(deps.DriveAdmin, deps.DriveReader, deps.DriveLifecycle),
		JobsPort:      NewClipsJobsPortAdapter(deps.JobsFacade),
	}
}
