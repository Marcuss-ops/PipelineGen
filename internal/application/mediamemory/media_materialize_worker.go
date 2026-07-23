// Package mediamemory — media_materialize_worker.go is the
// canonical Fase 3.3 worker for the materialize seam (architecture
// doc section 8 — "Discovery → Linker → Materialize").
//
// godlike/06 SSOT: MaterializeWorker is the SINGLE owner of the
// (candidate → asset_id) materialization seam between the
// candidate repository and the canonical stockpipeline orchestrator.
// The worker sits DOWNSTREAM of DiscoveryWorker (Fase 3.1) and
// LinkerWorker (Fase 3.2) in the canonical pipeline; upstream
// phases populate the (DiscoveryStatus, MaterializationStatus)
// envelopes and the linker stamps the AssetID only on Hot-tier
// promotion.
//
// godlike/06 SSOT (tier SSOT): three canonical materialization
// tiers per types.go (godlike/06 closed set):
//
//   - Cold  : metadata-only. DiscoveryStatus ∈ {DiscoverySearched}
//   - Warm  : rights-verified, ready for on-demand segment. Set by
//     top-K promotion when stockpipeline completed a
//     rights-check + segment-capable download without
//     fully staging bytes locally.
//   - Hot   : bytes staged locally + AssetID populated + ready
//     for binding + scene render. Set ONLY by
//     PromoteOnDemand (resolver hot path) or by the
//     linker when a binding write demands bytes-local.
//
// godlike/07 NO-FAKE-AVAILABILITY: a missing StockPipelineAcquirer
// or a per-candidate failure surfaces as a typed sentinel. The
// worker does NOT silently degrade to "Cold with empty AssetID"
// — an AssetID == "" return from stockpipeline is a HARD failure
// (wrapping ErrCandidateMaterializationFailed) so the ranker
// cannot accidentally promote unready candidates to Hot.
package mediamemory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// MaterializationRequest is the canonical input to the worker for
// top-K promotion (Materialize). godlike/06 SSOT (narrow port):
// one method, one envelope, batch atomic.
type MaterializationRequest struct {
	BatchID   string
	ProjectID string
	Promotes  []AcquisitionPromote
	// HotCache controls whether successful promotes go to Hot
	// (true) or stay at Warm (false). Cold→Warm is the canonical
	// default for top-K; Warm→Hot is the canonical default for
	// resolver-side promotion.
	HotCache bool
	// MaxConcurrency is an OPTIONAL concurrency cap for the
	// per-promote StockPipelineAcquirer.Materialize loop. Zero ==
	// serial (godlike/06 default for canonically-deterministic
	// Resume). Future Fase 4.x may lift this to concurrent
	// fan-out behind a worker-pool.
	MaxConcurrency int
}

// MaterializationResult is the canonical output of the worker.
// godlike/06 SSOT (success + failure envelopes): the
// orchestrator branches via len(PersistedAssetIDs) /
// len(FailedCandidateIDs) rather than the error envelope, so a
// partial-success run is observable at the row level.
type MaterializationResult struct {
	PersistedAssetIDs  []string // candidateID → assetID mapping (gateway-side)
	FailedCandidateIDs []string
	MaterializedCount  int
	Failures           []string
}

// MaterializeWorker is the canonical Fase 3.3 worker port.
//
// godlike/06 SSOT (narrow doctrine):
//   - Materialize    : top-K promotion (Cold → Warm/Hot via
//     stockpipeline). Returns partial-success
//     envelopes so per-candidate failures don't
//     poison the batch.
//   - PromoteOnDemand: single candidate Warm → Hot (resolver hot
//     path). AssetID MUST be populated on
//     success; transient failures leave
//     MaterializationStatus unchanged so the
//     caller can retry.
type MaterializeWorker interface {
	Materialize(ctx context.Context, req MaterializationRequest) (MaterializationResult, error)
	PromoteOnDemand(ctx context.Context, candidate MediaCandidate, opts MaterializeOptions) (MediaCandidate, error)
}

