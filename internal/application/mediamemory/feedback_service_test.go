// Package mediamemory — feedback_service_test.go is the Fase 1.4
// unit-test surface for defaultFeedbackService.
//
// godlike/06 SSOT (port-driven tests): the feedback_record port
// feeds the ranker success-score increment loop (architecture
// doc section 12, "non restituire sempre il candidato con
// punteggio più alto"). This test exercises the canonical score
// propagation for every closed-set FeedbackAction value so a
// future tweak of DeltaForAction cannot silently drift the ranker.
//
// godlike/07 NO-FAKE-AVAILABILITY (audit-trail-first): the test
// asserts the append-only UsageEvent ledger is updated BEFORE the
// binding SuccessScore promotion (godlike/06 SSOT: append-then-
// update). A drift here would let an offline reconciliation lose
// the audit trail — acceptable but not desirable, so we pin it.
package mediamemory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	"github.com/google/uuid"
)

// ── Fake UsageRepository ────────────────────────────────────────────

// fakeUsageRepo is an in-memory UsageRepository used by the
// feedback_service tests.
//
// godlike/06 SSOT (mirror of concrete): the fake records the
// order of Append calls so the append-then-update audit trail
// ordering assertion can verify godlike/07 in this test layer.
type fakeUsageRepo struct {
	mu     sync.Mutex
	events []UsageEvent
	byID   map[string]UsageEvent
}

func newFakeUsageRepo() *fakeUsageRepo {
	return &fakeUsageRepo{byID: make(map[string]UsageEvent)}
}

