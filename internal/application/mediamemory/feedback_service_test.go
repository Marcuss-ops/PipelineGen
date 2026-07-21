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
	if !IsKnownSlotKind(ev.SlotKind) {
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

// ── Test fixtures ───────────────────────────────────────────────────

const (
	testConceptID = "concept-maya-feed"
	testAssetID   = "asset-chichen-itza"
	testSlotKind  = SlotPrimaryVideo
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

func TestFeedbackAggregateSinceReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	svc, _, _ := defaultFeedbackSvc(t)
	_, err := svc.AggregateSince(context.Background(), "2026-01-01T00:00:00Z")
	if err == nil {
		t.Fatalf("AggregateSince should fail-closed per godlike/07 NO-FAKE-AVAILABILITY (Phase 1.x stub)")
	}
	// godlike/06 SSOT: the canonical contract is "this returns a
	// non-nil error and does not silently return []". We do not pin
	// the specific sentinel (errNotImplemented or a typed
	// envelope) — both are acceptable.
}

// ── Compile-time assertion ─────────────────────────────────────────

var _ UsageRepository = (*fakeUsageRepo)(nil)