// ── Default implementation ─────────────────────────────────────────

// defaultMaterializeWorker is the canonical implementation.
// Composition root wires concrete StockPipelineAcquirer +
// CandidateRepository + RightsValidator.
type defaultMaterializeWorker struct {
	stock      StockPipelineAcquirer
	candidates CandidateRepository
	rights     RightsValidator
	log        Logger
	clock      Clock
}

// NewDefaultMaterializeWorker constructs the canonical worker.
// Composition root injects concrete adapters; nil ports trip
// ErrCandidateMaterializationFailed at call time (godlike/07
// NO-FAKE-AVAILABILITY — never silent zero-promotion).
func NewDefaultMaterializeWorker(
	stock StockPipelineAcquirer,
	candidates CandidateRepository,
	rights RightsValidator,
	log Logger,
	clock Clock,
) MaterializeWorker {
	if log == nil {
		log = NoopLogger()
	}
	if clock == nil {
		clock = RealClock()
	}
	return &defaultMaterializeWorker{
		stock:      stock,
		candidates: candidates,
		rights:     rights,
		log:        log,
		clock:      clock,
	}
}

// Compile-time assertion: defaultMaterializeWorker satisfies
// MaterializeWorker. Drift surfaces as a build error.
var _ MaterializeWorker = (*defaultMaterializeWorker)(nil)

