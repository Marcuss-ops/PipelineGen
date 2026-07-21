// Package mediamemory — feedback_service.go is the canonical SSOT
// for the human-in-the-loop feedback surface (POST
// /api/media-memory/feedback).
//
// godlike/06 SSOT: FeedbackService is the SINGLE owner of the
// media_usage_events write surface that drives the ranker's
// success_score increment loop (architecture doc section 12:
// "non restituire sempre il candidato con punteggio più alto").
// Every accepted/rejected/replaced/trimmed/used_successfully event
// MUST route through this service so the ranker can promote
// success_score uniformly across all manual UI paths.
//
// godlike/07 NO-FAKE-AVAILABILITY: unknown FeedbackAction values
// surface as wrapped ErrInvalidFeedbackAction. Per-binding
// ownership mismatches (a binding_id we cannot resolve) surface as
// wrapped ErrBindingNotFound so callers branch via errors.Is.
//
// order of operations in Record (godlike/07 fail-closed audit):
//  1. validate the action (DeltaForAction — surfaces
//     ErrInvalidFeedbackAction on unknown values);
//  2. validate the binding_id exists (BindingRepository.FindByID);
//  3. append the UsageEvent (audit log; append-only by design);
//  4. update the binding's SuccessScore + UsageCount + LastUsedAt
//     via BindingsRepository.Upsert (which now propagates these
//     three columns in its ON CONFLICT DO UPDATE clause as of
//     Fase 1.4).
//
// godlike/06 SSOT (append-then-update): if step 4 fails after step
// 3, the audit log retains the event and reconciliation can replay
// later — the alternative (update-then-append) would risk a
// silent score drift with no audit trail.
package mediamemory

import (
	"context"
	"errors"
	"fmt"
)

// FeedbackService is the canonical port for feedback ingestion.
// Concrete impl is defaultFeedbackService below.
type FeedbackService interface {
	// Record appends one UsageEvent and applies the corresponding
	// success_score delta on the affected binding.
	Record(ctx context.Context, in FeedbackInput) (UsageEvent, error)

	// AggregateSince returns bound counts per (concept, slot) since
	// `since`. Used by ranker warm-up after re-deploys.
	AggregateSince(ctx context.Context, since string) ([]FeedbackAggregate, error)
}

// FeedbackInput is the canonical shape received from the API.
//
// godlike/06 SSOT (Fase 2.3 anti-repetition contract): ChannelID
// and VideoID are recorded alongside ProjectID so the resolver's
// repetition_penalty has a deterministic source-of-truth identity
// for every event without a runtime join against media_assets.
// Empty values are valid (caller-side omitted, e.g. legacy log
// rows pre-Fase 2.3) — the ranker treats empty channel/video as
// "no penalty input available" but the same-asset penalty still
// drives the contract (UsageCount + SuccessScore lifetime).
type FeedbackInput struct {
	ProjectID  string
	SceneID    string
	BindingID  string
	Action     FeedbackAction
	ChannelID  string // optional; "" when caller omits
	VideoID    string // optional; "" when caller omits
	OccurredAt string // ISO8601; defaults to clock.Now() if empty
	Reason     string // free-form, optional
}

// UsageEventDelta maps FeedbackAction to the success_score increment.
// godlike/06 SSOT (closed set, see IsKnownFeedbackAction).
type UsageEventDelta struct {
	SelectedIncrement         float64
	ManuallySelectedIncrement float64
	RenderCompletedIncrement  float64
	RejectedIncrement         float64
}

