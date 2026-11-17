// Package app — wire_services_startup_plan.go (PURE STARTUP-STEP BUILDER,
// July 2026 split).
//
// Split rationale, see wire_services.go header.
//
// This file owns the PURE STARTUP-STEP BUILDER stage. The dependency
// order for boot preconditions (drive-init → pgvector-collection →
// outbox-pool → chrome-pool-prewarm), plus the pgvector-config-mismatch
// gate, plus the CollectionManager-nil gate, all live here. Extracted
// out of WireServices so the order is visible at a glance and adding
// a future Required precondition is a single-edit canonical change.
//
// Why the user's literal stock|voiceover|images split doesn't
// fit wire_services.go: this entire file is the cross-cutting
// composition-root surface, NOT a per-domain aggregator. The codebase
// is already organized by domain:
//
//   - stock         → wire_stock_pipeline.go (218 LOC)
//   - voiceover     → build_bundles_voiceover.go
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
//   - validateIndexerCompatibility (in the retired indexer gates file;
//     consolidates the pgvector config-mismatch gate here, so the
//     orchestration file does NOT call it)
//   - NewComposition / assetsJobs.Broker / Process.CollectionManager
//     (root's typed ports, same package)
//
// godlike/06 SSOT: the dependency order in this file is the canonical
// boot order. Any new Required precondition MUST be appended in the
// appropriate position; Required guarantees abort the entire
// serverLifecycle.Start chain on first-step failure.
package wiring

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildStartupPlan is the canonical dependency-ordered StartupStep
// builder. Returns the dependency-ordered baseline (drive-init →
// pgvector-collection → outbox-pool → chrome-pool-prewarm) plus the
// jobs.startupPlan append.
//
// Returns a typed error when any precondition fails, so the caller
// can fail-closed with a uniform partialCleanup:
//
//   - validateIndexerCompatibility gate (pgvector config mismatch
//     across VECTOR-014 / VECTOR-005 / VECTOR-011 / VECTOR-003).
//   - cfg.pgvector.Enabled but CollectionManager == nil (VECTOR-003
//     requires Client + CollectionManager + IndexWriter + Searcher
//     when pgvector.enabled=true).
//
// Steps NOT enabled are silently skipped (drive-init skipped when
// root.DriveStart is nil, etc.) so the same helper works for
// minimal-mode and full-mode server boots.
func buildStartupPlan(cfg *config.Config, root *ComposeRoot, jobs *backgroundJobs, log *zap.Logger) ([]StartupStep, error) {
	// PR-VECTOR-CONFIG-MISMATCH-GATE (July 2026): defense-in-depth gate.
	// This is the FOURTH wire site (BUILD_PROCESS is 1st, BUILD_OUTBOX
	// is 2nd, BUILD_BUNDLES_VECTOR_GATES validateIndexer
	// Compatibility is 3rd, this helper invocations is 4th). godlike/07
	// no-fake-availability: catch the misconfiguration at composition
	// time so the registry-level failover aborts BEFORE EnsureSchema is
	// wired against a half-built legacy vector runtime.
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
	// internet_images remain usable. Making this step globally Required would
	// incorrectly take unrelated VidRush providers offline.
	// Falls into the canonical order:
	//
	//   drive-init → pgvector-collection → outbox-pool
	//   → background services (scanner, monitor, sweepers)
	//   → job runner (always last)
	//
	// Retrieved images do not require a browser worker. AI/Chrome image
	// generation is deliberately not part of this startup plan.
	// Append the background services plan (scanner, monitor, sweepers, etc.)
	// followed by the job runner (always last, required). jobs.startupPlan
	// is captured by startBackgroundJobs in wire_services_composition.go.
	if jobs != nil {
		plan = append(plan, jobs.startupPlan...)
	}

	return plan, nil
}
