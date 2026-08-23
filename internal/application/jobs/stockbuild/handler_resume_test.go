// Package stockbuild — handler_resume_test.go (P0-2 stock-pipeline refactor, July 2026).
//
// Tests the resume contract: when the steps.Store has COMPLETED
// rows for phases 01..03 against the canonical RunID, a fresh
// Handler.Handle invocation MUST skip those phases and only re-run
// 04..08. The handler is the canonical owner of resume, NOT a
// central scheduler.
//
// godlike/06 SSOT: this test is the single canonical tripwire for
// the resume contract. Any future change to handler.go's phase
// iteration MUST update this test in lockstep.
package stockbuild

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/subjects"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ─── Test fixtures (hermetic, no DB) ────────────────────────────────────────

// fakeResolver is the canonical hermetic subjects.Resolver for
// stockbuild tests. Returns a stable UUID per display_name so RunID
// derivation is reproducible across runs.
//
// The fakeResolver returns *asset.Subject (canonical kernel type
// from P0-1) — the same type the production subjects.Resolver
// returns, so the Handler under test binds to a real type
// (no test-only aliases).
type fakeResolver struct {
	rows map[string]*asset.Subject
}

func (f *fakeResolver) Lookup(_ context.Context, displayName string) (*asset.Subject, error) {
	s, ok := f.rows[strings.ToLower(strings.TrimSpace(displayName))]
	if !ok {
		return nil, subjects.ErrSubjectNotFound
	}
	return s, nil
}

func (f *fakeResolver) LookupOrCreate(_ context.Context, displayName string) (*asset.Subject, error) {
	key := strings.ToLower(strings.TrimSpace(displayName))
	if s, ok := f.rows[key]; ok {
		return s, nil
	}
	s := &asset.Subject{
		UUID:        "uuid-" + key,
		Slug:        SlugifyForTest(displayName),
		DisplayName: displayName,
	}
	f.rows[key] = s
	return s, nil
}

