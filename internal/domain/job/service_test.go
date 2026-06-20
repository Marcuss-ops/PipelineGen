package job

import (
	"context"
	"errors"
	"testing"
)

// ── NewUnwiredService ──────────────────────────────────────────────────

func TestNewUnwiredService_AllDelegatesNil(t *testing.T) {
	s := NewUnwiredService()
	if s == nil {
		t.Fatal("NewUnwiredService returned nil")
	}
	if s.EnqueueFn != nil {
		t.Error("EnqueueFn should be nil")
	}
	if s.GetFn != nil {
		t.Error("GetFn should be nil")
	}
	if s.CancelFn != nil {
		t.Error("CancelFn should be nil")
	}
	if s.ListFn != nil {
		t.Error("ListFn should be nil")
	}
	if s.IsTerminalFn != nil {
		t.Error("IsTerminalFn should be nil")
	}
	if s.RegisterHandlerFn != nil {
		t.Error("RegisterHandlerFn should be nil")
	}
	if s.ListEventsFn != nil {
		t.Error("ListEventsFn should be nil")
	}
}

func TestNewUnwiredService_IsNotWired(t *testing.T) {
	s := NewUnwiredService()
	if s.IsWired() {
		t.Error("NewUnwiredService should report IsWired=false")
	}
}

// ── NewService ──────────────────────────────────────────────────────────

func TestNewService_WiresAllDelegates(t *testing.T) {
	enqueueCalled := false
	getCalled := false
	cancelCalled := false
	listCalled := false
	isTerminalCalled := false

	s := NewService(
		func(ctx context.Context, req *EnqueueRequest) (*Job, error) {
			enqueueCalled = true
			return &Job{ID: "j1"}, nil
		},
		func(ctx context.Context, id string) (*Job, error) {
			getCalled = true
			return &Job{ID: id}, nil
		},
		func(ctx context.Context, id string) error {
			cancelCalled = true
			return nil
		},
		func(ctx context.Context, filter Filter) ([]*Job, error) {
			listCalled = true
			return nil, nil
		},
		func(status Status) bool {
			isTerminalCalled = true
			return false
		},
	)

	if !s.IsWired() {
		t.Error("NewService should report IsWired=true")
	}

	_, _ = s.Enqueue(context.Background(), &EnqueueRequest{Type: "test"})
	if !enqueueCalled {
		t.Error("Enqueue delegate not called")
	}

	_, _ = s.Get(context.Background(), "test-id")
	if !getCalled {
		t.Error("Get delegate not called")
	}

	_ = s.Cancel(context.Background(), "test-id")
	if !cancelCalled {
		t.Error("Cancel delegate not called")
	}

	_, _ = s.List(context.Background(), Filter{})
	if !listCalled {
		t.Error("List delegate not called")
	}

	_ = s.IsTerminal(StatusQueued)
	if !isTerminalCalled {
		t.Error("IsTerminal delegate not called")
	}
}

// ── SetInner ────────────────────────────────────────────────────────────

func TestSetInner_PopulatesAllDelegates(t *testing.T) {
	s := NewUnwiredService()
	inner := &fakeInnerService{}
	s.SetInner(inner)

	if s.EnqueueFn == nil {
		t.Error("SetInner should set EnqueueFn")
	}
	if s.GetFn == nil {
		t.Error("SetInner should set GetFn")
	}
	if s.CancelFn == nil {
		t.Error("SetInner should set CancelFn")
	}
	if s.ListFn == nil {
		t.Error("SetInner should set ListFn")
	}
	if s.IsTerminalFn == nil {
		t.Error("SetInner should set IsTerminalFn")
	}

	j, err := s.Enqueue(context.Background(), &EnqueueRequest{Type: "test"})
	if err != nil {
		t.Errorf("Enqueue after SetInner: %v", err)
	}
	if j.ID != "inner-job" {
		t.Errorf("expected inner-job, got %s", j.ID)
	}
}

