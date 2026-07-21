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
// (RightsStatus != RightsVerified) are NEVER promoted to Hot
// during the same plan pass; they MUST route through Warm and
// require a confirmed RightsValidator verdict before promotion.
// godlike/07 surface: ErrCandidateMaterializationFailed wraps
// any planner-internal failure (rights flip, hot-cache eviction,
// stockpipeline reconcile miss).
package mediamemory

import "context"

// AcquisitionPlanner is the canonical port. Concrete impl is
// defaultAcquisitionPlanner below.
type AcquisitionPlanner interface {
	// Plan selects the top-K candidates from a batch that should
	// be promoted from Cold→Warm (always) and Warm→Hot (only if
	// rights verified). Returns the slice of (candidateID, target
	// tier) promotions to apply.
	Plan(ctx context.Context, in AcquisitionInput) ([]AcquisitionPromote, error)

	// PlanOnDemand decides whether a single candidate (chosen by
	// the resolver at render time) should be promoted Warm→Hot
	// on demand. Returns the chosen promotion (or none).
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
// returns. Apply executes the (Cold→Warm→Hot) transition via
// StockPipelineAcquirer.
type AcquisitionPromote struct {
	CandidateID string
	Target      MaterializationStatus
	Reason      string
	HotCache    bool
}

// ── Default implementation (skeleton) ─────────────────────────────

// defaultAcquisitionPlanner is the canonical implementation.
type defaultAcquisitionPlanner struct {
	pipeline StockPipelineAcquirer
	rights   RightsValidator
	log      Logger
}

// NewDefaultAcquisitionPlanner constructs the planner.
func NewDefaultAcquisitionPlanner(pipeline StockPipelineAcquirer, rights RightsValidator, log Logger) *defaultAcquisitionPlanner {
	if log == nil {
		log = NoopLogger()
	}
	return &defaultAcquisitionPlanner{
		pipeline: pipeline,
		rights:   rights,
		log:      log,
	}
}

var _ AcquisitionPlanner = (*defaultAcquisitionPlanner)(nil)

// Plan is the canonical Phase 1.x entrypoint: identity stub;
// Phase 3 wires the (validate rights → choose top-K → promote tier)
// chain.
func (p *defaultAcquisitionPlanner) Plan(_ context.Context, _ AcquisitionInput) ([]AcquisitionPromote, error) {
	return nil, errNotImplemented("mediamemory: defaultAcquisitionPlanner.Plan not yet implemented (Phase 3)")
}

// PlanOnDemand is the canonical Phase 1.x entrypoint: identity
// stub; Phase 3 wires the (rights check → keep Warm, promote Hot
// iff rights_verified) chain.
func (p *defaultAcquisitionPlanner) PlanOnDemand(_ context.Context, _ MediaCandidate) (AcquisitionPromote, error) {
	return AcquisitionPromote{}, errNotImplemented("mediamemory: defaultAcquisitionPlanner.PlanOnDemand not yet implemented (Phase 3)")
}
