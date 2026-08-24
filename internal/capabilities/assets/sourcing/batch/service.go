// Package batch — the BatchRegistrar use case extracted from the historical
// sourcing.Service.BatchRegisterFromYouTube body (P0-1 / commit 2, June 2026).
//
// Per AGENTS.md Pattern 0 (port abstraction) + Pattern 5 (one concept per file):
// the BatchRegistrar owns the per-batch YouTube flow as a focused service
// with 2 narrow deps (ClipJobEnqueuer + Logger). The façade
// sourcing.Service.BatchRegisterFromYouTube delegates to *Service.BatchRegister
// for backward compatibility.
//
// PR-BATCH-REGISTER-ASYNC (July 2026): replaced the synchronous YouTubeRegistrar
// loop with async ClipJobEnqueuer. Each clip is enqueued as an independent
// media.clip job; yt-dlp + cut + Drive upload happen off the request thread.
// The handler returns immediately with job_ids.
//
// Sub-package construction is *Service.NewService(enqueuer, log) — see
// internal/app/assets_register_sourcing.go for wiring.
package batch

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
)

// ClipJobEnqueuer is the narrow port for enqueuing a per-clip registration
// job. Implemented by the composition root via an adapter wrapping
// appjobs.Service.Enqueue with typing middleware.
//
// PR-BATCH-REGISTER-ASYNC (July 2026): canonical async path. Each
// RegisterClipCommand becomes one media.clip job; the worker handler
// (registered in internal/app/assets_register_sourcing.go) decodes the
// payload and calls YouTubeRegistrar.Register off-thread.
type ClipJobEnqueuer interface {
	EnqueueClip(ctx context.Context, cmd sourcing.RegisterClipCommand) (jobID string, err error)
}

// Service is the BatchRegistrar implementation. 2-port budget per
// architecture/policy.yaml::max_constructor_deps (well under the 8 cap).
type Service struct {
	enqueuer ClipJobEnqueuer
	log      sourcing.Logger
}

// NewService creates a BatchRegistrar service. enqueuer is REQUIRED (batch
// enqueues one async job per clip via the ClipJobEnqueuer port).
func NewService(enqueuer ClipJobEnqueuer, log sourcing.Logger) *Service {
	return &Service{enqueuer: enqueuer, log: log}
}

// BatchRegister enqueues N async media.clip jobs (one per clip) and returns
// immediately with job_ids. The heavy work (yt-dlp + cut + Drive upload +
// DB write + indexing) happens off the request thread via the worker.
//
// PR-BATCH-REGISTER-ASYNC (July 2026): replaced the synchronous
// YouTubeRegistrar.Register loop with async ClipJobEnqueuer.EnqueueClip.
// Each clip becomes one independent media.clip job; the handler returns
// immediately with job_ids array so the caller can poll GET /api/jobs/:id.
// OK=false on every BatchClipResult (outcome unknown at enqueue time);
// BatchRegisterResult.Enqueued counts jobs submitted, EnqueueFailed counts
// per-clip enqueue errors.
//
// godlike/07 typed-error contract: per-clip enqueue failures are captured
// in BatchClipResult.Error + empty JobID; batch-level failure (nil enqueuer)
// returns OK:false + all-enqueue-failed.
func (s *Service) BatchRegister(ctx context.Context, commands []sourcing.RegisterClipCommand) *sourcing.BatchRegisterResult {
	if s == nil || s.enqueuer == nil {
		results := make([]sourcing.BatchClipResult, len(commands))
		for i := range results {
			results[i].Name = commands[i].Name
			results[i].Error = "batch enqueuer not wired (composition bug — wire ClipJobEnqueuer at composition time per PR-BATCH-REGISTER-ASYNC)"
		}
		return &sourcing.BatchRegisterResult{
			OK:            false,
			Total:         len(commands),
			EnqueueFailed: len(commands),
			Results:       results,
		}
	}

	log := s.log
	results := make([]sourcing.BatchClipResult, len(commands))
	var enqueued, enqueueFailed int

	log.Info("starting async batch registration", "service", "batch", "clips", len(commands))
	for i, cmd := range commands {
		br := sourcing.BatchClipResult{Name: cmd.Name}

		jobID, err := s.enqueuer.EnqueueClip(ctx, cmd)
		if err != nil {
			br.Error = fmt.Sprintf("enqueue: %v", err)
			results[i] = br
			enqueueFailed++
			log.Info("batch clip enqueue failed",
				"index", i+1,
				"total", len(commands),
				"name", cmd.Name,
				"ok", false,
				"error", err.Error(),
			)
			continue
		}

		br.JobID = jobID
		results[i] = br
		enqueued++
		log.Info("batch clip enqueued",
			"index", i+1,
			"total", len(commands),
			"name", cmd.Name,
			"job_id", jobID,
		)
	}

	log.Info("batch registration enqueued", "service", "batch", "enqueued_count", enqueued, "enqueue_failed", enqueueFailed)
	return &sourcing.BatchRegisterResult{
		OK:            true,
		Total:         len(commands),
		Enqueued:      enqueued,
		EnqueueFailed: enqueueFailed,
		Results:       results,
	}
}