func TestSetInner_NilReceiver(t *testing.T) {
	var s *Service
	inner := &fakeInnerService{}
	s = s.SetInner(inner)
	if s == nil {
		t.Fatal("SetInner on nil receiver should return non-nil service")
	}
	if !s.IsWired() {
		t.Error("SetInner on nil receiver should wire delegates")
	}
}

func TestSetInner_NilInner(t *testing.T) {
	s := NewUnwiredService()
	result := s.SetInner(nil)
	if result.IsWired() {
		t.Error("SetInner with nil inner should not wire")
	}
}

// ── ErrNotWired sentinel ────────────────────────────────────────────────

func TestEnqueue_UnwiredReturnsErrNotWired(t *testing.T) {
	s := NewUnwiredService()
	_, err := s.Enqueue(context.Background(), &EnqueueRequest{Type: "media.artlist"})
	if err == nil {
		t.Fatal("Enqueue on unwired service should return error")
	}
	if !errors.Is(err, ErrNotWired) {
		t.Errorf("expected ErrNotWired, got %v", err)
	}
}

func TestGet_UnwiredReturnsErrNotWired(t *testing.T) {
	s := NewUnwiredService()
	_, err := s.Get(context.Background(), "some-id")
	if err == nil {
		t.Fatal("Get on unwired service should return error")
	}
	if !errors.Is(err, ErrNotWired) {
		t.Errorf("expected ErrNotWired, got %v", err)
	}
}

func TestCancel_UnwiredReturnsErrNotWired(t *testing.T) {
	s := NewUnwiredService()
	err := s.Cancel(context.Background(), "some-id")
	if err == nil {
		t.Fatal("Cancel on unwired service should return error")
	}
	if !errors.Is(err, ErrNotWired) {
		t.Errorf("expected ErrNotWired, got %v", err)
	}
}

func TestList_UnwiredReturnsErrNotWired(t *testing.T) {
	s := NewUnwiredService()
	_, err := s.List(context.Background(), Filter{})
	if err == nil {
		t.Fatal("List on unwired service should return error")
	}
	if !errors.Is(err, ErrNotWired) {
		t.Errorf("expected ErrNotWired, got %v", err)
	}
}

func TestRegisterHandler_UnwiredReturnsErrNotWired(t *testing.T) {
	s := NewUnwiredService()
	err := s.RegisterHandler("test.type", nil)
	if err == nil {
		t.Fatal("RegisterHandler on unwired service should return error")
	}
	if !errors.Is(err, ErrNotWired) {
		t.Errorf("expected ErrNotWired, got %v", err)
	}
}

func TestListEvents_UnwiredReturnsErrNotWired(t *testing.T) {
	s := NewUnwiredService()
	_, err := s.ListEvents(context.Background(), "some-job")
	if err == nil {
		t.Fatal("ListEvents on unwired service should return error")
	}
	if !errors.Is(err, ErrNotWired) {
		t.Errorf("expected ErrNotWired, got %v", err)
	}
}

// ── IsTerminal fallback ────────────────────────────────────────────────

func TestIsTerminal_FallbackWhenUnwired(t *testing.T) {
	s := NewUnwiredService()
	if !s.IsTerminal(StatusSucceeded) {
		t.Error("IsTerminal should use Status.IsTerminal() fallback when unwired")
	}
	if s.IsTerminal(StatusQueued) {
		t.Error("IsTerminal should use Status.IsTerminal() fallback when unwired (queued is not terminal)")
	}
}

// ── SetRegisterHandler / SetListEvents chaining ─────────────────────────

func TestSetRegisterHandler_WiresAndReturnsSelf(t *testing.T) {
	s := NewUnwiredService()
	called := false
	result := s.SetRegisterHandler(func(jobType string, handler any) error {
		called = true
		return nil
	})
	if result != s {
		t.Error("SetRegisterHandler should return receiver for chaining")
	}
	if err := s.RegisterHandler("test.type", struct{}{}); err != nil {
		t.Errorf("RegisterHandler after SetRegisterHandler: %v", err)
	}
	if !called {
		t.Error("RegisterHandler delegate not called")
	}
}

