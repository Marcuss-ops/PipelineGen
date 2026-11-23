package clipswiring

// deps.go owns the typed input contracts of the clips module.
//
// NewOpsPorts was the widest positional signature in internal/app/modules:
// seven arguments, three of them adjacent Drive ports (Admin, Reader,
// FileLifecycle) that a caller can swap without the compiler noticing. The
// composition site already passed the same uploader twice for admin and
// reader, which is exactly the shape named fields make visible.
// BuildClipRenderRuntime and NewClipRenderDriveDeliveryHandler take four
// arguments each and follow the same contract for the same reason
// (BON-03 step B).

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	clips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
	"go.uber.org/zap"
)

// OpsPortsDeps is the input contract of NewOpsPorts.
type OpsPortsDeps struct {
	ClipRepo       clips.ClipRepositoryPort
	VoiceoverRepo  *assets.VoiceoversRepository
	ImageRepo      *imagesrepo.ImagesRepository
	DriveAdmin     drive.Admin
	DriveReader    drive.Reader
	DriveLifecycle drive.FileLifecycle
	JobsFacade     job.Service
}

// ClipRenderRuntimeDeps is the input contract of BuildClipRenderRuntime.
// Existing is a fully built runtime from a prior phase: when non-nil the
// constructor returns it unchanged instead of reading the config again. There
// is deliberately no Log field: the constructor never logs, and the old
// positional logger parameter was dead.
type ClipRenderRuntimeDeps struct {
	Config    *config.Config
	MediaExec mediaexec.ExecutionConfig
	Existing  *ClipRenderRuntime
}

// ClipRenderDeliveryDeps is the input contract of
// NewClipRenderDriveDeliveryHandler. Publisher and Mutator are required;
// Subtitles is optional (nil is allowed for deployments that never render
// sidecar subtitles).
type ClipRenderDeliveryDeps struct {
	Publisher delivery.Publisher
	Mutator   persistence.AssetMutator
	Subtitles detail.SubtitleArtifactRepository
	Log       *zap.Logger
}
