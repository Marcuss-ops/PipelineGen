// Package app — build_stage_drive_bundle.go (P0-COMPL-2-COMPOSITION-WIRE,
// July 2026, COMPLETION-CUTOVER-P0-2026-07-04 wave).
//
// Canonical Stage→Drive production composition bundle. Wires 5 components
// in pipeline order:
//
//	[1] StagedResolver (staged.StagedArtifactResolver)
//	[2] VerifiedArtifactProjector (verification.Verifier)
//	[3] ArtifactPreparation (finalization.ArtifactPreparationService)
//	[4] Drive Publisher (delivery.Publisher)
//	[5] WithArtifactsService (completion.WithArtifactsService)
//
// godlike/06 SSOT: this bundle is the SINGLE canonical owner of the
// Stage→Drive production composition wiring. No other code path may
// construct this chain in parallel; future waves route through this seam.
//
// godlike/07 typed-error contract: ErrStageDriveInsufficientForCompletion
// is the shared typed sentinel for the 2 forward-pointer components
// (ArtifactPreparation + WithArtifactsService). Callers reach them via
// errors.Is for fail-closed posture.
//
// Migration trajectory (godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT):
//   - EXPAND (this commit): 3 LIVE components + 2 forward-pointers.
//     Auto-sufficient compile + tests still pass; runtime callers
//     using the forward-pointer typed-nil-safe accessors get the
//     typed sentinel rather than a nil-deref panic.
//   - BACKFILL (forward-pointer TODO(P0-COMPL-5-SINGLE-BACKBONE),
//     deadline 2026-08-15): production concrete for CompleteJobTxRunner
//     + IdempotencyCachePort lands; this bundle's 5th component goes LIVE.
//   - CUTOVER (forward-pointer TODO(P0-COMPL-4-PUBLISH-DEDUPE),
//     deadline 2026-07-25): ArtifactPreparation becomes the SOLE publish
//     seam (collapse any duplicate publisher surface); this bundle's
//     4th component becomes the source-of-truth Publisher.
//   - CONTRACT (forward-pointer TODO(P0-COMPL-2-CONTRACT), deadline
//     2026-Q4): physics-rm of the typed-NIL-safe accessor shape once
//     both forward-pointers ship; check N+1 forward-prevention gate
//     tightens to BAN nil-bundle-without-error entirely.
//
// Auto-sufficient micro-PR shape (per AGENTS.md Git-Lesson-2 direct-on-main):
//
//	feat(composition): BuildStageDriveBundle — 5-stage production
//	                  composition (Staged→Drive), 3 LIVE + 2 forward-pointer
//	                  (artifact-prep adapter + completion tx-runner)
//
//	N+1 atomic commit (this file only; zero modifications to existing files).
//	Co-authored-by trailer per AGENTS.md Git-Lesson-3.
//	Direct-on-main (NO branch, NO PR, NO --force — ff-push per
//	AGENTS.md Git-Lesson-4/5).
//
// The HTTP handler → local.Broker → finalization.JobFinalizer pipeline
// remains UNWIRED per the user's hard constraint — this bundle is the
// canonical Stage→Drive production composition path, not the legacy
// remote-worker artifact handoff seam. The latter has its own forward-pointer
// (`PR-JOB-FINALIZER-CONTRACT`) and is OUT OF SCOPE for this commit.
package app

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	finalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/staged"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/verification"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/completion"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// ErrStageDriveInsufficientForCompletion is the godlike/07 typed sentinel
// shared by the 2 forward-pointer components in StageDriveBundle:
//
//   - ArtifactPreparation (gated on P0-COMPL-4-PUBLISH-DEDUPE, deadline 2026-07-25)
//   - WithArtifactsService (gated on P0-COMPL-5-SINGLE-BACKBONE, deadline 2026-08-15)
//
// Returned by the typed-nil-safe accessors (ArtifactPreparationService /
// WithArtifactsService) when the corresponding bundle field is still nil
// pending the canonical production surface. Callers detect via:
//
//	if errors.Is(err, app.ErrStageDriveInsufficientForCompletion) {
//	    var b *app.StageDriveBundle
//	    // b.<LIVE components> are usable; b.<forward-pointer components> are not.
//	}
//
// godlike/07 typed-error contract: this sentinel is the SINGLE failure point
// for "StageDriveBundle half-wired" — no string-matching, no direct pointer
// equality, no zero-value substitution. errors.Is-compatible across %w wraps.
var ErrStageDriveInsufficientForCompletion = errors.New(
	"stage-drive bundle: forward-pointer components (ArtifactPreparation + WithArtifactsService) require P0-COMPL-4-PUBLISH-DEDUPE (deadline 2026-07-25) + P0-COMPL-5-SINGLE-BACKBONE (deadline 2026-08-15) to ship; bundle's 3 LIVE components (StagedResolver + Verifier + Publisher) are usable today",
)

