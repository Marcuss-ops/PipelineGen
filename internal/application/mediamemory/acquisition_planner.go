// Package mediamemory — acquisition_planner.go is the canonical SSOT
// for the "decide what to download / segment / promote" decision
// surface (architecture doc section 8: parent/child batch model).
//
// godlike/06 SSOT: AcquisitionPlanner is the SINGLE owner of the
// hot/warm/cold tiering decision (godlike/06 closed set per
// MaterializationStatus). Every discoverer / linker / batch owner
// routes through it. Direct StockPipelineAcquirer.Materialize calls
// outside the planner are forward-prevention forbidden.
//
// godlike/07 NO-FAKE-AVAILABILITY: rights-uncertain candidates
// (RightsStatus == RightsDenied or RightsExpired) are NEVER
// promoted to Hot during the same plan pass; they MUST route
// through the rights-gate drop at the planner boundary. The
// worker re-validates as defence-in-depth.
package mediamemory

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	"sort"
)

// AcquisitionPlanner is the canonical port.
type AcquisitionPlanner interface {
	// Plan selects the top-K candidates from a batch that should
	// be promoted from Cold→Warm. The candidate snapshot is
	// included in each AcquisitionPromote so the worker does
	// NOT need a second repository round-trip.
	Plan(ctx context.Context, in AcquisitionInput) ([]AcquisitionPromote, error)

	// PlanOnDemand decides whether a single candidate (chosen by
	// the resolver at render time) should be promoted Warm→Hot
	// on demand. Returns the chosen promotion (or
	// ErrApprovalRequired on rights-denied).
	PlanOnDemand(ctx context.Context, candidate MediaCandidate) (AcquisitionPromote, error)
}

// AcquisitionInput is the planner input bundle. The planner NEVER
// re-reads Candidates — they are pinned in `Candidates`.
type AcquisitionInput struct {
	BatchID          string
	TopK             int
	Candidates       []MediaCandidate
	RightsDecisions  map[string]RightsDecision // candidate_id → verdict
	HotCacheSlotUsed int
	HotCacheLimit    int
}

// AcquisitionPromote is the canonical mutation tuple the planner
// returns. godlike/06 SSOT (snapshot + decision): the tuple
// carries the FULL candidate snapshot (the planner re-reads
// Candidates at Plan-time so the worker never needs a second
// candidate-repository round-trip) plus the canonical
// promotion decision (Target tier + TargetSlot hint + HotCache
// flag + Reason audit string).
type AcquisitionPromote struct {
	Candidate  MediaCandidate
	Target     MaterializationStatus
	TargetSlot SlotKind
	HotCache   bool
	Reason     string
}

// ── Default implementation ─────────────────────────────────────────

// defaultAcquisitionPlanner is the canonical implementation.
// godlike/06 SSOT (no-DB-read planner): the planner is a pure
// function over the AcquisitionInput — it does NOT touch the
// DB, the rights validator, or the stockpipeline. The worker
// applies the rights gate as defence-in-depth at the
// promote-row boundary.
type defaultAcquisitionPlanner struct {
	log Logger
}

// NewDefaultAcquisitionPlanner constructs the planner.
func NewDefaultAcquisitionPlanner(log Logger) *defaultAcquisitionPlanner {
	if log == nil {
		log = NoopLogger()
	}
	return &defaultAcquisitionPlanner{log: log}
}

var _ AcquisitionPlanner = (*defaultAcquisitionPlanner)(nil)

// Plan selects the top-K candidates from a batch that should be
// promoted from Cold→Warm.
//
// godlike/06 SSOT (deterministic rank): CandidateScore desc
// with stable tiebreaker on CandidateID asc. A re-run of Plan
// on the same input MUST produce the same permutation so
// Resume-after-crash flow sees a stable Top-K identity.
//
// godlike/07 NO-FAKE-AVAILABILITY (rights gate at planner):
//   - RightsDenied / RightsExpired → silently dropped (NOT in
//     the output slice).
//   - Already-Warm or already-Hot → already promoted, so
//     re-running Plan does NOT count them against the Top-K
//     ceiling (idempotence on partial-progress batches).
func (p *defaultAcquisitionPlanner) Plan(_ context.Context, in AcquisitionInput) ([]AcquisitionPromote, error) {
	if in.TopK <= 0 {
		in.TopK = 1
	}

	scored := make([]MediaCandidate, 0, len(in.Candidates))
	for _, c := range in.Candidates {
		// Drop rights-denied / expired before ranking.
		if c.RightsStatus == RightsDenied || c.RightsStatus == RightsExpired {
			continue
		}
		// godlike/06 SSOT (idempotent re-plan): already-Warm or
		// already-Hot rows are NOT counted toward the Top-K
		// ceiling — the planner treats the partial-progress
		// batch as if those rows were already promoted.
		if c.MaterializationStatus == MaterializationWarm ||
			c.MaterializationStatus == MaterializationHot {
			continue
		}
		scored = append(scored, c)
	}
	sortCandidatesByScoreDesc(scored)

	limit := in.TopK
	if limit > len(scored) {
		limit = len(scored)
	}

	out := make([]AcquisitionPromote, 0, limit)
	for i := 0; i < limit; i++ {
		c := scored[i]
		target := MaterializationWarm
		slot := media.SlotPrimaryVideo
		if c.MediaType == "image" {
			slot = media.SlotSecondaryImage
		}
		out = append(out, AcquisitionPromote{
			Candidate:  c,
			Target:     target,
			TargetSlot: slot,
			HotCache:   false,
			Reason:     "top-k slot promotion (plan)",
		})
	}
	return out, nil
}

// PlanOnDemand decides whether a single candidate (chosen by the
// resolver at render time) should be promoted Warm→Hot on demand.
// The Hot-cache envelope is mandatory for this entry point so the
// downstream renderer can stage bytes locally.
//
// godlike/07 NO-FAKE-AVAILABILITY: a Deny verdict returns
// wrapped ErrApprovalRequired so the resolver branches via
// errors.Is on the canonical envelope.
func (p *defaultAcquisitionPlanner) PlanOnDemand(_ context.Context, c MediaCandidate) (AcquisitionPromote, error) {
	if c.RightsStatus == RightsDenied || c.RightsStatus == RightsExpired {
		return AcquisitionPromote{}, fmt.Errorf(
			"mediamemory: AcquisitionPlanner PlanOnDemand candidate=%q rights=%q: %w",
			c.ID, string(c.RightsStatus), ErrApprovalRequired,
		)
	}
	slot := media.SlotPrimaryVideo
	if c.MediaType == "image" {
		slot = media.SlotSecondaryImage
	}
	return AcquisitionPromote{
		Candidate:  c,
		Target:     MaterializationHot,
		TargetSlot: slot,
		HotCache:   true,
		Reason:     "on-demand hot-tier promotion (plan)",
	}, nil
}

// sortCandidatesByScoreDesc orders candidates by CandidateScore
// desc with a stable tiebreaker on CandidateID asc.
func sortCandidatesByScoreDesc(cs []MediaCandidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].CandidateScore != cs[j].CandidateScore {
			return cs[i].CandidateScore > cs[j].CandidateScore
		}
		return cs[i].ID < cs[j].ID
	})
}