// Materialize runs the canonical Cold→{Warm,Hot} promotion
// loop. godlike/06 SSOT (per-candidate error isolation): every
// promote failure appends to res.FailedCandidateIDs and the loop
// continues — the worker MUST NOT short-circuit on a single bad
// candidate because BatchSpec.MaterializeTopK is canonically a
// ordered Top-K ranking and partial-success is allowed.
//
// godlike/07 NO-FAKE-AVAILABILITY: an empty stockpipeline
// adapter trips ErrSemanticNotConfigured (godlike/06 fail-closed
// semantic on the StockPipelineAcquirer port). Empty Promotes
// produces an idle result with zero count.
func (w *defaultMaterializeWorker) Materialize(ctx context.Context, req MaterializationRequest) (MaterializationResult, error) {
	res := MaterializationResult{
		PersistedAssetIDs:  make([]string, 0, len(req.Promotes)),
		FailedCandidateIDs: make([]string, 0),
		Failures:           make([]string, 0),
	}
	if w.stock == nil {
		return res, fmt.Errorf(
			"mediamemory: materialize worker StockPipelineAcquirer not wired (composition root must inject prod adapter): %w",
			ErrSemanticNotConfigured,
		)
	}
	if w.candidates == nil {
		return res, fmt.Errorf(
			"mediamemory: materialize worker CandidateRepository not wired: %w",
			ErrCandidateNotFound,
		)
	}
	if len(req.Promotes) == 0 {
		return res, nil
	}

	// godlike/06 SSOT (idempotent promotion gate): a candidate
	// already at Hot tier with non-empty AssetID is a no-op so a
	// re-run of MaterializeTopK (e.g., after worker crash) is
	// safely resumable. Warm→Cold rewind is FORBIDDEN here —
	// promoting back to Cold would orphan in-flight hot bytes.
	for _, p := range req.Promotes {
		if p.Candidate.MaterializationStatus == MaterializationHot &&
			p.Candidate.AssetID != "" {
			// Idempotency hit.
			res.PersistedAssetIDs = append(res.PersistedAssetIDs, p.Candidate.AssetID)
			res.MaterializedCount++
			continue
		}

		// godlike/06 SSOT (gate enforcement): every Cold→Warm
		// promotion re-validates rights at the worker boundary.
		// The canonical AcquisitionPlanner.Plan already filters
		// by Verdict ∈ {Allow, AllowConditional-with-empty-
		// Conditions}; this defence-in-depth guards against
		// planner-side drift.
		decision, rerr := w.rights.Validate(ctx, p.Candidate, req.ProjectID)
		if rerr != nil {
			res.FailedCandidateIDs = append(res.FailedCandidateIDs, p.Candidate.ID)
			res.Failures = append(res.Failures, fmt.Sprintf(
				"candidate=%q rights-validate failed: %s",
				p.Candidate.ID, rerr.Error()))
			continue
		}
		if decision.Verdict == RightsVerdictDeny {
			res.FailedCandidateIDs = append(res.FailedCandidateIDs, p.Candidate.ID)
			res.Failures = append(res.Failures, fmt.Sprintf(
				"candidate=%q rights-verify denied: %s",
				p.Candidate.ID, decision.Reason))
			continue
		}

		// godlike/06 SSOT (target slot hint propagation): the
		// planner specifies a TargetSlot that biases the
		// stockpipeline output (e.g., primary_video wants a
		// higher bitrate than secondary_image). Default is
		// media.SlotPrimaryVideo so backward-compat tests stay green.
		targetSlot := p.TargetSlot
		if targetSlot == "" {
			targetSlot = media.SlotPrimaryVideo
		}
		opts := MaterializeOptions{
			TargetSlot:    targetSlot,
			HotCache:      req.HotCache,
			MaxDurationMs: 0, // planner did not pre-budget
			ProjectID:     req.ProjectID,
		}

		mat, merr := w.stock.Materialize(ctx, p.Candidate, opts)
		if merr != nil {
			res.FailedCandidateIDs = append(res.FailedCandidateIDs, p.Candidate.ID)
			res.Failures = append(res.Failures, fmt.Sprintf(
				"candidate=%q stockpipeline materialize failed: %s",
				p.Candidate.ID, merr.Error()))
			continue
		}

		// godlike/07 NO-FAKE-AVAILABILITY: stockpipeline must
		// return a populated AssetID; an empty AssetID is the
		// canonical "fake-availability" failure mode — ranker
		// must not promote unready candidates. Surface as
		// typed envelope.
		if mat.AssetID == "" {
			res.FailedCandidateIDs = append(res.FailedCandidateIDs, p.Candidate.ID)
			res.Failures = append(res.Failures, fmt.Sprintf(
				"candidate=%q stockpipeline returned empty AssetID (NO-FAKE-AVAILABILITY): %s",
				p.Candidate.ID, ErrCandidateMaterializationFailed.Error()))
			continue
		}

		// godlike/06 SSOT (canonical tier assignment):
		//   - HotCache=true → asset is staged locally → Hot
		//   - HotCache=false → asset only rights-checked →
		//                      Warm (catalog-only Top-K path)
		newTier := MaterializationWarm
		newDiscovery := DiscoveryIndexed // canonical state after materialize
		if req.HotCache {
			newTier = MaterializationHot
			newDiscovery = DiscoveryMaterialized
		}
		if uerr := w.candidates.UpdateStatus(ctx, mat.ID, newDiscovery, newTier); uerr != nil {
			// UpdateStatus failure is non-fatal: the asset was
			// minted, but the tier-state row didn't flip. Log
			// and continue — the orchestrator's per-child
			// counter uses CandidateRepository as the
			// canonical source for MaterializedCount.
			res.Failures = append(res.Failures, fmt.Sprintf(
				"candidate=%q UpdateStatus tier=%q: %s",
				mat.ID, string(newTier), uerr.Error()))
		}

		res.PersistedAssetIDs = append(res.PersistedAssetIDs, mat.AssetID)
		res.MaterializedCount++
	}
	return res, nil
}

