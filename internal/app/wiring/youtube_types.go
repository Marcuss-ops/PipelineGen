package wiring

import (
	ytService "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
)

// YouTubeClipWiring is the published YouTube clip module surface.
type YouTubeClipWiring struct {
	Module  api.Module
	Service *ytService.Service
}