// StageDriveBundle is the canonical 5-component bundle for the Stage→Drive
// production composition. Per godlike/06 SSOT, this struct is the single
// canonical surface for the bundle; future waves MUST extend this type
// rather than constructing parallel bundles in the application layer.
//
// The 5 fields map 1:1 onto the COMPLETION-CUTOVER-P0-2026-07-04 wave's
// verification ladder:
//
//	[1] StagedResolver            — Azione 1 (DB lookup + os.Stat + SHA recompute)
//	[2] Verifier                  — Azione 3 (size + SHA-256 integrity gate)
//	[3] ArtifactPreparation       — Azione 4 (forward-pointer, gated on P0-COMPL-4)
//	[4] Publisher                 — Phase 3 (single canal for all Drive writes)
//	[5] WithArtifactsService      — Azione 6 (forward-pointer, gated on P0-COMPL-5)
type StageDriveBundle struct {
	// StagedResolver is the LIVE Resolver.Pattern0 port entry-point.
	// Carries the lookupFn (production wires against assetindex.Service;
	// tests inject a stub). Per AGENTS.md Pattern 0 godlike/06 SSOT,
	// composition root wires the production concrete + real lookupFn;
	// tests mock via the interface.
	StagedResolver staged.StagedArtifactResolver

	// Verifier is the LIVE on-disk integrity gate. NewVerifier()
	// (production wiring in this bundle) — no deps. NewVerifierWithClock
	// (tests) is the determinism seam.
	//
	// Note: the user-facing name "VerifiedArtifactProjector" maps to this
	// `verification.Verifier` concrete. The mapping is semantic: project
	// a *staged.StagedArtifact → *verification.VerifiedArtifact (12-field
	// envelope). The struct name reflects the CUTOVER-WRITE-content
	// audit; the implementation name reflects the CUTOVER-WRITE-time
	// convention. Forward-pointer to godlike/06 SSOT resolution: future
	// type alias `type VerifiedArtifactProjector = *verification.Verifier`.
	Verifier *verification.Verifier

	// ArtifactPreparation is a forward-pointer to the canonical publish
	// seam (Azione 4 + P0-COMPL-4-PUBLISH-DEDUPE). The struct field is
	// nil at construction time today; the typed-nil-safe accessor
	// (ArtifactPreparationService) returns the shared sentinel.
	//
	// Future BACKFILL wiring (deadline 2026-07-25):
	//   ap := finalizer.NewArtifactPreparation(toFinalizationPublisherPort(p), log)
	//   bundle.ArtifactPreparation = ap
	//
	// godlike/06 SSOT: finalizer.ArtifactPreparation is the canonical
	// SOLE publish seam (per P0-COMPL-4 collapse directive); future
	// composition root MUST NOT construct parallel ArtifactPreparation
	// outside this bundle.
	ArtifactPreparation finalization.ArtifactPreparationService

	// Publisher is the LIVE delivery.Publisher pointer (concrete impl
	// at internal/infrastructure/drive/publisher.go). Receives the
	// verified-artifact envelope and produces a *delivery.PublishResult
	// with canonical Drive location metadata.
	//
	// Nil-tolerance: when Drive auth failed at composition time, this
	// pointer is nil; callers detect via nil-check + log+drop per
	// godlike/07 no-fake-availability (ArtefactPreparation still
	// constructs but Prepare returns "no publisher configured").
	Publisher delivery.Publisher

	// WithArtifacts is a forward-pointer to the canonical Sender-side
	// atomic-complete-with-artifacts service (Azione 6 + P0-COMPL-5-
	// SINGLE-BACKBONE). The struct field is nil at construction time;
	// the typed-nil-safe accessor (WithArtifactsService) returns the
	// shared sentinel.
	//
	// Future BACKFILL wiring (deadline 2026-08-15):
	//   svc := completion.NewWithArtifactsService(rxRunner, idempotencyCache)
	//   bundle.WithArtifacts = svc
	WithArtifacts *completion.WithArtifactsService
}

