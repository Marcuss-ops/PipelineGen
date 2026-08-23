// Package scripts_test — generate_many_usecase_test.go exercises
// GenerateManyUseCase.ExecuteFanout (fan-out path).
package usecase_test

import (
	"context"
	"errors"
	"testing"

	scripts "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// ── Stub broker ────────────────────────────────────────────────────

type stubBroker struct {
	enqueueFunc func(ctx context.Context, parentJobID string, itemIndex int, item scriptpkg.GenerationItemV2, preset scriptpkg.Preset) (string, error)
}

func (s *stubBroker) EnqueueScriptItem(ctx context.Context, parentJobID string, itemIndex int, item scriptpkg.GenerationItemV2, preset scriptpkg.Preset) (string, error) {
	return s.enqueueFunc(ctx, parentJobID, itemIndex, item, preset)
}

var _ scripts.FanoutItemBroker = (*stubBroker)(nil)

// ── Helpers ────────────────────────────────────────────────────────

func makeManyEnv(items ...scriptpkg.GenerationItemV2) *scriptpkg.GenerationEnvelopeV2 {
	return &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items:   items,
	}
}

func makeItem(id string) scriptpkg.GenerationItemV2 {
	return scriptpkg.GenerationItemV2{
		ID: id,
		Source: scriptpkg.SourceSpec{
			Type:  scriptpkg.SourceText,
			Topic: "test topic " + id,
		},
	}
}

func newWiredUC(broker scripts.FanoutItemBroker) *scripts.GenerateManyUseCase {
	uc := scripts.NewGenerateManyUseCase(zap.NewNop())
	uc.SetFanoutBroker(broker)
	return uc
}

// ── Tests ──────────────────────────────────────────────────────────

func TestExecuteFanout_EmptyEnvelope(t *testing.T) {
	t.Parallel()
	uc := newWiredUC(&stubBroker{
		enqueueFunc: func(ctx context.Context, parentJobID string, itemIndex int, item scriptpkg.GenerationItemV2, preset scriptpkg.Preset) (string, error) {
			return "job-1", nil
		},
	})
	result, err := uc.ExecuteFanout(context.Background(), "parent-1", makeManyEnv())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalItems != 0 {
		t.Errorf("expected 0 total, got %d", result.TotalItems)
	}
}

func TestExecuteFanout_NilEnvelope(t *testing.T) {
	t.Parallel()
	uc := newWiredUC(&stubBroker{})
	result, err := uc.ExecuteFanout(context.Background(), "parent-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalItems != 0 {
		t.Errorf("expected 0 total, got %d", result.TotalItems)
	}
}

func TestExecuteFanout_NilUseCase(t *testing.T) {
	t.Parallel()
	var uc *scripts.GenerateManyUseCase
	_, err := uc.ExecuteFanout(context.Background(), "parent-1", makeManyEnv(makeItem("a")))
	if err == nil {
		t.Fatal("expected error for nil use case")
	}
}

func TestExecuteFanout_BrokerNotWired(t *testing.T) {
	t.Parallel()
	uc := scripts.NewGenerateManyUseCase(zap.NewNop())
	_, err := uc.ExecuteFanout(context.Background(), "parent-1", makeManyEnv(makeItem("a")))
	if err == nil {
		t.Fatal("expected error for missing broker")
	}
}

func TestExecuteFanout_AllSucceed(t *testing.T) {
	t.Parallel()
	nextID := 0
	uc := newWiredUC(&stubBroker{
		enqueueFunc: func(ctx context.Context, parentJobID string, itemIndex int, item scriptpkg.GenerationItemV2, preset scriptpkg.Preset) (string, error) {
			nextID++
			return "child-" + string(rune('0'+nextID)), nil
		},
	})
	result, err := uc.ExecuteFanout(context.Background(), "parent-1",
		makeManyEnv(makeItem("a"), makeItem("b"), makeItem("c")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalItems != 3 {
		t.Errorf("TotalItems: got %d, want 3", result.TotalItems)
	}
	if result.FailedEnqueueCount != 0 {
		t.Errorf("FailedEnqueueCount: got %d, want 0", result.FailedEnqueueCount)
	}
	if result.TotalEnqueued != 3 {
		t.Errorf("TotalEnqueued: got %d, want 3", result.TotalEnqueued)
	}
	if len(result.ChildJobIDs) != 3 {
		t.Errorf("ChildJobIDs len: got %d, want 3", len(result.ChildJobIDs))
	}
	for i, id := range result.ChildJobIDs {
		if id == "" {
			t.Errorf("ChildJobIDs[%d] is empty", i)
		}
	}
}

func TestExecuteFanout_AllFailed(t *testing.T) {
	t.Parallel()
	uc := newWiredUC(&stubBroker{
		enqueueFunc: func(ctx context.Context, parentJobID string, itemIndex int, item scriptpkg.GenerationItemV2, preset scriptpkg.Preset) (string, error) {
			return "", errors.New("stub enqueue failure")
		},
	})
	_, err := uc.ExecuteFanout(context.Background(), "parent-1",
		makeManyEnv(makeItem("a"), makeItem("b")))
	if err == nil {
		t.Fatal("expected error when all enqueues fail")
	}
}

func TestExecuteFanout_PartialFailure(t *testing.T) {
	t.Parallel()
	failCount := 0
	uc := newWiredUC(&stubBroker{
		enqueueFunc: func(ctx context.Context, parentJobID string, itemIndex int, item scriptpkg.GenerationItemV2, preset scriptpkg.Preset) (string, error) {
			failCount++
			if failCount == 2 {
				return "", errors.New("stub enqueue failure")
			}
			return "child-ok", nil
		},
	})
	result, err := uc.ExecuteFanout(context.Background(), "parent-1",
		makeManyEnv(makeItem("a"), makeItem("b"), makeItem("c")))
	if err != nil {
		t.Fatalf("unexpected error on partial failure: %v", err)
	}
	if result.TotalItems != 3 {
		t.Errorf("TotalItems: got %d, want 3", result.TotalItems)
	}
	if result.FailedEnqueueCount != 1 {
		t.Errorf("FailedEnqueueCount: got %d, want 1", result.FailedEnqueueCount)
	}
	if result.TotalEnqueued != 2 {
		t.Errorf("TotalEnqueued: got %d, want 2", result.TotalEnqueued)
	}
	// Failed enqueue slot must be empty.
	emptyCount := 0
	for _, id := range result.ChildJobIDs {
		if id == "" {
			emptyCount++
		}
	}
	if emptyCount != 1 {
		t.Errorf("expected 1 empty ChildJobID (failed enqueue), got %d", emptyCount)
	}
}

func TestExecuteFanout_CtxCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	uc := newWiredUC(&stubBroker{
		enqueueFunc: func(ctx context.Context, parentJobID string, itemIndex int, item scriptpkg.GenerationItemV2, preset scriptpkg.Preset) (string, error) {
			return "child-ok", nil
		},
	})
	_, err := uc.ExecuteFanout(ctx, "parent-1",
		makeManyEnv(makeItem("a"), makeItem("b")))
	if err == nil {
		t.Fatal("expected error on cancelled context (all enqueues skipped)")
	}
}