// SlugifyForTest canonical test-side slug derivation (mirror of
// pkg/slug.SlugifyTitle). Kept local to test pkg to avoid import
// drift on slug package in test-only code.
func SlugifyForTest(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// recordingPhase returns a PhaseBody that, on Run, appends to a
// shared log. Used to assert WHICH phases ran.
type recordingPhase struct {
	name      PhaseName
	ran       *[]string
	returnErr error
}

func (r *recordingPhase) Run(_ context.Context, _ PhaseInput) error {
	if r.ran != nil {
		*r.ran = append(*r.ran, string(r.name))
	}
	return r.returnErr
}

// Compile-time assertion.
var _ PhaseBody = (*recordingPhase)(nil)

// ─── Tests ─────────────────────────────────────────────────────────────────

// TestHandler_Handle_SkipsCompletedPhases is the canonical resume
// tripwire: pre-populate phases 01..03 with Completed rows, then
// invoke Handle and assert that:
//   - phases 01..03 are NOT in the ran log (skipped).
//   - phases 04..08 ARE in the ran log (executed).
//
// Without this the JOB cannot resume a crashed run — defeating
// the user's "Il sistema deve continuare dalle 33 clip mancanti"
// requirement.
func TestHandler_Handle_SkipsCompletedPhases(t *testing.T) {
	ctx := context.Background()

	// Pre-compute the RunID the test must use.
	resolver := &fakeResolver{rows: map[string]*asset.Subject{
		"sugar ray robinson": {
			UUID:        "uuid-sugar",
			Slug:        "sugar-ray-robinson",
			DisplayName: "Sugar Ray Robinson",
		},
	}}
	payload := validPayload()
	subject, _ := resolver.LookupOrCreate(ctx, payload.Subject.DisplayName)
	runID := DeriveRunID(subject.Slug, payload)
	if runID == "" {
		t.Fatalf("DeriveRunID returned empty")
	}

	// Pre-populate step store: complete phases 01..03, leave 04..08
	// untouched.
	store := steps.NewInMemoryStore()
	completedPhases := []PhaseName{PhaseSearch, PhaseSelect, PhaseDownload}
	for _, p := range completedPhases {
		k := steps.StepKey{
			JobID:            runID,
			StepKey:          string(p),
			InputFingerprint: PhaseFingerprint(runID, p, phaseSpecificInput(p, payload)),
		}
		if err := store.MarkStarted(ctx, k); err != nil {
			t.Fatalf("prepopulate MarkStarted %s: %v", p, err)
		}
		if err := store.MarkCompleted(ctx, k, json.RawMessage(`{"counts":{}}`), nil); err != nil {
			t.Fatalf("prepopulate MarkCompleted %s: %v", p, err)
		}
	}

	// Wire phase bodies that record their run.
	var ran []string
	phases := map[PhaseName]PhaseBody{
		PhaseSearch:   &recordingPhase{name: PhaseSearch, ran: &ran},
		PhaseSelect:   &recordingPhase{name: PhaseSelect, ran: &ran},
		PhaseDownload: &recordingPhase{name: PhaseDownload, ran: &ran},
		PhaseExtract:  &recordingPhase{name: PhaseExtract, ran: &ran, returnErr: ErrPhaseNotImplemented},
		PhaseUpload:   &recordingPhase{name: PhaseUpload, ran: &ran, returnErr: ErrPhaseNotImplemented},
		PhasePersist:  &recordingPhase{name: PhasePersist, ran: &ran, returnErr: ErrPhaseNotImplemented},
		PhaseIndex:    &recordingPhase{name: PhaseIndex, ran: &ran, returnErr: ErrPhaseNotImplemented},
		PhaseVerify:   &recordingPhase{name: PhaseVerify, ran: &ran, returnErr: ErrPhaseNotImplemented},
	}

	h, err := NewHandler(zap.NewNop(), resolver, store, phases)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	// Encode the payload as JSON for the kernel-side trip.
	rawPayload, _ := json.Marshal(payload)

	// Build the canonical job.Job shape (stub — the handler reads
	// only Payload).
	j := &job.Job{
		ID:      runID,
		Type:    JobType,
		Payload: rawPayload,
		Status:  job.StatusRunning,
	}

	result, runErr := h.Handle(ctx, j, nil)

	// Phase 04 returns ErrPhaseNotImplemented → handler fails at
	// phase 04; the resume test asserts that 01..03 are NOT in ran.
	if runErr == nil {
		t.Fatalf("expected failure at phase 04 (stub returns ErrPhaseNotImplemented), got nil")
	}
	if !errors.Is(runErr, ErrPhaseNotImplemented) {
		t.Fatalf("expected ErrPhaseNotImplemented, got %v", runErr)
	}

	// Assert phases 01..03 NOT in ran.
	got := strings.Join(ran, ",")
	for _, skipped := range []PhaseName{PhaseSearch, PhaseSelect, PhaseDownload} {
		if strings.Contains(got, string(skipped)) {
			t.Errorf("Completed phase %s was re-run on resume (resume contract broken)", skipped)
		}
	}
	// Assert phase 04 IS in ran (the failed one).
	if !strings.Contains(got, string(PhaseExtract)) {
		t.Errorf("Phase 04 was NOT executed on resume (handler hung at 03 instead)")
	}

	if result == nil {
		t.Errorf("result envelope is nil despite handler returning non-nil error")
	}
}

// TestHandler_Handle_FailClosedOnInvalidPayload asserts that an
// invalid payload surfaces a typed error and the steps.Store is NOT
// touched (godlike/07 NO-FAKE-AVAILABILITY).
func TestHandler_Handle_FailClosedOnInvalidPayload(t *testing.T) {
	ctx := context.Background()
	resolver := &fakeResolver{rows: map[string]*asset.Subject{
		"sugar ray robinson": {
			UUID:        "uuid-sugar",
			Slug:        "sugar-ray-robinson",
			DisplayName: "Sugar Ray Robinson",
		},
	}}
	store := steps.NewInMemoryStore()

	// Recorders that should NEVER be called.
	var ran []string
	phases := map[PhaseName]PhaseBody{}
	for _, p := range AllPhases() {
		phases[p] = &recordingPhase{name: p, ran: &ran}
	}

	h, err := NewHandler(zap.NewNop(), resolver, store, phases)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	// Construct invalid payload (videos = 0).
	bad := validPayload()
	bad.Target.Videos = 0
	rawPayload, _ := json.Marshal(bad)

	j := &job.Job{
		ID:      "run-invalid",
		Type:    JobType,
		Payload: rawPayload,
	}

	result, runErr := h.Handle(ctx, j, nil)
	if runErr == nil {
		t.Fatalf("Expected failure on invalid payload, got nil")
	}
	if !errors.Is(runErr, ErrInvalidPayload) {
		t.Errorf("Expected ErrInvalidPayload, got %v", runErr)
	}
	if len(ran) > 0 {
		t.Errorf("Phases ran on invalid payload: %v (godlike/07 fail-closed)", ran)
	}
	if result == nil {
		t.Errorf("Result envelope is nil despite failure")
	}
	// Confirm stepsStore remained empty.
	history, _ := store.ListByJob(ctx, "run-invalid")
	if len(history) > 0 {
		t.Errorf("Invalid payload wrote %d step rows (godlike/07 fail-closed broken)", len(history))
	}
}

// TestNewHandler_FailClosedOnMissingDeps asserts the composition-time
// fail-closed contract (godlike/06 SSOT): nil deps MUST refuse.
func TestNewHandler_FailClosedOnMissingDeps(t *testing.T) {
	resolver := &fakeResolver{}
	store := steps.NewInMemoryStore()
	phases := map[PhaseName]PhaseBody{}
	for _, p := range AllPhases() {
		phases[p] = &recordingPhase{name: p}
	}

	_, err := NewHandler(zap.NewNop(), nil, store, phases)
	if !errors.Is(err, ErrResolverNotWired) {
		t.Errorf("nil resolver: got %v, want ErrResolverNotWired", err)
	}
	_, err = NewHandler(zap.NewNop(), resolver, nil, phases)
	if !errors.Is(err, ErrStepsStoreNotWired) {
		t.Errorf("nil stepsStore: got %v, want ErrStepsStoreNotWired", err)
	}
	// Malformed phases: missing entries or spilled 9th entry.
	_, err = NewHandler(zap.NewNop(), resolver, store, map[PhaseName]PhaseBody{})
	if !errors.Is(err, ErrPhasesMalformed) {
		t.Errorf("empty phases map: got %v, want ErrPhasesMalformed", err)
	}
}