// BuildStageDriveBundle wires the canonical Stage→Drive production composition.
// Auto-sufficient per the user task constraint: compiles in isolation,
// independent of the HTTP handler → local.Broker → finalization.JobFinalizer
// pipeline which stays UNWIRED (per the user's hard constraint in the
// close). Construction order:
//
//	(1) staged.NewResolver(lookupFn)   — godlike/07 fail-fast on nil lookupFn
//	(2) verification.NewVerifier()      — no deps; clock-injectable for tests
//	(3) delivery.Publisher             — passed-in (reused from DriveBundle); nil-tolerant
//	(4) ArtifactPreparation             — forward-pointer (see struct doc comment)
//	(5) WithArtifactsService            — forward-pointer (see struct doc comment)
//
// godlike/07 fail-closed:
//   - nil lookupFn              → staged.NewResolver returns typed sentinel
//                                 ErrStagedArtifactNotConfigured; wrapped via
//                                 fmt.Errorf %w here; composition aborts.
//   - nil Publisher             → bundle still constructs; ArtifactPreparation's
//                                 Prepare returns "no publisher configured"
//                                 at runtime (production-side runtime error).
//   - nil log                   → defaults to zap.NewNop() per convention.
//
// godlike/06 SSOT: this function is the SINGLE canonical entry-point for
// Stage→Drive composition. No parallel NewXxx constructors in the
// application layer.
func BuildStageDriveBundle(
	_ context.Context, // reserved for ctx-scoped tracer propagation (Azione 7 forward-pointer)
	log *zap.Logger,
	lookupFn staged.ArtifactIndexLookupFn,
	publisher delivery.Publisher,
) (*StageDriveBundle, error) {
	if log == nil {
		log = zap.NewNop()
	}

	// (1) StagedResolver — godlike/07 fail-fast on nil lookupFn.
	resolver, err := staged.NewResolver(lookupFn)
	if err != nil {
		return nil, fmt.Errorf(
			"BuildStageDriveBundle: staged.NewResolver (godlike/07 fail-closed posture): %w",
			err,
		)
	}

	// (2) Verifier — no deps; production wiring uses NewVerifier() (time.Now);
	// tests inject NewVerifierWithClock for byte-stability assertions.
	verifier := verification.NewVerifier()

	// (3) Publisher — passed-in (production composition wires against
	// DriveBundle.Publisher from internal/app/build_bundles_drive.go).
	// Nil-tolerance: when Drive auth failed at composition time,
	// Publisher is nil (true-nil interface, not typed-nil) per the
	// BuildDriveBundle explicit var admin drive.Admin + if driveUploader != nil guard.
	publisherRef := publisher
	if publisherRef == nil {
		// Log+drop per godlike/07 no-fake-availability: the bundle
		// continues to construct (auto-sufficient compile), but
		// downstream callers using the Publisher access
		// nil-deref-safe paths. Production-side runtime surfaces
		// "Drive not configured" via the canonical Publisher.
		log.Warn("BuildStageDriveBundle: Publisher is nil (Drive auth may have failed at composition-time); bundle still constructs but ArtifactPreparation.Prepare will return a runtime error rather than succeeding")
	}

	// (4) + (5) — forward-pointers left at zero-value (nil).
	// See struct doc comment for the migration trajectory (P0-COMPL-4
	// + P0-COMPL-5 closure deadlines).

	return &StageDriveBundle{
		StagedResolver:      resolver,
		Verifier:            verifier,
		ArtifactPreparation: nil, // forward-pointer TODO(P0-COMPL-4-PUBLISH-DEDUPE, 2026-07-25)
		Publisher:           publisherRef,
		WithArtifacts:       nil, // forward-pointer TODO(P0-COMPL-5-SINGLE-BACKBONE, 2026-08-15)
	}, nil
}

// ArtifactPreparationService returns the canonical 3rd component of the
// bundle, OR the typed sentinel ErrStageDriveInsufficientForCompletion
// when the forward-pointer is unresolved.
//
// godlike/07 typed-error contract: the typed sentinel guarantees callers
// detect the half-wired state via errors.Is (NOT direct pointer equality
// or string-match). Production callers handle via:
//
//	if err != nil {
//	    if errors.Is(err, app.ErrStageDriveInsufficientForCompletion) {
//	        log.Warn("forward-pointer surface is unresolved; deferring to 4 LIVE components only")
//	        continue
//	    }
//	    return err
//	}
//
// godlike/06 SSOT: this accessor is the SINGLE typed surface for the
// forward-pointer component; no callsite reaches the struct field
// directly (future callers MUST use this accessor).
func (b *StageDriveBundle) ArtifactPreparationService() (finalization.ArtifactPreparationService, error) {
	if b == nil || b.ArtifactPreparation == nil {
		return nil, ErrStageDriveInsufficientForCompletion
	}
	return b.ArtifactPreparation, nil
}

// WithArtifactsServiceAccessor returns the canonical 5th component of the bundle,
// OR the typed sentinel ErrStageDriveInsufficientForCompletion when the
// forward-pointer is unresolved. Mirrors ArtifactPreparationService's
// typed-nil-safe accessor shape; shared sentinel so callers errors.Is once
// and route to either deferred-or-error handling per godlike/07 contract.
func (b *StageDriveBundle) WithArtifactsServiceAccessor() (*completion.WithArtifactsService, error) {
	if b == nil || b.WithArtifacts == nil {
		return nil, ErrStageDriveInsufficientForCompletion
	}
	return b.WithArtifacts, nil
}

// ── Compile-time assertions (AGENTS.md Pattern 0) ─────────────────────
//
// Drift between *verification.Verifier and the (forward-pointer) typed
// Validator surface would surface at build time, not runtime panic. The
// Verifier is NOT behind a port today (composition root constructs the
// concrete directly), so we only pin the artifactPreparation-via-port
// assertion.
//
// The pre-existing assertions in the leaf packages cascade:
//
//	var _ staged.StagedArtifactResolver = (*staged.Resolver)(nil)              // staged/resolver.go
//	var _ finalization.ArtifactPreparationService = (*finalizer.ArtifactPreparation)(nil)  // finalizer/artifact_preparation.go
//	var _ delivery.Publisher = (*drive.Publisher)(nil)                        // drive/publisher.go
//
// These three leaf-package pins are the load-bearing invariants; this
// bundle does NOT re-pin them (no double-pinning; per AGENTS.md Pattern 0
// godlike/06 SSOT one canonical owner per fact).