func (r *fakeUsageRepo) Append(_ context.Context, ev UsageEvent) error {
	if !media.IsKnownSlotKind(ev.SlotKind) {
		return errors.New("mediamemory: usage event slot_kind unknown")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	r.events = append(r.events, ev)
	r.byID[ev.ID] = ev
	return nil
}

func (r *fakeUsageRepo) ListByConcept(_ context.Context, conceptID string, limit int) ([]UsageEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]UsageEvent, 0, 4)
	for _, e := range r.events {
		if e.ConceptID == conceptID {
			out = append(out, e)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (r *fakeUsageRepo) ListByAsset(_ context.Context, assetID string, limit int) ([]UsageEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]UsageEvent, 0, 4)
	for _, e := range r.events {
		if e.AssetID == assetID {
			out = append(out, e)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// ListProjectUsages is the Fase 2.3 anti-repetition read seam
// mirror. Returns every event for the project (newest first) up
// to the limit. The fake preserves the column-store identity of
// usage events (ChannelID, VideoID) so resolver-level tests can
// assert the canonical repetition_penalty formulas.
func (r *fakeUsageRepo) ListProjectUsages(_ context.Context, projectID string, limit int) ([]UsageEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]UsageEvent, 0, 4)
	// Iterate events in newest-first order so the resolver's
	// pre-cached history mirrors the canonical
	// ORDER BY created_at DESC contract from
	// sqlite/usage_repository.go::ListProjectUsages.
	for i := len(r.events) - 1; i >= 0; i-- {
		e := r.events[i]
		if e.ProjectID == projectID {
			out = append(out, e)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// ListSince is the Fase 1.6 ranker warm-up read seam mirror.
// Returns every event whose CreatedAt is >= since (newest first),
// bounded by limit. A zero `since` returns the most recent
// `limit` events across all projects — matches the canonical
// post-deploy full warm-up behavior.
func (r *fakeUsageRepo) ListSince(_ context.Context, since time.Time, limit int) ([]UsageEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]UsageEvent, 0, 4)
	// Iterate events in newest-first order so the ranker warm-up
	// sees the canonical ORDER BY created_at DESC contract.
	for i := len(r.events) - 1; i >= 0; i-- {
		e := r.events[i]
		if since.IsZero() || !e.CreatedAt.Before(since) {
			out = append(out, e)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// ── Test fixtures ───────────────────────────────────────────────────

const (
	testConceptID = "concept-maya-feed"
	testAssetID   = "asset-chichen-itza"
	testSlotKind  = media.SlotPrimaryVideo
)

func seedFeedbackBinding(bindings *fakeBindingsRepo, score float64, count int) MediaBinding {
	b := MediaBinding{
		ID:        "b-feedback-" + uuid.NewString(),
		ConceptID: testConceptID, AssetID: testAssetID,
		SlotKind: testSlotKind,
		Origin:   OriginManual, ApprovalStatus: ApprovalApproved,
		SuccessScore: score, UsageCount: count,
	}
	bindings.mu.Lock()
	defer bindings.mu.Unlock()
	bindings.byID[b.ID] = b
	return b
}

func defaultFeedbackSvc(t *testing.T) (*defaultFeedbackService, *fakeBindingsRepo, *fakeUsageRepo) {
	t.Helper()
	bindings := newFakeBindingsRepo()
	usage := newFakeUsageRepo()
	svc := NewDefaultFeedbackService(usage, bindings, NoopLogger(), newFixedClock())
	return svc, bindings, usage
}

// ── DeltaForAction (closed-set unit test) ──────────────────────────

func TestDeltaForActionAcceptedProducesPositiveRenderCompleted(t *testing.T) {
	t.Parallel()
	d, err := DeltaForAction(FeedbackAccepted)
	if err != nil {
		t.Fatalf("DeltaForAction(accepted) returned error: %v", err)
	}
	if d.RenderCompletedIncrement <= 0 {
		t.Fatalf("accepted RenderCompletedIncrement must be > 0 (ranker signal), got %f", d.RenderCompletedIncrement)
	}
	if d.SelectedIncrement <= 0 {
		t.Fatalf("accepted SelectedIncrement must be > 0, got %f", d.SelectedIncrement)
	}
}

func TestDeltaForActionRejectedProducesNegativeRenderCompleted(t *testing.T) {
	t.Parallel()
	d, err := DeltaForAction(FeedbackRejected)
	if err != nil {
		t.Fatalf("DeltaForAction(rejected) returned error: %v", err)
	}
	if d.RenderCompletedIncrement >= 0 {
		t.Fatalf("rejected RenderCompletedIncrement must be < 0 (ranker signal), got %f", d.RenderCompletedIncrement)
	}
	if d.RejectedIncrement <= 0 {
		t.Fatalf("rejected RejectedIncrement must be > 0, got %f", d.RejectedIncrement)
	}
}

func TestDeltaForActionUsedSuccessfulHasLargestRenderCompleted(t *testing.T) {
	t.Parallel()
	used, _ := DeltaForAction(FeedbackUsedSuccessful)
	accept, _ := DeltaForAction(FeedbackAccepted)
	if used.RenderCompletedIncrement <= accept.RenderCompletedIncrement {
		t.Fatalf("used_successfully must promote harder than accepted (ranker strongest signal). used=%f accepted=%f",
			used.RenderCompletedIncrement, accept.RenderCompletedIncrement)
	}
}

func TestDeltaForActionUnknownReturnsErrInvalidFeedbackAction(t *testing.T) {
	t.Parallel()
	_, err := DeltaForAction(FeedbackAction("not_a_real_action"))
	if err == nil {
		t.Fatalf("DeltaForAction('not_a_real_action') returned no error")
	}
	if !errors.Is(err, ErrInvalidFeedbackAction) {
		t.Fatalf("DeltaForAction('not_a_real_action') returned %v, want wrapped ErrInvalidFeedbackAction", err)
	}
}

// ── Record fail-closed envelopes ───────────────────────────────────

func TestFeedbackRecordRejectsEmptyBindingID(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)
	_, err := svc.Record(context.Background(), FeedbackInput{
		ProjectID: "p-1", SceneID: "s-1", BindingID: "", Action: FeedbackAccepted,
	})
	if err == nil {
		t.Fatalf("Record accepted empty binding_id")
	}
	if !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("Record returned %v, want wrapped ErrBindingNotFound", err)
	}
	if len(usage.events) != 0 || len(bindings.upserts) != 0 {
		t.Fatalf("Record must NOT touch repos when input is invalid; got %d events / %d upserts",
			len(usage.events), len(bindings.upserts))
	}
}

func TestFeedbackRecordRejectsUnknownAction(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)
	b := seedFeedbackBinding(bindings, 0.0, 0)
	_, err := svc.Record(context.Background(), FeedbackInput{
		ProjectID: "p-1", SceneID: "s-1", BindingID: b.ID, Action: FeedbackAction("not_real"),
	})
	if err == nil {
		t.Fatalf("Record accepted unknown action")
	}
	if !errors.Is(err, ErrInvalidFeedbackAction) {
		t.Fatalf("Record returned %v, want wrapped ErrInvalidFeedbackAction", err)
	}
	if len(usage.events) != 0 || len(bindings.upserts) != 0 {
		t.Fatalf("Record must NOT touch repos on invalid action; got %d events / %d upserts",
			len(usage.events), len(bindings.upserts))
	}
}

func TestFeedbackRecordSurfacesBindingNotFound(t *testing.T) {
	t.Parallel()
	svc, _, usage := defaultFeedbackSvc(t)
	_, err := svc.Record(context.Background(), FeedbackInput{
		ProjectID: "p-1", SceneID: "s-1", BindingID: "missing-id", Action: FeedbackAccepted,
	})
	if err == nil {
		t.Fatalf("Record accepted missing binding")
	}
	if !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("Record returned %v, want wrapped ErrBindingNotFound", err)
	}
	if len(usage.events) != 0 {
		t.Fatalf("Record must NOT append an event when binding is missing; got %d events", len(usage.events))
	}
}

// ── Record score propagation (one test per FeedbackAction) ─────────

// assertCanonicalScoreAndCount checks the canonical
// post-Record state: usage_count incremented by 1, last_used_at
// stamped to clock.Now(), RenderCompleted * delta applied to
// SuccessScore, and the audit event was appended.
func assertCanonicalScoreAndCount(t *testing.T, before MediaBinding, after MediaBinding,
	wantRenderCompleted bool, expectedDelta float64, usage *fakeUsageRepo) {
	t.Helper()
	if after.UsageCount != before.UsageCount+1 {
		t.Fatalf("UsageCount increment failed: before=%d after=%d", before.UsageCount, after.UsageCount)
	}
	if after.LastUsedAt == nil {
		t.Fatalf("Record must stamp LastUsedAt on every action")
	}
	if !after.LastUsedAt.Equal(newFixedClock().Now()) {
		t.Fatalf("Record stamped LastUsedAt = %v, want %v (clock.Now)", *after.LastUsedAt, newFixedClock().Now())
	}
	gotDelta := after.SuccessScore - before.SuccessScore
	if absDelta(gotDelta-expectedDelta) > 1e-9 {
		t.Fatalf("SuccessScore delta = %f, want %f (±1e-9)", gotDelta, expectedDelta)
	}
	if wantRenderCompleted && !usage.events[len(usage.events)-1].RenderCompleted {
		t.Fatalf("Record: appended event must have RenderCompleted=true for used_successfully")
	}
}

func absDelta(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestFeedbackRecordAcceptedPromotesScoreAndAppendsEvent(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)
	b := seedFeedbackBinding(bindings, 0.0, 0)
	got, err := svc.Record(context.Background(), FeedbackInput{
		ProjectID: "p-1", SceneID: "s-1", BindingID: b.ID, Action: FeedbackAccepted,
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	delta, _ := DeltaForAction(FeedbackAccepted)
	stored, _ := bindings.FindByID(context.Background(), b.ID)
	assertCanonicalScoreAndCount(t, b, stored, false, delta.RenderCompletedIncrement, usage)

	if got.BindingID != b.ID {
		t.Fatalf("returned event BindingID = %q, want %q", got.BindingID, b.ID)
	}
	if !got.Selected || !got.ManuallySelected {
		t.Fatalf("accepted event must have Selected && ManuallySelected = true; got %+v", got)
	}
	if len(usage.events) != 1 {
		t.Fatalf("usage ledger got %d events, want 1", len(usage.events))
	}
}

func TestFeedbackRecordRejectedDemotesScoreAndAppendsEvent(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)
	b := seedFeedbackBinding(bindings, 0.5, 2)
	got, err := svc.Record(context.Background(), FeedbackInput{
		ProjectID: "p-1", SceneID: "s-1", BindingID: b.ID, Action: FeedbackRejected,
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	delta, _ := DeltaForAction(FeedbackRejected)
	stored, _ := bindings.FindByID(context.Background(), b.ID)
	assertCanonicalScoreAndCount(t, b, stored, false, delta.RenderCompletedIncrement, usage)

	if !got.Rejected {
		t.Fatalf("rejected event must have Rejected = true; got %+v", got)
	}
}

func TestFeedbackRecordReplacedPenaltyAndAppendsEvent(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)
	b := seedFeedbackBinding(bindings, 0.5, 1)
	_, err := svc.Record(context.Background(), FeedbackInput{
		ProjectID: "p-1", SceneID: "s-1", BindingID: b.ID, Action: FeedbackReplaced,
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	delta, _ := DeltaForAction(FeedbackReplaced)
	stored, _ := bindings.FindByID(context.Background(), b.ID)
	assertCanonicalScoreAndCount(t, b, stored, false, delta.RenderCompletedIncrement, usage)
}

func TestFeedbackRecordTrimmedPositiveAndAppendsEvent(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)
	b := seedFeedbackBinding(bindings, 0.5, 0)
	_, err := svc.Record(context.Background(), FeedbackInput{
		ProjectID: "p-1", SceneID: "s-1", BindingID: b.ID, Action: FeedbackTrimmed,
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	delta, _ := DeltaForAction(FeedbackTrimmed)
	stored, _ := bindings.FindByID(context.Background(), b.ID)
	assertCanonicalScoreAndCount(t, b, stored, false, delta.RenderCompletedIncrement, usage)
}

func TestFeedbackRecordUsedSuccessfulStronglyPromotesAndAppendsEvent(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)
	b := seedFeedbackBinding(bindings, 0.5, 1)
	got, err := svc.Record(context.Background(), FeedbackInput{
		ProjectID: "p-1", SceneID: "s-1", BindingID: b.ID, Action: FeedbackUsedSuccessful,
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	delta, _ := DeltaForAction(FeedbackUsedSuccessful)
	stored, _ := bindings.FindByID(context.Background(), b.ID)
	assertCanonicalScoreAndCount(t, b, stored, true, delta.RenderCompletedIncrement, usage)
	if !got.RenderCompleted {
		t.Fatalf("used_successfully event must have RenderCompleted=true")
	}
}

// ── Append-then-update audit-trail invariant ────────────────────────

func TestFeedbackRecordAppendsEventBeforeUpsertingBinding(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)
	b := seedFeedbackBinding(bindings, 0.5, 1)

	// Pre-condition: force the bindings.Upsert call to FAIL so we
	// can observe that the append-only UsageEvent is still
	// persisted (and Record surfaces the error after the append).
	bindings.failNextErr = errors.New("synthetic upsert failure")
	defer func() { bindings.failNextErr = nil }()

	_, err := svc.Record(context.Background(), FeedbackInput{
		ProjectID: "p-1", SceneID: "s-1", BindingID: b.ID, Action: FeedbackAccepted,
	})
	if err == nil {
		t.Fatalf("Record should have surfaced the synthetic Upsert failure")
	}
	// godlike/06 SSOT (append-then-update): even though step 4
	// (binding upsert) failed, step 3 (usage append) succeeded.
	if len(usage.events) != 1 {
		t.Fatalf("Record must still append the audit event when binding Upsert fails (append-then-update SSOT); got %d events",
			len(usage.events))
	}
}

// ── AggregateSince stub invariant ──────────────────────────────────

func TestFeedbackAggregateSinceReturnsEmptyForNoEvents(t *testing.T) {
	t.Parallel()
	svc, _, _ := defaultFeedbackSvc(t)
	aggregates, err := svc.AggregateSince(context.Background(), "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("AggregateSince returned unexpected error: %v", err)
	}
	if len(aggregates) != 0 {
		t.Fatalf("expected 0 aggregates for empty repo, got %d", len(aggregates))
	}
}

// TestFeedbackAggregateSinceGroupsByConceptSlot pins the SPEC
// contract: AggregateSince returns one FeedbackAggregate per
// (ConceptID, SlotKind) pair with the canonical count fields
// (AcceptedN, RejectedN, SuccessN) derived from the bounded event
// slice since `since`.
func TestFeedbackAggregateSinceGroupsByConceptSlot(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)

	b1 := seedFeedbackBinding(bindings, 0.0, 0)
	b1.ConceptID = "concept-A"
	b1.SlotKind = media.SlotPrimaryVideo
	bindings.byID[b1.ID] = b1

	b2 := seedFeedbackBinding(bindings, 0.0, 0)
	b2.ConceptID = "concept-A"
	b2.SlotKind = media.SlotSecondaryImage
	bindings.byID[b2.ID] = b2

	b3 := seedFeedbackBinding(bindings, 0.0, 0)
	b3.ConceptID = "concept-B"
	b3.SlotKind = media.SlotPrimaryVideo
	bindings.byID[b3.ID] = b3

	b4 := seedFeedbackBinding(bindings, 0.0, 0)
	b4.ConceptID = "concept-B"
	b4.SlotKind = media.SlotSecondaryImage
	bindings.byID[b4.ID] = b4

	now := newFixedClock().Now()
	mkEv := func(c string, s SlotKind, b MediaBinding, sel, rej, rc bool) UsageEvent {
		return UsageEvent{
			ProjectID: "p-1", ConceptID: c, SlotKind: s,
			AssetID: b.AssetID, BindingID: b.ID,
			Selected: sel, Rejected: rej, RenderCompleted: rc,
			CreatedAt: now,
		}
	}
	usage.events = []UsageEvent{
		mkEv("concept-A", media.SlotPrimaryVideo, b1, true, false, false),
		mkEv("concept-A", media.SlotPrimaryVideo, b1, false, true, false),
		mkEv("concept-A", media.SlotSecondaryImage, b2, false, false, true),
		mkEv("concept-B", media.SlotPrimaryVideo, b3, true, false, false),
		mkEv("concept-B", media.SlotPrimaryVideo, b3, true, false, false),
		mkEv("concept-B", media.SlotSecondaryImage, b4, false, true, false),
	}

	agg, err := svc.AggregateSince(context.Background(), "")
	if err != nil {
		t.Fatalf("AggregateSince returned error: %v", err)
	}
	if len(agg) != 4 {
		t.Fatalf("AggregateSince returned %d aggregates, want 4 (one per concept × slot pair); got %+v",
			len(agg), agg)
	}

	for i := 1; i < len(agg); i++ {
		prev, cur := agg[i-1], agg[i]
		if prev.ConceptID > cur.ConceptID ||
			(prev.ConceptID == cur.ConceptID && prev.SlotKind > cur.SlotKind) {
			t.Fatalf("AggregateSince ordering violated at index %d: prev=%+v cur=%+v", i, prev, cur)
		}
	}

	find := func(c string, s SlotKind) (FeedbackAggregate, bool) {
		for _, a := range agg {
			if a.ConceptID == c && a.SlotKind == s {
				return a, true
			}
		}
		return FeedbackAggregate{}, false
	}
	aAPV, ok := find("concept-A", media.SlotPrimaryVideo)
	if !ok {
		t.Fatalf("missing aggregate for (concept-A, primary_video)")
	}
	if aAPV.AcceptedN != 1 || aAPV.RejectedN != 1 || aAPV.SuccessN != 0 {
		t.Fatalf("(concept-A, primary_video) counts = %+v, want {AcceptedN=1 RejectedN=1 SuccessN=0}", aAPV)
	}
	if aAPV.AvgScore != 0.0 {
		t.Fatalf("(concept-A, primary_video) AvgScore = %v, want 0.0", aAPV.AvgScore)
	}

	aASI, ok := find("concept-A", media.SlotSecondaryImage)
	if !ok {
		t.Fatalf("missing aggregate for (concept-A, secondary_image)")
	}
	if aASI.AcceptedN != 0 || aASI.RejectedN != 0 || aASI.SuccessN != 1 {
		t.Fatalf("(concept-A, secondary_image) counts = %+v, want {AcceptedN=0 RejectedN=0 SuccessN=1}", aASI)
	}
	if aASI.AvgScore != 1.0 {
		t.Fatalf("(concept-A, secondary_image) AvgScore = %v, want 1.0", aASI.AvgScore)
	}

	aBPV, ok := find("concept-B", media.SlotPrimaryVideo)
	if !ok {
		t.Fatalf("missing aggregate for (concept-B, primary_video)")
	}
	if aBPV.AcceptedN != 2 || aBPV.RejectedN != 0 || aBPV.SuccessN != 0 {
		t.Fatalf("(concept-B, primary_video) counts = %+v, want {AcceptedN=2 RejectedN=0 SuccessN=0}", aBPV)
	}
	if aBPV.AvgScore != 1.0 {
		t.Fatalf("(concept-B, primary_video) AvgScore = %v, want 1.0", aBPV.AvgScore)
	}
}

// TestFeedbackAggregateSinceUsesRawEventCountForAvgScore pins
// the SPEC contract: AvgScore divides by the raw event count
// (totalN), NOT by the sum of boolean counters. A
// used_successfully event stamps BOTH Selected=true AND
// RenderCompleted=true; without the totalN fix, that single
// event would inflate the denominator and depress AvgScore.
func TestFeedbackAggregateSinceUsesRawEventCountForAvgScore(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)
	b := seedFeedbackBinding(bindings, 0.0, 0)
	b.ConceptID = "concept-A"
	b.SlotKind = media.SlotPrimaryVideo
	bindings.byID[b.ID] = b

	now := newFixedClock().Now()
	usage.events = []UsageEvent{
		{ProjectID: "p-1", ConceptID: "concept-A", SlotKind: media.SlotPrimaryVideo,
			AssetID: b.AssetID, BindingID: b.ID,
			Selected: true, RenderCompleted: true,
			CreatedAt: now},
	}

	agg, err := svc.AggregateSince(context.Background(), "")
	if err != nil {
		t.Fatalf("AggregateSince returned error: %v", err)
	}
	if len(agg) != 1 {
		t.Fatalf("AggregateSince returned %d aggregates, want 1", len(agg))
	}
	if agg[0].AvgScore != 2.0 {
		t.Fatalf("AvgScore = %v, want 2.0 (raw event count denominator pins used_successfully's full weight)",
			agg[0].AvgScore)
	}
}

// TestFeedbackAggregateSinceFiltersBySinceTimestamp pins the
// ranker warm-up read contract: events with CreatedAt < `since`
// are excluded from the bounded slice and therefore from the
// aggregate.
func TestFeedbackAggregateSinceFiltersBySinceTimestamp(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)

	b := seedFeedbackBinding(bindings, 0.0, 0)
	b.ConceptID = "concept-A"
	b.SlotKind = media.SlotPrimaryVideo
	bindings.byID[b.ID] = b

	cutoff := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	usage.events = []UsageEvent{
		{ProjectID: "p-1", ConceptID: "concept-A", SlotKind: media.SlotPrimaryVideo,
			AssetID: b.AssetID, BindingID: b.ID,
			Selected:  true,
			CreatedAt: cutoff.Add(-48 * time.Hour)},
		{ProjectID: "p-1", ConceptID: "concept-A", SlotKind: media.SlotPrimaryVideo,
			AssetID: b.AssetID, BindingID: b.ID,
			Selected:  true,
			CreatedAt: cutoff.Add(1 * time.Hour)},
	}

	agg, err := svc.AggregateSince(context.Background(), cutoff.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("AggregateSince returned error: %v", err)
	}
	if len(agg) != 1 {
		t.Fatalf("AggregateSince returned %d aggregates, want 1 (after-since filter)", len(agg))
	}
	if agg[0].AcceptedN != 1 {
		t.Fatalf("AcceptedN = %d, want 1 (only the after-cutoff event)", agg[0].AcceptedN)
	}
}

// TestFeedbackAggregateSinceInvalidTimestampRejects pins the
// fail-closed envelope for malformed `since` inputs via the
// canonical ErrInvalidAggregateSince sentinel.
func TestFeedbackAggregateSinceInvalidTimestampRejects(t *testing.T) {
	t.Parallel()
	svc, _, _ := defaultFeedbackSvc(t)
	_, err := svc.AggregateSince(context.Background(), "not-a-real-timestamp")
	if err == nil {
		t.Fatalf("AggregateSince accepted malformed `since` input")
	}
	if !errors.Is(err, ErrInvalidAggregateSince) {
		t.Fatalf("AggregateSince returned %v, want wrapped ErrInvalidAggregateSince", err)
	}
}

// TestFeedbackAggregateSinceEmptySinceReturnsAll pins the
// post-deploy full warm-up path: an empty `since` is treated
// as "no lower bound" (godlike/06 SSOT for full warm-up).
func TestFeedbackAggregateSinceEmptySinceReturnsAll(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)
	b := seedFeedbackBinding(bindings, 0.0, 0)
	bindings.byID[b.ID] = b

	usage.events = []UsageEvent{
		{ProjectID: "p-1", ConceptID: b.ConceptID, SlotKind: b.SlotKind,
			AssetID: b.AssetID, BindingID: b.ID,
			Selected:  true,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ProjectID: "p-1", ConceptID: b.ConceptID, SlotKind: b.SlotKind,
			AssetID: b.AssetID, BindingID: b.ID,
			Selected:  true,
			CreatedAt: time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)},
		{ProjectID: "p-1", ConceptID: b.ConceptID, SlotKind: b.SlotKind,
			AssetID: b.AssetID, BindingID: b.ID,
			RenderCompleted: true,
			CreatedAt:       time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)},
	}

	agg, err := svc.AggregateSince(context.Background(), "")
	if err != nil {
		t.Fatalf("AggregateSince returned error: %v", err)
	}
	if len(agg) != 1 {
		t.Fatalf("AggregateSince returned %d aggregates, want 1", len(agg))
	}
	if agg[0].AcceptedN != 2 || agg[0].SuccessN != 1 {
		t.Fatalf("counts = %+v, want {AcceptedN=2 SuccessN=1}", agg[0])
	}
	if agg[0].LastUsedAt != "2026-07-21T00:00:00Z" {
		t.Fatalf("LastUsedAt = %q, want %q", agg[0].LastUsedAt, "2026-07-21T00:00:00Z")
	}
}

