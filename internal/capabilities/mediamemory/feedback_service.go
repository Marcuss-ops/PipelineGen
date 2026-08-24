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
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"sort"
	"time"
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

	// godlike/07 NO-FAKE-AVAILABILITY (Fase 1.6 SlotKind guard):
	// the binding's SlotKind is propagated verbatim onto the
	// UsageEvent; a binding with an uncanonical SlotKind would
	// slip past the append-only audit log and corrupt the ranker
	// warm-up aggregate (Aggregator groups by SlotKind). Surface
	// the canonical sentinel so the dashboard / API handler can
	// branch via errors.Is and surface a 400 to the operator.
	if !media.IsKnownSlotKind(binding.SlotKind) {
		return UsageEvent{}, fmt.Errorf(
			"mediamemory: feedback record binding %q slot_kind=%q: %w",
			in.BindingID, string(binding.SlotKind), ErrInvalidSlotKind,
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

// AggregateSince is the ranker warm-up read. Fase 1.6 implementation:
// parse the `since` ISO8601 timestamp, call usage.ListSince to read
// the bounded event slice (canonical limit = AntiRepetitionHistoryLimit),
// then group by (ConceptID, SlotKind) in Go and emit one
// FeedbackAggregate per group.
//
// godlike/06 SSOT (port-driven read): the SQL query is the single
// bottleneck at the repository seam (no media_bindings JOIN); the
// Go-side aggregation is O(N) over the bounded slice. This is the
// canonical warm-up cost ceiling — a future SQL-side GROUP BY lands
// as a sibling method (ListAggregatesSince) when the volume
// justifies the index round-trip.
//
// godlike/07 NO-FAKE-AVAILABILITY: an empty bounded slice returns
// an empty (non-nil) aggregate slice with zero entries. The caller
// MUST NOT interpret this as "no signal" — godlike/06 SSOT pins
// empty == "no events yet" (legitimate initial state after a fresh
// deploy).
//
// AvgScore is computed as the simple success-rate signal for the
// (concept, slot) group: (SuccessN + AcceptedN - RejectedN) /
// max(1, total_events). This is in [-1, +2] and is consumed by
// the ranker as a warm-up seed for HistoricalSuccessScore. A future
// migration denormalizes the binding SuccessScore at event time so
// AvgScore can be an exact value rather than a derived rate.
func (s *defaultFeedbackService) AggregateSince(ctx context.Context, since string) ([]FeedbackAggregate, error) {
	parsed, err := parseAggregateSince(since)
	if err != nil {
		return nil, err
	}
	events, err := s.usage.ListSince(ctx, parsed, AntiRepetitionHistoryLimit)
	if err != nil {
		return nil, fmt.Errorf("mediamemory: AggregateSince list since: %w", err)
	}
	if len(events) == 0 {
		return []FeedbackAggregate{}, nil
	}
	// Group by (concept, slot). The internal accumulator carries
	// the raw event count (totalN) so AvgScore divides by the
	// canonical event count, NOT by the sum of the boolean
	// counters — a single used_successfully event has BOTH
	// Selected=true AND RenderCompleted=true, so the boolean sum
	// would double-count it and depress AvgScore. totalN is the
	// godlike/06 SSOT denominator.
	type groupKey struct {
		conceptID string
		slotKind  SlotKind
	}
	type groupAcc struct {
		agg    FeedbackAggregate
		totalN int
	}
	groups := make(map[groupKey]*groupAcc, 8)
	for _, ev := range events {
		k := groupKey{conceptID: ev.ConceptID, slotKind: ev.SlotKind}
		g, ok := groups[k]
		if !ok {
			g = &groupAcc{
				agg: FeedbackAggregate{
					ConceptID: ev.ConceptID,
					SlotKind:  ev.SlotKind,
				},
			}
			groups[k] = g
		}
		g.totalN++
		if ev.Selected {
			g.agg.AcceptedN++
		}
		if ev.Rejected {
			g.agg.RejectedN++
		}
		if ev.RenderCompleted {
			g.agg.SuccessN++
		}
		if g.agg.LastUsedAt == "" || ev.CreatedAt.Format(time.RFC3339) > g.agg.LastUsedAt {
			g.agg.LastUsedAt = ev.CreatedAt.Format(time.RFC3339)
		}
	}
	out := make([]FeedbackAggregate, 0, len(groups))
	for _, g := range groups {
		if g.totalN > 0 {
			g.agg.AvgScore = float64(g.agg.SuccessN+g.agg.AcceptedN-g.agg.RejectedN) / float64(g.totalN)
		}
		out = append(out, g.agg)
	}
	// godlike/06 SSOT (deterministic ordering): sort by
	// (ConceptID ASC, SlotKind ASC) so the ranker warm-up can
	// apply its own ordering on top of a stable base.
	sortFeedbackAggregates(out)
	return out, nil
}

// parseAggregateSince parses the canonical ISO8601 input. An
// empty string is treated as "no lower bound" (the canonical
// post-deploy full warm-up), mapping to time.Time{}. A malformed
// string returns a typed envelope (godlike/07 NO-FAKE-AVAILABILITY)
// wrapping ErrInvalidAggregateSince so the wire handler can
// branch via errors.Is and return a 400.
func parseAggregateSince(since string) (time.Time, error) {
	if since == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, since)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"mediamemory: AggregateSince invalid since=%q (expected RFC3339): %w",
			since, ErrInvalidAggregateSince,
		)
	}
	return t.UTC(), nil
}

// sortFeedbackAggregates sorts in place by (ConceptID ASC,
// SlotKind ASC) for the canonical deterministic ordering
// contract. godlike/06 SSOT (stable sort): equal keys preserve
// the insertion order (which mirrors the event scan order).
// sort.SliceStable so a future caller iterating this aggregate
// in a UI sees deterministic ordering.
func sortFeedbackAggregates(in []FeedbackAggregate) {
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].ConceptID != in[j].ConceptID {
			return in[i].ConceptID < in[j].ConceptID
		}
		return in[i].SlotKind < in[j].SlotKind
	})
}