func TestSetListEvents_WiresAndReturnsSelf(t *testing.T) {
	s := NewUnwiredService()
	result := s.SetListEvents(func(ctx context.Context, jobID string) ([]Event, error) {
		return []Event{{ID: "e1", JobID: jobID}}, nil
	})
	if result != s {
		t.Error("SetListEvents should return receiver for chaining")
	}
	events, err := s.ListEvents(context.Background(), "job-1")
	if err != nil {
		t.Errorf("ListEvents after SetListEvents: %v", err)
	}
	if len(events) != 1 || events[0].JobID != "job-1" {
		t.Error("ListEvents returned unexpected result")
	}
}

func TestSetRegisterHandler_NilReceiver(t *testing.T) {
	var s *Service
	result := s.SetRegisterHandler(func(jobType string, handler any) error { return nil })
	if result == nil {
		t.Fatal("SetRegisterHandler on nil receiver should return non-nil service")
	}
	if result.RegisterHandlerFn == nil {
		t.Error("SetRegisterHandler on nil receiver should wire the delegate")
	}
}

func TestSetListEvents_NilReceiver(t *testing.T) {
	var s *Service
	result := s.SetListEvents(func(ctx context.Context, jobID string) ([]Event, error) {
		return nil, nil
	})
	if result == nil {
		t.Fatal("SetListEvents on nil receiver should return non-nil service")
	}
	if result.ListEventsFn == nil {
		t.Error("SetListEvents on nil receiver should wire the delegate")
	}
}

// ── Nil receiver safety ─────────────────────────────────────────────────

func TestEnqueue_NilReceiver(t *testing.T) {
	var s *Service
	_, err := s.Enqueue(context.Background(), &EnqueueRequest{Type: "test"})
	if err == nil {
		t.Fatal("Enqueue on nil receiver should return error")
	}
	if !errors.Is(err, ErrNotWired) {
		t.Errorf("expected ErrNotWired, got %v", err)
	}
}

func TestIsTerminal_NilReceiver(t *testing.T) {
	var s *Service
	// nil receiver with IsTerminalFn nil should fall back to Status.IsTerminal
	if !s.IsTerminal(StatusSucceeded) {
		t.Error("nil Service should fall back to Status.IsTerminal")
	}
}

// ── EnqueueRequest fields ───────────────────────────────────────────────

func TestEnqueueRequest_AllFields(t *testing.T) {
	req := &EnqueueRequest{
		Type:          "media.artlist",
		Payload:       map[string]string{"key": "val"},
		CorrelationID: "corr-123",
		MaxRetries:    3,
		Priority:      1,
		Project:       "my-project",
		ActiveKey:     "ak-456",
		VideoName:     "test-video.mp4",
	}
	if req.Type != "media.artlist" {
		t.Error("Type field mismatch")
	}
	if req.Project != "my-project" {
		t.Error("Project field mismatch")
	}
	if req.ActiveKey != "ak-456" {
		t.Error("ActiveKey field mismatch")
	}
	if req.VideoName != "test-video.mp4" {
		t.Error("VideoName field mismatch")
	}
	if req.Priority != 1 {
		t.Error("Priority field mismatch")
	}
	if req.CorrelationID != "corr-123" {
		t.Error("CorrelationID field mismatch")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────

type fakeInnerService struct{}

func (f *fakeInnerService) Enqueue(ctx context.Context, req *EnqueueRequest) (*Job, error) {
	return &Job{ID: "inner-job", Type: req.Type}, nil
}
func (f *fakeInnerService) Get(ctx context.Context, id string) (*Job, error) {
	return &Job{ID: id}, nil
}
func (f *fakeInnerService) Cancel(ctx context.Context, id string) error { return nil }
func (f *fakeInnerService) List(ctx context.Context, filter Filter) ([]*Job, error) {
	return nil, nil
}
func (f *fakeInnerService) IsTerminal(status Status) bool { return status.IsTerminal() }
