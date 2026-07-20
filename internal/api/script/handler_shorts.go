// Package script — handler_shorts.go owns the canonical Shorts HTTP
// surface: POST /shorts/generate, POST /shorts/render, and
// POST /shorts/render/async.
//
// PR-SHORTS-EXTRACT (July 2026): the Shorts methods were previously
// co-located on HandlerGenerate, making the generation handler carry
// Remotion renderer/producer concerns. This file extracts them into a
// dedicated, narrow handler so that HandlerGenerate owns only the
// script-generation submission surface.
//
// godlike/06 SSOT: one capability per file, one struct per capability.
// godlike/07 fail-closed: nil renderer/producer return 503, never a
// fake-availability success.
package script

import (
	"context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appvideo "github.com/Marcuss-ops/PipelineGen/internal/application/video"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/remotionjob"
)

// HandlerShorts is the narrow HTTP handler for Remotion Shorts.
// It owns exactly the 3 fields it needs — no more, no less.
type HandlerShorts struct {
	renderer appvideo.Renderer
	producer interface {
		Enqueue(context.Context, remotionjob.RenderJob) (*job.Job, error)
	}
	log *zap.Logger
}

// NewHandlerShorts constructs the canonical Shorts handler.
// Both renderer and producer are optional at construction time;
// the individual handlers return 503 at request time when the
// required dependency is missing.
func NewHandlerShorts(
	renderer appvideo.Renderer,
	producer interface {
		Enqueue(context.Context, remotionjob.RenderJob) (*job.Job, error)
	},
	log *zap.Logger,
) *HandlerShorts {
	if log == nil {
		log = zap.NewNop()
	}
	return &HandlerShorts{
		renderer: renderer,
		producer: producer,
		log:      log,
	}
}

// ShortsRoute registers the /shorts/* routes on the given router group.
// Nil-safe: when h is nil the routes are silently skipped.
func (h *HandlerShorts) ShortsRoute(r *gin.RouterGroup) {
	if h == nil {
		return
	}
	r.POST("/shorts/generate", h.GenerateShorts)
	r.POST("/shorts/render", h.RenderShorts)
	r.POST("/shorts/render/async", h.RenderShortsAsync)
}