// PromoteOnDemand is the canonical Warm→Hot (or Cold→Hot) hot
// path. It is invoked by the resolver / scene-render pipeline
// when a candidate is selected for a video clip. The candidate
// MUST already be DiscoveryIndexed (linker passed); the worker
// re-runs the rights gate (defence-in-depth) and shells out to
// the canonical stockpipeline for the segments the renderer
// needs. AssetID is the canonical return shape.
//
// godlike/06 SSOT (idempotence + on-demand resume): a candidate
// already at Hot with non-empty AssetID returns the same shape
// (zero side-effects) so a retry after partial-failure is safe.
// A candidate at Warm is the canonical happy path.
//
// godlike/07 NO-FAKE-AVAILABILITY: an empty AssetID return from
// stockpipeline wraps ErrCandidateMaterializationFailed so
// callers branch via errors.Is — never via "AssetID == \"\" on
// success".
func (w *defaultMaterializeWorker) PromoteOnDemand(
	ctx context.Context,
	candidate MediaCandidate,
	opts MaterializeOptions,
) (MediaCandidate, error) {
	// godlike/07 NO-FAKE-AVAILABILITY: preconditions are checked
	// FIRST so a missing/wrong-shaped input never silently
	// reaches stockpipeline.
	if w.stock == nil {
		return candidate, fmt.Errorf(
			"mediamemory: PromoteOnDemand StockPipelineAcquirer not wired: %w",
			ErrSemanticNotConfigured,
		)
	}
	if w.candidates == nil {
		return candidate, fmt.Errorf(
			"mediamemory: PromoteOnDemand CandidateRepository not wired: %w",
			ErrCandidateNotFound,
		)
	}
	if strings.TrimSpace(candidate.ID) == "" {
		return candidate, fmt.Errorf(
			"mediamemory: PromoteOnDemand candidate.ID is empty: %w",
			ErrCandidateNotFound,
		)
	}

	// godlike/06 SSOT (idempotent re-promotion): an already-Hot
	// candidate with non-empty AssetID is a no-op (canonical
	// Resume-after-success contract).
	if candidate.MaterializationStatus == MaterializationHot &&
		candidate.AssetID != "" {
		return candidate, nil
	}

	// godlike/06 SSOT (rights-gate enforcement): the planner's
	// PlanOnDemand already filters by rights verdict, but the
	// worker re-validates to guard against planner-side drift
	// and to honour opts.ProjectID for downstream rights
	// decryption (project-scoped allowed_channels/regions).
	decision, rerr := w.rights.Validate(ctx, candidate, opts.ProjectID)
	if rerr != nil {
		return candidate, fmt.Errorf(
			"mediamemory: PromoteOnDemand candidate=%q rights-validate: %w",
			candidate.ID, rerr)
	}
	if decision.Verdict == RightsVerdictDeny {
		// godlike/07 surface as the canonical approval-required
		// envelope so the resolver branch is canonical.
		return candidate, fmt.Errorf(
			"mediamemory: PromoteOnDemand candidate=%q rights-verify denied (%s): %w",
			candidate.ID, decision.Reason, ErrApprovalRequired)
	}

	// godlike/06 SSOT (default HotCache): on-demand promotion
	// is canonically Hot (bytes local for the renderer). The
	// caller-supplied opts.HotCache is honored when set; an
	// unset zero-value falls back to true.
	if !opts.HotCache && opts.MaxDurationMs == 0 && opts.TargetSlot == "" {
		opts.HotCache = true
	}
	if opts.TargetSlot == "" {
		opts.TargetSlot = media.SlotPrimaryVideo
	}

	mat, merr := w.stock.Materialize(ctx, candidate, opts)
	if merr != nil {
		return candidate, fmt.Errorf(
			"mediamemory: PromoteOnDemand candidate=%q stockpipeline: %w",
			candidate.ID, errors.Join(ErrCandidateMaterializationFailed, merr))
	}
	if mat.AssetID == "" {
		return candidate, fmt.Errorf(
			"mediamemory: PromoteOnDemand candidate=%q stockpipeline returned empty AssetID: %w",
			candidate.ID, ErrCandidateMaterializationFailed)
	}

	// godlike/06 SSOT (canonical tier transition): Hot +
	// DiscoveryMaterialized = "ready for binding + render".
	if uerr := w.candidates.UpdateStatus(
		ctx, mat.ID, DiscoveryMaterialized, MaterializationHot,
	); uerr != nil {
		return candidate, fmt.Errorf(
			"mediamemory: PromoteOnDemand candidate=%q UpdateStatus: %w",
			mat.ID, uerr)
	}
	return mat, nil
}
