package module

import (
	"context"

	"velox/go-master/internal/api/handlers/voiceover"
	"velox/go-master/internal/config"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// NewVoiceoverModule creates a new Voiceover module using RouteModule
// Only registers /sync endpoint; /generate and /batch are handled by the Assets module,
// which supports both sync and async generation via job queue.
func NewVoiceoverModule(
	cfg *config.Config,
	log *zap.Logger,
	syncHandler *voiceover.SyncHandler,
) *RouteModule {
	return NewRouteModule(
		"voiceover",
		func(cfg *config.Config) bool { return cfg.Features.VoiceoverEnabled },
		"/voiceover",
		&voiceoverHandlerWrapper{
			syncHandler: syncHandler,
		},
		log,
		WithStart(func(ctx context.Context) error {
			log.Info("starting voiceover module")
			return nil
		}),
		WithStop(func(ctx context.Context) error {
			log.Info("stopping voiceover module")
			return nil
		}),
	)
}

// voiceoverHandlerWrapper wraps the sync handler to register routes
type voiceoverHandlerWrapper struct {
	syncHandler *voiceover.SyncHandler
}

func (w *voiceoverHandlerWrapper) RegisterRoutes(r *gin.RouterGroup) {
	if w.syncHandler != nil {
		w.syncHandler.RegisterRoutes(r)
	}
}