// TestFeedbackAggregateSinceLastUsedAtMaxTimestamp pins the
// LastUsedAt field contract: it must be the maximum CreatedAt
// across all events in the (concept, slot) group, formatted
// as RFC3339.
func TestFeedbackAggregateSinceLastUsedAtMaxTimestamp(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)
	b := seedFeedbackBinding(bindings, 0.0, 0)
	bindings.byID[b.ID] = b

	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	usage.events = []UsageEvent{
		{ProjectID: "p-1", ConceptID: b.ConceptID, SlotKind: b.SlotKind,
			AssetID: b.AssetID, BindingID: b.ID,
			Selected: true, CreatedAt: later},
		{ProjectID: "p-1", ConceptID: b.ConceptID, SlotKind: b.SlotKind,
			AssetID: b.AssetID, BindingID: b.ID,
			Selected: true, CreatedAt: earlier},
	}

	agg, err := svc.AggregateSince(context.Background(), "")
	if err != nil {
		t.Fatalf("AggregateSince returned error: %v", err)
	}
	if len(agg) != 1 {
		t.Fatalf("AggregateSince returned %d aggregates, want 1", len(agg))
	}
	if agg[0].LastUsedAt != later.Format(time.RFC3339) {
		t.Fatalf("LastUsedAt = %q, want %q (max CreatedAt)",
			agg[0].LastUsedAt, later.Format(time.RFC3339))
	}
}

