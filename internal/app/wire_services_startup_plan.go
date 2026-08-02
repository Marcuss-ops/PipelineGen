// Package app — wire_services_startup_plan.go (PURE STARTUP-STEP BUILDER,
// July 2026 split).
//
// Split rationale, see wire_services.go header.
//
// This file owns the PURE STARTUP-STEP BUILDER stage. The dependency
// order for boot preconditions (drive-init → qdrant-collection →
// outbox-pool → chrome-pool-prewarm), plus the qdrant-config-mismatch
// gate, plus the CollectionManager-nil gate, all live here. Extracted
// out of WireServices so the order is visible at a glance and adding
// a future Required precondition is a single-edit canonical change.
//
// Why the user's literal stock|voiceover|artlist|images split doesn't
// fit wire_services.go: this entire file is the cross-cutting
// composition-root surface, NOT a per-domain aggregator. The codebase
// is already organized by domain:
//
//   - stock         → wire_stock_pipeline.go (218 LOC)
//   - voiceover     → build_bundles_voiceover.go
//   - artlist       → build_bundles_artlist_*.go (6 sibling files)
//   - images        → chrome-pool-prewarm step (~50 LOC inside THIS file,
//     because it's a Required boot prerequisite, not
//     a domain service)
//
// Per-domain wiring does NOT belong here; the chrome-pool-prewarm step
// is the only image-related block in the file and it lives in this
// sibling because it's a server-lifecycle prerequisite, not an image
// asset service.
//
// Cross-file deps (same package `app`, accessed without explicit import):
//   - validateQdrantIndexerCompatibility (in build_bundles_qdrant_gates.go;
//     consolidates the qdrant config-mismatch gate here, so the
//     orchestration file does NOT call it)
//   - NewComposition / assetsJobs.Broker / Process.CollectionManager
//     (root's typed ports, same package)
//
// godlike/06 SSOT: the dependency order in this file is the canonical
// boot order. Any new Required precondition MUST be appended in the
// appropriate position; Required guarantees abort the entire
// serverLifecycle.Start chain on first-step failure.
package app

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildStartupPlan is the canonical dependency-ordered StartupStep
// builder. Returns the dependency-ordered baseline (drive-init →
// qdrant-collection → outbox-pool → chrome-pool-prewarm) plus the
// jobs.startupPlan append.
//
// Returns a typed error when any precondition fails, so the caller
// can fail-closed with a uniform partialCleanup:
//
//   - validateQdrantIndexerCompatibility gate (qdrant config mismatch
//     across QDRANT-014 / QDRANT-005 / QDRANT-011 / QDRANT-003).
//   - cfg.Qdrant.Enabled but CollectionManager == nil (QDRANT-003
//     requires Client + CollectionManager + IndexWriter + Searcher
//     when qdrant.enabled=true).
//
// Steps NOT enabled are silently skipped (drive-init skipped when
// root.DriveStart is nil, etc.) so the same helper works for
// minimal-mode and full-mode server boots.
func buildStartupPlan(cfg *config.Config, root *wiring.ComposeRoot, jobs *backgroundJobs, log *zap.Logger) ([]StartupStep, error) {
	// PR-QDRANT-CONFIG-MISMATCH-GATE (July 2026): defense-in-depth gate.
	// This is the FOURTH wire site (BUILD_PROCESS is 1st, BUILD_OUTBOX
	// is 2nd, BUILD_BUNDLES_QDRANT_GATES validateQdrantIndexer
	// Compatibility is 3rd, this helper invocations is 4th). godlike/07
	// no-fake-availability: catch the misconfiguration at composition
	// time so the registry-level failover aborts BEFORE EnsureSchema is
	// wired against a half-built Qdrant runtime.
	if err := validateQdrantIndexerCompatibility(cfg); err != nil {
		return nil, err
	}

	var plan []StartupStep

	// Drive folder validation. Required.
	if root != nil && root.DriveStart != nil {
		ds := root.DriveStart
		plan = append(plan, StartupStep{
			Name: "drive-init", Required: true,
			Start: func(ctx context.Context) error {
				return ds()
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// QDRANT-003 (June 2026): qdrant-collection step — creates/validates
	// the versioned Qdrant collection + sets the runtime alias. Required
	// when Qdrant is enabled. Fail-closed gate inside this helper covers
	// the CollectionManager-nil case (the pre-PR4.8 historical error
	// path that surfaced at EnsureSchema time).
	if cfg.Qdrant.Enabled {
		if root.Process == nil || root.Process.CollectionManager == nil {
			return nil, fmt.Errorf("qdrant is enabled but CollectionManager is nil — QDRANT-003 requires Client + CollectionManager + IndexWriter + Searcher when qdrant.enabled=true")
		}
		cm := root.Process.CollectionManager
		plan = append(plan, StartupStep{
			Name: "qdrant-collection", Required: true,
			Start: func(ctx context.Context) error {
				_, err := cm.EnsureSchema(ctx)
				return err
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// Outbox dispatcher pool. Required when present.
	if root != nil && root.OutboxStart != nil {
		os := root.OutboxStart
		plan = append(plan, StartupStep{
			Name: "outbox-pool", Required: true,
			Start: func(ctx context.Context) error {
				return os()
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// PR-CHROME-POOL-WARM-AT-BOOT (July 2026): the Slides image worker
	// pool warmup used to be fired by a readiness probe (lazily on the
	// first /ready poll). That allowed the HTTP listener to accept
	// traffic against a cold / partially-warm pool, and added the full
	// browser-launch + login + slides.new latency to the FIRST image
	// generation request after boot.
	//
	// Prewarm synchronously, but keep the capability optional at global boot.
	// Image generation is a per-job provider policy: an unavailable Google
	// Slides backend must fail the generation job closed, while Artlist and
	// internet_images remain usable. Making this step globally Required would
	// incorrectly take unrelated VidRush providers offline.
	// Falls into the canonical order:
	//
	//   drive-init → qdrant-collection → outbox-pool
	//   → chrome-pool-prewarm    [OPTIONAL, only when ImagesEnabled]
	//   → background services (scanner, monitor, sweepers)
	//   → job runner (always last)
	//
	// Per-worker profile isolation is already enforced by session.py
	// (MASTER_STORAGE.profile_<id> + PROFILE_DIR_<id>_<pid>); this
	// step just guarantees those workers actually exist server-side
	// before requests can reach them.
	if root != nil && root.Domains != nil && root.Domains.ImageService != nil && cfg.Features.ImagesEnabled {
		imgSvc := root.Domains.ImageService
		poolSize := cfg.Concurrency.MaxConcurrentGoogleSlidesGenerations
		plan = append(plan, StartupStep{
			Name: "chrome-pool-prewarm", Required: false,
			Start: func(ctx context.Context) error {
				log.Info("StartupStep: prewarming ChromeImageProviderPool", zap.Int("pool_size", poolSize))
				imgSvc.TriggerPrewarm(ctx, "startup-prewarm", poolSize)

				report := imgSvc.Diagnostics()
				if !report.ImageGenWired {
					return fmt.Errorf("chrome image provider pool is not wired")
				}
				if !report.ImageGenHealthy {
					return fmt.Errorf("chrome image provider pool is unhealthy")
				}
				if report.ImageGenCooldownProfiles > 0 {
					return fmt.Errorf("chrome image provider pool has %d unhealthy/cooldown profiles", report.ImageGenCooldownProfiles)
				}
				log.Info("StartupStep: ChromeImageProviderPool prewarmed successfully and healthy", zap.Int("pool_size", poolSize))
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// Append the background services plan (scanner, monitor, sweepers, etc.)
	// followed by the job runner (always last, required). jobs.startupPlan
	// is captured by startBackgroundJobs in wire_services_composition.go.
	if jobs != nil {
		plan = append(plan, jobs.startupPlan...)
	}

	return plan, nil
}