// DeltaForAction returns the canonical Phase 1.x scoring delta for
// the input action. Future revisions land as a new function
// (e.g. DeltaForActionV2) — callers MUST pin the version.
func DeltaForAction(a FeedbackAction) (UsageEventDelta, error) {
	switch a {
	case FeedbackAccepted:
		return UsageEventDelta{
			SelectedIncrement:         0.05,
			ManuallySelectedIncrement: 0.05,
			RenderCompletedIncrement:  0.10,
			RejectedIncrement:         0.0,
		}, nil
	case FeedbackRejected:
		return UsageEventDelta{
			SelectedIncrement:         -0.10,
			ManuallySelectedIncrement: -0.10,
			RenderCompletedIncrement:  -0.10,
			RejectedIncrement:         0.05,
		}, nil
	case FeedbackReplaced:
		return UsageEventDelta{
			SelectedIncrement:         -0.05,
			ManuallySelectedIncrement: 0.0,
			RenderCompletedIncrement:  -0.05,
			RejectedIncrement:         0.0,
		}, nil
	case FeedbackTrimmed:
		// User kept the clip but trimmed its window. Selection
		// signal is positive but smaller than a full acceptance.
		return UsageEventDelta{
			SelectedIncrement:         0.02,
			ManuallySelectedIncrement: 0.02,
			RenderCompletedIncrement:  0.02,
			RejectedIncrement:         0.0,
		}, nil
	case FeedbackUsedSuccessful:
		// Render completed and rendered successfully — strongest
		// signal for ranking.
		return UsageEventDelta{
			SelectedIncrement:         0.10,
			ManuallySelectedIncrement: 0.10,
			RenderCompletedIncrement:  0.20,
			RejectedIncrement:         0.0,
		}, nil
	default:
		// godlike/06 SSOT: dedicated sentinel (NOT ErrInvalidPhrase
		// which is reserved for Normalizer input corruption).
		return UsageEventDelta{}, errors.Join(ErrInvalidFeedbackAction,
			errors.New("unknown: "+string(a)))
	}
}

// FeedbackAggregate is the per-(concept, slot) summary the ranker
// warm-up reads.
type FeedbackAggregate struct {
	ConceptID  string
	SlotKind   SlotKind
	AcceptedN  int
	RejectedN  int
	SuccessN   int
	AvgScore   float64
	LastUsedAt string
}

// ── Default implementation (canonical) ──────────────────────────

// defaultFeedbackService is the canonical implementation.
type defaultFeedbackService struct {
	usage    UsageRepository
	bindings BindingRepository
	log      Logger
	clock    Clock
}

// NewDefaultFeedbackService constructs the service.
func NewDefaultFeedbackService(usage UsageRepository, bindings BindingRepository, log Logger, clock Clock) *defaultFeedbackService {
	if log == nil {
		log = NoopLogger()
	}
	if clock == nil {
		clock = RealClock()
	}
	return &defaultFeedbackService{
		usage:    usage,
		bindings: bindings,
		log:      log,
		clock:    clock,
	}
}

var _ FeedbackService = (*defaultFeedbackService)(nil)

