// Package app — artifact_finalize bundle construction (FASE 3 Spina
// Dorsale Push 3.1d, July 2026).
//
// Extracted from composition.go per PG-028 (the established
// `internal/app/build_bundles_<capability>.go` pattern). This file
// owns the SOLE canonical construction site for the
// artifact_finalize.Finalizer service. No caller (publisher worker
// pool, admin tool, test stub) should construct a second instance
// or call the artifact.Repository Mark* primitives directly — every
// consumer reaches the typed port via wiring.ComposeRoot.Finalizer.
//
// godlike/06 SSOT: this is the SINGLE canonical wiring of the
// FASE 3 application-layer finalization step. The composition
// root (composition.go::NewComposition) calls BuildArtifactFinalizeBundle
// immediately AFTER BuildStagingBundle because the Finalizer consumes
// the SAME artifact.Repository instance that wiring.StagingBundle.Repository
// exposes (no second DB lookup; the typed port is the canonical
// cursor to the same *artifactstages.Repository concrete).
//
// godlike/07 fail-closed:
//   - The constructor fails closed on a nil staging.Repository
//     (composition-time misconfiguration: caller forgot to wire
//     BuildStagingBundle BEFORE BuildArtifactFinalizeBundle, or
//     stagingBundle was passed as nil). The error message names
//     the missing dependency so a future debug session can
//     pinpoint the call-order issue without inspecting the
//     bundle contract.
//   - artifactfinalize.NewFinalizerService itself fail-closes on
//     nil repo + nil log (the canonical pre-flight check at the
//     boundary; this constructor wraps the boundary error in a
//     "build artifact_finalize:" envelope for log-greppers).
package app

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	"go.uber.org/zap"

	artifactfinalize "github.com/Marcuss-ops/PipelineGen/internal/application/artifact_finalize"
)

// BuildArtifactFinalizeBundle constructs the canonical FASE 3
// Spina Dorsale finalizer bundle: the application-layer
// finalizerService (the typed port that closes the FASE 3 saga's
// Finalize step by scanning ListByJob + flipping PUBLISHED →
// SUCCEEDED via the fenced MarkSucceeded primitive, gated on
// "all REQUIRED are PUBLISHED"). Returns the wiring.FinalizerBundle
// for assignment to wiring.ComposeRoot.Finalizer.
//
// Dependency surface (intentionally minimal — 2 deps):
//   - staging.Repository: the canonical artifact.Repository port
//     exposed on wiring.StagingBundle. The Finalizer's read path
//     (ListByJob) + write path (MarkSucceeded, fenced-CAS) both
//     consume this port, so sharing the same instance is a
//     godlike/06 SSOT requirement (the artifact_stages table has
//     exactly one canonical single-writer). BuildArtifactFinalizeBundle
//     MUST run AFTER BuildStagingBundle in NewComposition — the
//     constructor validates staging.Repository != nil at the
//     boundary and aborts otherwise.
//   - log: *zap.Logger for the boot-time wiring log line + the
//     per-Finalize info log line + the
//     `finalize: no stages found for job_id` debug log line
//     (the audit-trail contract).
//
// Returns error on (a) nil staging.Repository (composition-time
// misconfiguration) or (b) artifactfinalize.NewFinalizerService
// rejection (nil repo or nil log underneath — the boundary check
// re-runs as a defense-in-depth). No log is emitted on error
// path — NewComposition wraps the error and the orchestrator
// aborts startup.
func BuildArtifactFinalizeBundle(staging *wiring.StagingBundle, log *zap.Logger) (*wiring.FinalizerBundle, error) {
	// Step 1 (godlike/07 fail-fast at construction):
	// pre-validate the staging bundle dependency. A nil
	// Repository signals that BuildStagingBundle did NOT run
	// (or returned nil) — composition-order misconfiguration.
	// The error message names the missing dependency so a
	// future debug session can pinpoint the call-order issue
	// without inspecting the bundle contract.
	if staging == nil || staging.Repository == nil {
		return nil, fmt.Errorf("build artifact_finalize: staging.Repository is nil (BuildStagingBundle must run first in NewComposition)")
	}

	// Step 2: construct the application-layer finalizerService.
	// artifactfinalize.NewFinalizerService fail-closes on nil
	// repo (impossible — staging.Repository is non-nil above)
	// or nil log (impossible — log is non-nil at the
	// composition root). The boundary check is a defense-in-
	// depth double-check.
	finalizer, err := artifactfinalize.NewFinalizerService(staging.Repository, log)
	if err != nil {
		return nil, fmt.Errorf("build artifact_finalize: NewFinalizerService: %w", err)
	}

	log.Info("artifact_finalize bundle wired (FASE 3 Spina Dorsale Push 3.1d)",
		zap.String("repository_table", "artifact_stages"),
	)

	return &wiring.FinalizerBundle{
		Finalizer: finalizer,
	}, nil
}
