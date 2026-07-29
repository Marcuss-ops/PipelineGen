package scripts

import (
	"context"
	"errors"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// stubDispatcher implements DispatcherShim for testing.
type stubDispatcher struct {
	enqueueFn func(ctx context.Context, jobType string, payload any) (*job.Job, error)
}

func (s *stubDispatcher) Enqueue(ctx context.Context, jobType string, payload any) (*job.Job, error) {
	if s.enqueueFn != nil {
		return s.enqueueFn(ctx, jobType, payload)
	}
	return &job.Job{ID: "stub-job-001"}, nil
}

// ── Test 1: Compile-time pin ──────────────────────────────────────────

// var _ SceneImageJobEmitter = (*Emitter)(nil) — verified at compile time.

func TestSceneImageJobEmitter_CompileTimePin(t *testing.T) {
	// The compile-time assertion lives outside any test function
	// at package level so it runs at build time, not test time.
	// We verify it exists by just referencing the type here.
	var _ SceneImageJobEmitter = (*Emitter)(nil)
}

// ── Test 2: Happy path — successful emit ──────────────────────────────

func TestSceneImageJobEmitter_HappyPath(t *testing.T) {
	stub := &stubDispatcher{
		enqueueFn: func(ctx context.Context, jobType string, payload any) (*job.Job, error) {
			if jobType != "images.generate" {
				t.Errorf("expected jobType=images.generate, got %s", jobType)
			}
			sp, ok := payload.(SceneImageJobPayload)
			if !ok {
				t.Fatalf("expected payload type SceneImageJobPayload, got %T", payload)
			}
			if sp.Prompt != "a beautiful sunset" {
				t.Errorf("expected Prompt='a beautiful sunset', got %q", sp.Prompt)
			}
			if sp.ParentJobID != "parent-001" {
				t.Errorf("expected ParentJobID='parent-001', got %q", sp.ParentJobID)
			}
			if sp.SceneIndex != 3 {
				t.Errorf("expected SceneIndex=3, got %d", sp.SceneIndex)
			}
			if sp.CorrelationID != "parent-001:scene:3" {
				t.Errorf("expected CorrelationID='parent-001:scene:3', got %q", sp.CorrelationID)
			}
			return &job.Job{ID: "child-job-042"}, nil
		},
	}

	emitter := NewEmitter(stub)
	if emitter == nil {
		t.Fatal("NewEmitter returned nil for non-nil dispatcher")
	}

	jobID, err := emitter.EmitSceneImageJob(context.Background(), EmitSceneImageCommand{
		ParentJobID: "parent-001",
		ScriptID:    "script-abc",
		SceneIndex:  3,
		Prompt:      "a beautiful sunset",
		Style:       "cinematic",
		Width:       1024,
		Height:      768,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jobID != "child-job-042" {
		t.Errorf("expected jobID='child-job-042', got %q", jobID)
	}
}

// ── Test 3: Nil receiver ──────────────────────────────────────────────

func TestSceneImageJobEmitter_NilReceiver(t *testing.T) {
	var emitter *Emitter
	_, err := emitter.EmitSceneImageJob(context.Background(), EmitSceneImageCommand{
		ParentJobID: "parent-001",
		Prompt:      "test",
	})
	if err == nil {
		t.Fatal("expected error on nil receiver")
	}
}

// ── Test 4: Typed envelope — missing required fields ──────────────────

func TestSceneImageJobEmitter_MissingRequiredFields(t *testing.T) {
	emitter := NewEmitter(&stubDispatcher{})
	if emitter == nil {
		t.Fatal("NewEmitter returned nil")
	}

	// Missing ParentJobID.
	_, err := emitter.EmitSceneImageJob(context.Background(), EmitSceneImageCommand{
		Prompt: "test",
	})
	if err == nil {
		t.Fatal("expected error for missing ParentJobID")
	}
	if !errors.Is(err, ErrEmitMissingParentJobID) {
		t.Errorf("expected ErrEmitMissingParentJobID, got %v", err)
	}

	// Missing Prompt.
	_, err = emitter.EmitSceneImageJob(context.Background(), EmitSceneImageCommand{
		ParentJobID: "parent-001",
	})
	if err == nil {
		t.Fatal("expected error for missing Prompt")
	}
	if !errors.Is(err, ErrEmitMissingPrompt) {
		t.Errorf("expected ErrEmitMissingPrompt, got %v", err)
	}
}

// ── Test 5: Dispatcher error propagation ──────────────────────────────

func TestSceneImageJobEmitter_DispatcherErrorPropagation(t *testing.T) {
	dispatchErr := errors.New("mock dispatch failure")
	stub := &stubDispatcher{
		enqueueFn: func(ctx context.Context, jobType string, payload any) (*job.Job, error) {
			return nil, dispatchErr
		},
	}

	emitter := NewEmitter(stub)
	_, err := emitter.EmitSceneImageJob(context.Background(), EmitSceneImageCommand{
		ParentJobID: "parent-001",
		Prompt:      "test",
	})

	if err == nil {
		t.Fatal("expected error propagation from dispatcher")
	}
	// The error should wrap the dispatch error via %w.
	if !errors.Is(err, dispatchErr) {
		t.Errorf("expected errors.Is(err, dispatchErr), got %v", err)
	}
}

// ── Test 6: CorrelationID override ────────────────────────────────────

func TestSceneImageJobEmitter_CustomCorrelationID(t *testing.T) {
	stub := &stubDispatcher{
		enqueueFn: func(ctx context.Context, jobType string, payload any) (*job.Job, error) {
			sp := payload.(SceneImageJobPayload)
			if sp.CorrelationID != "custom-trace-id" {
				t.Errorf("expected CorrelationID='custom-trace-id', got %q", sp.CorrelationID)
			}
			return &job.Job{ID: "child-job-042"}, nil
		},
	}

	emitter := NewEmitter(stub)
	_, err := emitter.EmitSceneImageJob(context.Background(), EmitSceneImageCommand{
		ParentJobID:   "parent-001",
		Prompt:        "test",
		CorrelationID: "custom-trace-id",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Test 7: NewEmitter nil dispatcher fails closed on emit ────────────

func TestSceneImageJobEmitter_NewEmitterNilDispatcher(t *testing.T) {
	emitter := NewEmitter(nil)
	if emitter == nil {
		t.Fatal("expected an invalid emitter instance for typed error reporting")
	}
	_, err := emitter.EmitSceneImageJob(context.Background(), EmitSceneImageCommand{ParentJobID: "parent", Prompt: "prompt"})
	if err == nil {
		t.Fatal("expected nil dispatcher to return an error")
	}
}
