package module

import (
	"context"

	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/api/middleware"
	artlistService "velox/go-master/internal/service/artlist"
	"velox/go-master/pkg/config"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// NewArtlistModule creates a new Artlist module using RouteModule
func NewArtlistModule(
	cfg *config.Config,
	log *zap.Logger,
	service *artlistService.Service,
	handler *artlistHandler.Handler,
) *RouteModule {
	return NewRouteModule(
		"artlist",
		func(cfg *config.Config) bool { return cfg.Features.ArtlistEnabled },
		"/artlist",
		handler,
		log,
		WithStart(func(ctx context.Context) error {
			log.Info("starting artlist module")
			return nil
		}),
		WithStop(func(ctx context.Context) error {
			log.Info("stopping artlist module")
			if service != nil {
				return service.Close()
			}
			return nil
		}),
		// Add ArtlistEnabled middleware
		func(m *RouteModule) {
			m.handler = &artlistHandlerWithMiddleware{
				handler:   m.handler,
				cfg:       cfg,
				middleware: middleware.ArtlistEnabled(cfg),
			}
		},
	)
}

// artlistHandlerWithMiddleware wraps the handler to add ArtlistEnabled middleware
type artlistHandlerWithMiddleware struct {
	handler    interface{ RegisterRoutes(*gin.RouterGroup) }
	cfg        *config.Config
	middleware gin.HandlerFunc
}

func (h *artlistHandlerWithMiddleware) RegisterRoutes(r *gin.RouterGroup) {
	group := r.Group("")
	group.Use(h.middleware)
	h.handler.RegisterRoutes(group)
}
