// Package batch — the BatchRegistrar use case extracted from the historical
// sourcing.Service.BatchRegisterFromYouTube body (P0-1 / commit 2, June 2026).
//
// Per AGENTS.md Pattern 0 (port abstraction) + Pattern 5 (one concept per file):
// the BatchRegistrar owns the per-batch YouTube flow as a focused service
// with 2 narrow deps (the YouTubeRegistrar interface + Logger). The façade
// sourcing.Service.BatchRegisterFromYouTube delegates to *Service.BatchRegister
// for backward compatibility.
//
// Sub-package construction is *Service.NewService(yt, log) — see
// internal/app/assets_register_sourcing.go for wiring.
package batch

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
)

// Service is the BatchRegistrar implementation. 2-port budget per
// architecture/policy.yaml::max_constructor_deps (well under the 8 cap).
type Service struct {
	yt  sourcing.YouTubeRegistrar
	log sourcing.Logger
}

// NewService creates a BatchRegistrar service. yt is REQUIRED (batch wraps
// the YouTubeRegistrar's Register method per clip).
func NewService(yt sourcing.YouTubeRegistrar, log sourcing.Logger) *Service {
	return &Service{yt: yt, log: log}
}

// BatchRegister processes a batch of clip registration commands sequentially.
// For each clip it calls YouTubeRegistrar.Register and aggregates the results.
// This is the canonical service-level orchestrator — handlers call this
// single method instead of looping over clips themselves.
//
// Behaviour mirrors the historical sourcing.Service.BatchRegisterFromYouTube
// pre-commit-2 of P0-1 (test-fixture nil-svc path returns OK:false + all-failed
// batch result so handlers can surface the compose-time bug without panic).
func (s *Service) BatchRegister(ctx context.Context, commands []sourcing.RegisterClipCommand) *sourcing.BatchRegisterResult {
	if s == nil || s.yt == nil {
		return &sourcing.BatchRegisterResult{
			OK:      false,
			Total:   len(commands),
			Failed:  len(commands),
			Results: make([]sourcing.BatchClipResult, len(commands)),
		}
	}

	log := s.log
	results := make([]sourcing.BatchClipResult, len(commands))
	var succeeded, failed int

	log.Info("starting batch registration", "service", "batch", "clips", len(commands))
	for i, cmd := range commands {
		res, err := s.yt.Register(ctx, cmd)
		br := sourcing.BatchClipResult{Name: cmd.Name}
		if err != nil {
			br.Error = err.Error()
			results[i] = br
			failed++
			log.Info("batch clip processed",
				"index", i+1,
				"total", len(commands),
				"name", cmd.Name,
				"ok", false,
				"error", err.Error(),
			)
			continue
		}
		if res == nil {
			br.Error = "empty registration result"
			results[i] = br
			failed++
			continue
		}
		br.OK = res.OK
		br.ClipID = res.ClipID
		br.Duplicate = res.Duplicate
		if res.Duplicate {
			br.OK = false
		}
		if !res.OK && res.Message != "" {
			br.Error = res.Message
		}
		results[i] = br
		if br.OK || br.Duplicate {
			succeeded++
		} else {
			failed++
		}
		log.Info("batch clip processed",
			"index", i+1,
			"total", len(commands),
			"name", cmd.Name,
			"ok", br.OK,
			"duplicate", br.Duplicate,
			"error", br.Error,
		)
	}

	log.Info("batch registration completed", "service", "batch", "succeeded", succeeded, "failed", failed)
	return &sourcing.BatchRegisterResult{
		OK:        true,
		Total:     len(commands),
		Succeeded: succeeded,
		Failed:    failed,
		Results:   results,
	}
}