// Record is the canonical entrypoint.
//
// godlike/06 SSOT (closed set, see IsKnownFeedbackAction):
// unknown actions return ErrInvalidFeedbackAction wrapped.
//
// godlike/06 SSOT (binding lookup): the binding MUST exist before
// the feedback event is recorded; the ranker reads the binding's
// updated SuccessScore on the very next resolver call. A missing
// binding returns wrapped ErrBindingNotFound.
//
// godlike/07 NO-FAKE-AVAILABILITY: the binding update is atomic
// (Upsert via ON CONFLICT — usage_count and last_used_at now
// propagate). Selection vs rejection signals are recorded on the
// UsageEvent (audit trail); SuccessScore is the canonical ranker
// input incremented by DeltaForAction.RenderCompletedIncrement.
//
// godlike/06 SSOT (lost-update trade-off, Phase 1.x):
// Record reads the binding → increments UsageCount + SuccessScore
// locally → Upserts the row. Concurrent feedback events for the
// same binding race on UsageCount (last writer wins). The
// append-only media_usage_events audit log retains the full event
// sequence, so an offline job can replay SuccessScore
// deterministically from the audit log on warm-up. This is the
// Phase 1.x trade-off — full optimistic concurrency (e.g.
// SQL `usage_count = usage_count + 1`) lands in Fase 2.x alongside
// the ranker warm-up reader. Until then, the bind-side audit
// log is the canonical truth.
func (s *defaultFeedbackService) Record(ctx context.Context, in FeedbackInput) (UsageEvent, error) {
	if in.BindingID == "" {
		return UsageEvent{}, fmt.Errorf(
			"mediamemory: feedback record missing binding_id: %w",
			ErrBindingNotFound,
		)
	}
	if !IsKnownFeedbackAction(in.Action) {
		return UsageEvent{}, fmt.Errorf(
			"mediamemory: feedback record action=%q: %w",
			string(in.Action), ErrInvalidFeedbackAction,
		)
	}

	delta, err := DeltaForAction(in.Action)
	if err != nil {
		return UsageEvent{}, err
	}

	binding, err := s.bindings.FindByID(ctx, in.BindingID)
	if err != nil {
		return UsageEvent{}, fmt.Errorf(
			"mediamemory: feedback record binding %q: %w",
			in.BindingID, err,
		)
	}

	now := s.clock.Now().UTC()

	// 1. Append the audit event (append-only; ALWAYS succeeds
	// before we touch the binding success score).
	//
	// godlike/06 SSOT (Fase 2.3 anti-repetition): ChannelID +
	// VideoID flow verbatim from FeedbackInput into the
	// append-only UsageEvent so the resolver's
	// repetition_penalty loop has identity-bearing rows to
	// read via ListProjectUsages (forward-pointer to Fase 2.3).
	event := UsageEvent{
		ProjectID:        in.ProjectID,
		SceneID:          in.SceneID,
		ConceptID:        binding.ConceptID,
		AssetID:          binding.AssetID,
		BindingID:        binding.ID,
		SlotKind:         binding.SlotKind,
		ChannelID:        in.ChannelID,
		VideoID:          in.VideoID,
		Selected:         delta.SelectedIncrement > 0 || in.Action == FeedbackAccepted,
		ManuallySelected: delta.ManuallySelectedIncrement > 0 || in.Action == FeedbackAccepted,
		Rejected:         in.Action == FeedbackRejected || in.Action == FeedbackReplaced,
		RenderCompleted:  in.Action == FeedbackUsedSuccessful,
		CreatedAt:        now,
	}
	if err := s.usage.Append(ctx, event); err != nil {
		return UsageEvent{}, fmt.Errorf("mediamemory: feedback record append usage event: %w", err)
	}

	// 2. Update the binding's score lineage (atomic via Upsert).
	// godlike/06 SSOT: RenderCompletedIncrement is the canonical
	// SuccessScore increment per architecture doc section 12
	// ("non restituire sempre il candidato con punteggio più
	// alto"). Other deltas (SelectedIncrement, ManuallySelected,
	// Rejected) drive future Phase-2 weights / penalty tuning.
	binding.SuccessScore += delta.RenderCompletedIncrement
	binding.UsageCount++
	binding.LastUsedAt = &now
	binding.UpdatedAt = now

	if _, err := s.bindings.Upsert(ctx, binding); err != nil {
		return UsageEvent{}, fmt.Errorf(
			"mediamemory: feedback record upsert binding %q: "+
				"UsageEvent appended but binding score not "+
				"promoted (will require offline reconciliation): %w",
			in.BindingID, err,
		)
	}

	return event, nil
}

// AggregateSince is the ranker warm-up read. Phase 1.4 leaves this
// as an explicit stub because the canonical implementation needs
// a real SQL aggregate query (UsageRepository.ListSince + a
// ranker-side GROUP BY aggregation). The companion suggest_followup
// lands this in Fase 1.5.
//
// godlike/07 NO-FAKE-AVAILABILITY: returning an empty slice here
// would silently give the ranker "no signal" and is wrong; so we
// return the stub sentinel rather than fake data. Composition root
// surfaces this as a 501 to any consumer until Fase 1.5 lands.
func (s *defaultFeedbackService) AggregateSince(_ context.Context, _ string) ([]FeedbackAggregate, error) {
	return nil, errNotImplemented("mediamemory: defaultFeedbackService.AggregateSince not yet implemented (Phase 1.5)")
}
