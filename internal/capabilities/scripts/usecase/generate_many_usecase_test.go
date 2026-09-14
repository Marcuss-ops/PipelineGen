// Package scripts_test — generate_many_usecase_test.go exercises
// GenerateManyUseCase.ExecuteFanout (fan-out path).
package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// ── Stub broker ────────────────────────────────────────────────────

type stubBroker struct {
	mu          sync.Mutex
	enqueueFunc func(ctx context.Context, parentJobID string, itemIndex int, item scriptpkg.GenerationItemV2, preset scriptpkg.Preset) (string, error)
}

func (s *stubBroker) EnqueueScriptItem(ctx context.Context, parentJobID string, itemIndex int, item scriptpkg.GenerationItemV2, preset scriptpkg.Preset) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enqueueFunc(ctx, parentJobID, itemIndex, item, preset)
}

var _ scripts.FanoutItemBroker = (*stubBroker)(nil)

type blockingFanoutBroker struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	active  atomic.Int32
	max     atomic.Int32
}

func (b *blockingFanoutBroker) EnqueueScriptItem(ctx context.Context, _ string, itemIndex int, _ scriptpkg.GenerationItemV2, _ scriptpkg.Preset) (string, error) {
	active := b.active.Add(1)
	for {
		max := b.max.Load()
		if active <= max || b.max.CompareAndSwap(max, active) {
			break
		}
	}
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
		b.active.Add(-1)
		return fmt.Sprintf("child-%d", itemIndex), nil
	case <-ctx.Done():
		b.active.Add(-1)
		return "", ctx.Err()
	}
}

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

func TestExecuteFanout_EnqueueIsBoundedAndParallel(t *testing.T) {
	t.Parallel()
	broker := &blockingFanoutBroker{started: make(chan struct{}), release: make(chan struct{})}
	uc := newWiredUC(broker)
	uc.SetConcurrency(2)

	done := make(chan struct{})
	var result *scripts.FanoutResult
	var err error
	go func() {
		result, err = uc.ExecuteFanout(context.Background(), "parent-1",
			makeManyEnv(makeItem("a"), makeItem("b"), makeItem("c"), makeItem("d")))
		close(done)
	}()

	select {
	case <-broker.started:
	case <-time.After(time.Second):
		t.Fatal("bounded fanout did not start an enqueue")
	}
	deadline := time.Now().Add(time.Second)
	for broker.active.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := broker.active.Load(); got != 2 {
		t.Fatalf("active enqueues = %d, want configured parallelism 2", got)
	}
	if got := broker.max.Load(); got > 2 {
		t.Fatalf("active enqueues exceeded configured parallelism: %d", got)
	}

	close(broker.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("fanout did not finish after releasing broker")
	}
	if err != nil {
		t.Fatalf("ExecuteFanout: %v", err)
	}
	if result == nil || result.TotalEnqueued != 4 {
		t.Fatalf("fanout result = %+v, want four enqueued children", result)
	}
	for i, want := range []string{"child-0", "child-1", "child-2", "child-3"} {
		if result.ChildJobIDs[i] != want {
			t.Fatalf("ChildJobIDs[%d] = %q, want deterministic input order %q", i, result.ChildJobIDs[i], want)
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
