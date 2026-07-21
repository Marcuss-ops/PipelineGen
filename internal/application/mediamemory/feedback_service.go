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
// surface as wrapped ErrInvalidPhrase (re-using the canonical
// envelope — semantic-equivalent failure). Per-binding ownership
// mismatches (a binding_id owned by a different project) surface
// as wrapped ErrBindingNotFound so callers branch via errors.Is.
package mediamemory

import (
	"context"
	"errors"
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
type FeedbackInput struct {
	ProjectID  string
	SceneID    string
	BindingID  string
	Action     FeedbackAction
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

// ── Default implementation (skeleton) ─────────────────────────────

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

// Record is the canonical Phase 1.x entrypoint: identity stub;
// Phase 2 wires the (append usage_event → update binding success_score)
// chain.
func (s *defaultFeedbackService) Record(_ context.Context, _ FeedbackInput) (UsageEvent, error) {
	return UsageEvent{}, errNotImplemented("mediamemory: defaultFeedbackService.Record not yet implemented (Phase 1.x)")
}

func (s *defaultFeedbackService) AggregateSince(_ context.Context, _ string) ([]FeedbackAggregate, error) {
	return nil, errNotImplemented("mediamemory: defaultFeedbackService.AggregateSince not yet implemented (Phase 1.x)")
}
