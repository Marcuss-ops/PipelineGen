package youtubeclip

import (
	"go.uber.org/zap"

	sources "velox/go-master/internal/api/handlers/sources"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/youtubeclip"
)

type Handler = sources.YouTubeClipHandler

func NewHandler(service *youtubeclip.Service, log *zap.Logger, jobsSvc *jobservice.Service) *Handler {
	return sources.NewYouTubeClipHandler(service, log, jobsSvc)
}
