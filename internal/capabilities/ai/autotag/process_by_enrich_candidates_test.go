// Package autotag — process_by_enrich_candidates_test.go is the FIRST test
// coverage for the sweep selector surface. Before MEDIA-SSOT P2-9 Phase 2 the
// function had no test at all, which is why the surface could drift onto a
// second engine without anything noticing.
package autotag

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/vlm"
)

// stubEnrichCandidates records the fence/limit it was called with so the
// delegation contract is asserted, not assumed.
type stubEnrichCandidates struct {
	ids      []string
	err      error
	gotFence time.Duration
	gotLimit int
	calls    int
}

func (s *stubEnrichCandidates) PendingEnrichCandidates(_ context.Context, claimFence time.Duration, limit int) ([]string, error) {
	s.calls++
	s.gotFence = claimFence
	s.gotLimit = limit
	if s.err != nil {
		return nil, s.err
	}
	return s.ids, nil
}

// stubEnrichStateMachine satisfies enrichment.EnrichStateMachinePort while
// recording nothing: these tests only reach the claim on a non-empty candidate
// set, which the fail-closed cases never do.
type stubEnrichStateMachine struct{}

func (stubEnrichStateMachine) Transition(context.Context, string, asset.EnrichState, asset.EnrichState) error {
	return nil
}
func (stubEnrichStateMachine) MarkPending(context.Context, string) error { return nil }
func (stubEnrichStateMachine) ClaimForEnrichment(context.Context, string, asset.EnrichState) error {
	return errors.New("claim should not be reached in these cases")
}
func (stubEnrichStateMachine) MarkEnriched(context.Context, string) error { return nil }
func (stubEnrichStateMachine) MarkFailed(context.Context, string) error   { return nil }

func enabledVLMClient() *vlm.Client {
	return vlm.NewClient(vlm.Config{Enabled: true, Endpoint: "http://127.0.0.1:1", TimeoutMs: 10})
}

// TestProcessByEnrichCandidates_FailsClosedWithoutCandidateReader pins the
// no-fallback rule. The retired selector read the operational SQLite handle
// unconditionally; with that handle gone, an unwired media reader must fail
// loudly. Reporting an empty sweep would be indistinguishable from "no work",
// and the sweeper would look healthy while blind to every committed asset.
func TestProcessByEnrichCandidates_FailsClosedWithoutCandidateReader(t *testing.T) {
	svc := NewService(ServiceDeps{
		VLMClient:   enabledVLMClient(),
		EnrichState: stubEnrichStateMachine{},
		Log:         zap.NewNop(),
	})

	n, err := svc.ProcessByEnrichCandidates(context.Background(), 10, 30*time.Second)
	if err == nil {
		t.Fatal("expected a fail-closed error when no enrichment candidate reader is wired")
	}
	if n != 0 {
		t.Fatalf("processed = %d, want 0", n)
	}
}

// TestProcessByEnrichCandidates_StillRequiresTheStateMachine pins that the
// pre-existing dependency checks were not reordered by the migration: the
// state machine is still validated before any read is attempted.
func TestProcessByEnrichCandidates_StillRequiresTheStateMachine(t *testing.T) {
	reader := &stubEnrichCandidates{}
	svc := NewService(ServiceDeps{
		VLMClient:        enabledVLMClient(),
		EnrichCandidates: reader,
		Log:              zap.NewNop(),
	})

	if _, err := svc.ProcessByEnrichCandidates(context.Background(), 10, 30*time.Second); err == nil {
		t.Fatal("expected an error when the enrichment state machine is not wired")
	}
	if reader.calls != 0 {
		t.Errorf("candidate reader called %d times, want 0 (the state machine is checked first)", reader.calls)
	}
}

// TestProcessByEnrichCandidates_FailsClosedWithoutVLM pins the outermost
// pre-existing guard, so a future reordering is caught here.
func TestProcessByEnrichCandidates_FailsClosedWithoutVLM(t *testing.T) {
	disabled := vlm.NewClient(vlm.Config{Enabled: false})
	svc := NewService(ServiceDeps{
		VLMClient:   disabled,
		EnrichState: stubEnrichStateMachine{},
		Log:         zap.NewNop(),
	})
	if _, err := svc.ProcessByEnrichCandidates(context.Background(), 10, 30*time.Second); err == nil {
		t.Fatal("expected an error when the VLM client is disabled")
	}
}

// TestProcessByEnrichCandidates_PropagatesReaderError pins that an unreadable
// media SSOT surfaces as an error and is never swallowed into a successful
// zero-work sweep.
func TestProcessByEnrichCandidates_PropagatesReaderError(t *testing.T) {
	cause := errors.New("media SSOT unavailable")
	reader := &stubEnrichCandidates{err: cause}
	svc := NewService(ServiceDeps{
		VLMClient:        enabledVLMClient(),
		EnrichState:      stubEnrichStateMachine{},
		EnrichCandidates: reader,
		Log:              zap.NewNop(),
	})

	_, err := svc.ProcessByEnrichCandidates(context.Background(), 7, 45*time.Second)
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want wrapped %v", err, cause)
	}
	if reader.gotFence != 45*time.Second {
		t.Errorf("fence passed = %v, want 45s (the port receives the fence, not a SQLite modifier string)", reader.gotFence)
	}
	if reader.gotLimit != 7 {
		t.Errorf("limit passed = %d, want 7", reader.gotLimit)
	}
}

// TestProcessByEnrichCandidates_EmptyCandidateSetIsNotAnError pins the contract
// boundary the two cases above sit either side of: a readable catalog with no
// pending work is a successful no-op.
func TestProcessByEnrichCandidates_EmptyCandidateSetIsNotAnError(t *testing.T) {
	reader := &stubEnrichCandidates{ids: nil}
	svc := NewService(ServiceDeps{
		VLMClient:        enabledVLMClient(),
		EnrichState:      stubEnrichStateMachine{},
		EnrichCandidates: reader,
		Log:              zap.NewNop(),
	})

	n, err := svc.ProcessByEnrichCandidates(context.Background(), 10, 30*time.Second)
	if err != nil {
		t.Fatalf("an empty candidate set must not be an error, got %v", err)
	}
	if n != 0 {
		t.Fatalf("processed = %d, want 0", n)
	}
	if reader.calls != 1 {
		t.Errorf("candidate reader called %d times, want 1", reader.calls)
	}
}

// TestProcessByEnrichCandidates_DefaultLimitMatchesTheReader pins that the
// caller-side default is applied before the port is called, so a port
// implementation does not have to guess what "unlimited" meant.
func TestProcessByEnrichCandidates_DefaultLimitMatchesTheReader(t *testing.T) {
	reader := &stubEnrichCandidates{}
	svc := NewService(ServiceDeps{
		VLMClient:        enabledVLMClient(),
		EnrichState:      stubEnrichStateMachine{},
		EnrichCandidates: reader,
		Log:              zap.NewNop(),
	})
	if _, err := svc.ProcessByEnrichCandidates(context.Background(), 0, 30*time.Second); err != nil {
		t.Fatalf("ProcessByEnrichCandidates: %v", err)
	}
	if reader.gotLimit != 10 {
		t.Errorf("limit passed = %d, want the canonical default 10", reader.gotLimit)
	}
}
