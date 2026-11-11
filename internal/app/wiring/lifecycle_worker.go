// Package app — lifecycle worker capability (PR-LIFECYCLE-SPLIT-BY-CAPABILITY, July 2026).
//
// Extracted from internal/app/go per AGENTS.md Pattern 5
// (capability-stable file split). Owns the worker-mode startup steps:
//
//   - job-scanner            (sqlitejobs.Scanner ticker)
//   - metrics-refresher      (appjobs.StartMetricsRefresher)
//   - voiceover-parent-aggregator (voiceoverjobs.NewParentAggregator)
//   - script-parent-aggregator   (scriptjobs.NewScriptParentAggregator)
//
// Sister file to lifecycle_scheduler.go + lifecycle/maintenance.go
// (the 3 capability files) + lifecycle_adapters.go (composition-root
// adapters). Sister file RETAINED from prior waves:
// lifecycle/jobrunner.go (the job-runner step builder).
//
// The orchestrator (go::startBackgroundJobs) calls
// buildWorkerSteps when mode is "all" or "worker". The returned
// steps are appended to the startup plan BEFORE the scheduler +
// maintenance steps + the job-runner step.
package wiring

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// clipRenderParentAggregator returns the process-wide family aggregator,
// caching it on ComposeRoot so the worker notifier and recovery sweeper share
// the same idempotent finalizer.
func clipRenderParentAggregator(root *ComposeRoot, log *zap.Logger) *cliprender.ParentAggregator {
	if root == nil {
		return nil
	}
	if root.ClipRenderParentAggregator != nil {
		return root.ClipRenderParentAggregator
	}
	if root.Jobs == nil || root.Jobs.Service == nil {
		return nil
	}
	root.ClipRenderParentAggregator = cliprender.NewParentAggregator(
		root.Jobs.Service, log, cliprender.DefaultParentAggregationInterval)
	return root.ClipRenderParentAggregator
}

// EnvWarmModels is the optional comma-separated startup warm list
// (PIPELINEGEN_WARM_MODELS). The default warm step loads ONE model —
// cfg.External.OllamaModel — but production plans resolve between two sizes
// (gemma4:e2b for short scripts, gemma4:e4b for long ones), so a batch that
// switches size pays a full model load on its first job of the other size.
// Listing both models makes startup pay both loads once. Residency across
// both depends on the Ollama server's VRAM capacity: with a single resident
// slot the last listed model stays loaded.
const EnvWarmModels = "PIPELINEGEN_WARM_MODELS"

// startupWarmModels resolves the models the warm step loads: the env list
// when set, the configured single model otherwise. Entries are trimmed, the
// "auto" alias is dropped (the warm contract needs a concrete model), empty
// entries are skipped and duplicates deduplicated preserving order.
func startupWarmModels(configured string) []string {
	configured = strings.TrimSpace(configured)
	raw := strings.TrimSpace(os.Getenv(EnvWarmModels))
	if raw == "" {
		if configured == "" {
			return nil
		}
		return []string{configured}
	}
	out := make([]string, 0, 2)
	seen := make(map[string]bool)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" || entry == "auto" || seen[entry] {
			continue
		}
		seen[entry] = true
		out = append(out, entry)
	}
	if len(out) == 0 && configured != "" {
		return []string{configured}
	}
	return out
}

// buildOllamaWarmStep makes model residency a startup contract for worker
// mode. A job runner must not accept expensive generation work while the
// first request is still paying the model load. WarmModel owns the /api/ps
// check, singleflight and post-warm residency verification.
func buildOllamaWarmStep(cfg *config.Config, root *ComposeRoot, log *zap.Logger) *StartupStep {
	if cfg == nil || root == nil || root.AI == nil || root.AI.OllamaClient == nil {
		return nil
	}
	configured := strings.TrimSpace(cfg.External.OllamaModel)
	if configured == "" {
		configured = root.AI.OllamaClient.Model()
	}
	models := startupWarmModels(configured)
	if len(models) == 0 {
		return nil
	}
	return &StartupStep{
		Name:     "ollama-model-warm",
		Required: true,
		Start: func(startCtx context.Context) error {
			warmTimeout := time.Duration(cfg.External.OllamaWarmTimeoutSeconds) * time.Second
			if warmTimeout <= 0 {
				warmTimeout = 10 * time.Minute
			}
			ctx, cancel := context.WithTimeout(startCtx, warmTimeout)
			defer cancel()
			started := time.Now()
			for _, model := range models {
				if err := root.AI.OllamaClient.WarmModel(ctx, model); err != nil {
					return fmt.Errorf("warm Ollama model %q: %w", model, err)
				}
			}
			if log != nil {
				log.Info("StartupStep: Ollama model resident",
					zap.Strings("models", models),
					zap.Duration("warm_timeout", warmTimeout),
					zap.Int64("warm_ms", time.Since(started).Milliseconds()),
				)
			}
			return nil
		},
		Stop: func(context.Context) error { return nil },
	}
}
