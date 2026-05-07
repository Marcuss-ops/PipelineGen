package module

import (
	"context"

	"velox/go-master/internal/api/handlers/voiceover"
	"velox/go-master/pkg/config"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// NewVoiceoverModule creates a new Voiceover module using RouteModule
func NewVoiceoverModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *voiceover.Handler,
	syncHandler *voiceover.SyncHandler,
) *RouteModule {
	return NewRouteModule(
		"voiceover",
		func(cfg *config.Config) bool { return cfg.Features.VoiceoverEnabled },
		"/voiceover",
		&voiceoverHandlerWrapper{
			handler:     handler,
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

// voiceoverHandlerWrapper wraps both voiceover handlers to register all routes
type voiceoverHandlerWrapper struct {
	handler     *voiceover.Handler
	syncHandler *voiceover.SyncHandler
}

func (w *voiceoverHandlerWrapper) RegisterRoutes(r *gin.RouterGroup) {
	if w.handler != nil {
		w.handler.RegisterRoutes(r)
	}
	if w.syncHandler != nil {
		w.syncHandler.RegisterRoutes(r)
	}
}