// TestFeedbackRecordRejectsInvalidSlotKind pins the SPEC
// contract: a binding with an uncanonical SlotKind surfaces
// wrapped ErrInvalidSlotKind BEFORE any UsageEvent is appended
// or binding Upsert is called.
func TestFeedbackRecordRejectsInvalidSlotKind(t *testing.T) {
	t.Parallel()
	svc, bindings, usage := defaultFeedbackSvc(t)

	b := MediaBinding{
		ID:             "b-invalid-slot-" + uuid.NewString(),
		ConceptID:      testConceptID,
		AssetID:        testAssetID,
		SlotKind:       SlotKind("not_a_real_slot"),
		Origin:         OriginManual,
		ApprovalStatus: ApprovalApproved,
		SuccessScore:   0.0,
		UsageCount:     0,
	}
	bindings.mu.Lock()
	bindings.byID[b.ID] = b
	bindings.mu.Unlock()

	_, err := svc.Record(context.Background(), FeedbackInput{
		ProjectID: "p-1", SceneID: "s-1", BindingID: b.ID, Action: FeedbackAccepted,
	})
	if err == nil {
		t.Fatalf("Record accepted invalid SlotKind binding")
	}
	if !errors.Is(err, ErrInvalidSlotKind) {
		t.Fatalf("Record returned %v, want wrapped ErrInvalidSlotKind", err)
	}
	if len(usage.events) != 0 {
		t.Fatalf("Record must NOT append an event when SlotKind is invalid; got %d events", len(usage.events))
	}
}

// ── Compile-time assertion ─────────────────────────────────────────

var _ UsageRepository = (*fakeUsageRepo)(nil)
