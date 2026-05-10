package artlist

import (
	"go.uber.org/zap"

	sources "velox/go-master/internal/api/handlers/sources"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/catalogsync"
	"velox/go-master/internal/service/clipresolver"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/config"
)

type Handler = sources.ArtlistHandler

func NewHandler(
	service *artlist.Service,
	catalogSync *catalogsync.Service,
	jobsService *jobservice.Service,
	clipResolver *clipresolver.Service,
	nodeScraperDir string,
	log *zap.Logger,
	presetsConfig *artlist.PresetsConfig,
	cfg *config.Config,
) *Handler {
	return sources.NewArtlistHandler(service, catalogSync, jobsService, clipResolver, nodeScraperDir, log, presetsConfig, cfg)
}
