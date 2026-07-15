package deletion

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"go.uber.org/zap"
)

// NewDeletionService is a test-only compatibility bridge for historical
// external fixtures. Production builds expose only NewService(Dependencies),
// so the positional dependency list cannot leak back into runtime composition.
func NewDeletionService(
	artlistRepo, clipsRepo, stockRepo *assets.ClipsRepository,
	voiceoverRepo *assets.VoiceoversRepository,
	imagesRepo *assets.ImagesRepository,
	assetTreeSvc *assettree.Service,
	assetIndexSvc *assetindex.Service,
	dispatcher DispatcherPort,
	driveGoneChecker DriveGoneChecker,
	completionTxRunner CompletionTxRunner,
	log *zap.Logger,
) *DeletionService {
	return NewService(Dependencies{
		Repositories: RepositoryPorts{
			Artlist:   artlistRepo,
			Clips:     clipsRepo,
			Stock:     stockRepo,
			Voiceover: voiceoverRepo,
			Images:    imagesRepo,
		},
		Mutation: MutationPorts{
			Dispatcher: dispatcher,
			AssetTree:  assetTreeSvc,
		},
		Completion: CompletionPorts{
			DriveGone: driveGoneChecker,
			Tx:        completionTxRunner,
		},
		Maintenance: MaintenancePorts{
			AssetIndex: assetIndexSvc,
		},
		Log: log,
	})
}
