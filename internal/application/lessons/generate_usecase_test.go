package lessons

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"go.uber.org/zap"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	lessonsService "github.com/Marcuss-ops/PipelineGen/internal/media/lessons"
)

// ────────────────────────────────────────────────────────────────────
// 4-arm test matrix for the lessons GenerateLessonUseCase.
// Same shape as the books process_usecase_test.go matrix:
//
//   arm-1 sync validation   : request.Validate() (covered below)
//   arm-2 sync ok           : Handle() with FakeLessonProcessor returning success
//   arm-3 async ok          : Handle() with FakeAsyncEnqueuer returning a Job
//   arm-4 async enqueue-fail: Handle() with FakeAsyncEnqueuer returning error
//                              → mapped to 503 (ErrEnqueueFailed)
//
// Plus two construction-time checks (EmptyLessonsService /
// EmptyJobsSystem) and a standalone ErrMapper test for separation
// of concerns. Future migrations of async+sync endpoints can copy
// this matrix verbatim.
// ────────────────────────────────────────────────────────────────────

// FakeLessonProcessor is the consumer-side fake for the lessonProcessor
// interface. Zero value returns nil + no error.
type FakeLessonProcessor struct {
	Result *lessonsService.LessonResult
	Err    error

	LastRequest *lessonsService.LessonRequest
}

// GenerateLesson implements lessonProcessor.
func (f *FakeLessonProcessor) GenerateLesson(_ context.Context, req *lessonsService.LessonRequest) (*lessonsService.LessonResult, error) {
	f.LastRequest = req
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Result, nil
}

// FakeAsyncEnqueuer is the consumer-side fake for asyncEnqueuer.
// Zero value returns nil + no error.
type FakeAsyncEnqueuer struct {
	Job *jobs.Job
	Err error

	LastRequest *jobs.EnqueueRequest
}

// Enqueue implements asyncEnqueuer.
func (f *FakeAsyncEnqueuer) Enqueue(_ context.Context, req *jobs.EnqueueRequest) (*jobs.Job, error) {
	f.LastRequest = req
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Job, nil
}

// TestGenerateLessonRequest_Validate — arm-1 (sync validation).
func TestGenerateLessonRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		req     GenerateLessonRequest
		wantErr bool
	}{
		{
			name:    "empty source_text — rejected",
			req:     GenerateLessonRequest{Title: "intro"},
			wantErr: true,
		},
		{
			name:    "with source_text — accepted",
			req:     GenerateLessonRequest{SourceText: "long enough source to pass validation"},
			wantErr: false,
		},
		{
			name:    "whitespace source_text — rejected",
			req:     GenerateLessonRequest{SourceText: "   \n\t"},
			wantErr: true,
		},
		{
			name:    "async flag does not bypass source_text check",
			req:     GenerateLessonRequest{Async: true},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
		})
	}
}

// TestGenerateLessonUseCase_HandleSyncOK — arm-2 (sync happy path).
func TestGenerateLessonUseCase_HandleSyncOK(t *testing.T) {
	fakeSvc := &FakeLessonProcessor{
		Result: &lessonsService.LessonResult{
			Success:      true,
			Title:        "Recap: kubernetes networking",
			Language:     "en",
			TotalWords:   2500,
			MarkdownPath: "/tmp/lesson.md",
			PDFPath:      "/tmp/lesson.pdf",
			GeneratedAt:  "2026-06-20T12:34:56Z",
		},
	}
	uc := NewGenerateLessonUseCase(fakeSvc, &FakeAsyncEnqueuer{}, zap.NewNop())

	resp, err := uc.Handle(context.Background(), GenerateLessonRequest{
		SourceText:     "Kubernetes networking covers pod-to-pod, pod-to-service...",
		Title:          "kubernetes networking",
		MaxChapters:    5,
		GenerateImages: true,
		GeneratePDF:    true,
		Async:          false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.OK || !resp.Success {
		t.Fatalf("expected OK+Success, got: %+v", resp)
	}
	if resp.Title != "Recap: kubernetes networking" {
		t.Fatalf("title not projected: got %q", resp.Title)
	}
	if resp.TotalWords != 2500 || resp.MarkdownPath != "/tmp/lesson.md" || resp.PDFPath != "/tmp/lesson.pdf" {
		t.Fatalf("sync fields not projected: %+v", resp)
	}
	if resp.Enqueued || resp.JobID != "" {
		t.Fatalf("async fields must be empty on sync, got: %+v", resp)
	}
}

// TestGenerateLessonUseCase_HandleAsyncOK — arm-3 (async happy path).
func TestGenerateLessonUseCase_HandleAsyncOK(t *testing.T) {
	fakeJobs := &FakeAsyncEnqueuer{
		Job: &jobs.Job{ID: "job-lesson-456"},
	}
	uc := NewGenerateLessonUseCase(&FakeLessonProcessor{}, fakeJobs, zap.NewNop())

	resp, err := uc.Handle(context.Background(), GenerateLessonRequest{
		SourceText:  "Kubernetes networking covers pod-to-pod, pod-to-service...",
		Title:       "kubernetes networking",
		MaxChapters: 5,
		Async:       true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.OK || !resp.Enqueued {
		t.Fatalf("expected OK+Enqueued=true, got: ok=%v enqueued=%v", resp.OK, resp.Enqueued)
	}
	if resp.JobID != "job-lesson-456" {
		t.Fatalf("JobID mismatch: got %q", resp.JobID)
	}
	if fakeJobs.LastRequest == nil {
		t.Fatalf("asyncEnqueuer was not called")
	}
	if fakeJobs.LastRequest.Type != string(jobs.TypeLessonsProcess) {
		t.Fatalf("EnqueueRequest.Type = %q, want %q", fakeJobs.LastRequest.Type, jobs.TypeLessonsProcess)
	}
	if fakeJobs.LastRequest.Priority != generateLessonEnqueuePriority {
		t.Fatalf("EnqueueRequest.Priority = %d, want %d", fakeJobs.LastRequest.Priority, generateLessonEnqueuePriority)
	}
	// sync fields must be empty on async ack
	if resp.Success || resp.MarkdownPath != "" {
		t.Fatalf("sync fields must be empty on async, got: %+v", resp)
	}
}

// TestGenerateLessonUseCase_HandleAsyncFailure — arm-4 (async enqueue failure).
func TestGenerateLessonUseCase_HandleAsyncFailure(t *testing.T) {
	fakeJobs := &FakeAsyncEnqueuer{
		Err: errors.New("connection refused"),
	}
	uc := NewGenerateLessonUseCase(&FakeLessonProcessor{}, fakeJobs, zap.NewNop())

	resp, err := uc.Handle(context.Background(), GenerateLessonRequest{
		SourceText: "Kubernetes networking...",
		Async:      true,
	})
	if err == nil {
		t.Fatalf("expected error, got resp=%+v", resp)
	}
	// Gate that the use case actually called Enqueue before returning
	// the wrapped error — without this, a future refactor that
	// short-circuits the async branch would silently degrade.
	if fakeJobs.LastRequest == nil {
		t.Fatalf("Enqueue was not called before error return")
	}
	if !errors.Is(err, ErrEnqueueFailed) {
		t.Fatalf("expected err to wrap ErrEnqueueFailed, got: %v", err)
	}
	if fakeJobs.LastRequest.Type != string(jobs.TypeLessonsProcess) {
		t.Fatalf("EnqueueRequest.Type = %q, want %q (enqueue was not called with the right type)", fakeJobs.LastRequest.Type, jobs.TypeLessonsProcess)
	}
	status, msg := GenerateLessonErrMapper(err)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d (msg=%q)", status, msg)
	}
	if msg != "job enqueue failed" {
		t.Fatalf("msg: want %q, got %q", "job enqueue failed", msg)
	}
}

// TestGenerateLessonUseCase_EmptyJobsSystem — covers ErrJobsSystemUnavailable.
func TestGenerateLessonUseCase_EmptyJobsSystem(t *testing.T) {
	uc := NewGenerateLessonUseCase(&FakeLessonProcessor{}, nil, zap.NewNop())
	_, err := uc.Handle(context.Background(), GenerateLessonRequest{
		SourceText: "Kubernetes networking...",
		Async:      true,
	})
	if !errors.Is(err, ErrJobsSystemUnavailable) {
		t.Fatalf("want ErrJobsSystemUnavailable, got %v", err)
	}
	status, _ := GenerateLessonErrMapper(err)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d", status)
	}
}

// TestGenerateLessonUseCase_EmptyLessonsService — covers ErrLessonsServiceUnavailable.
func TestGenerateLessonUseCase_EmptyLessonsService(t *testing.T) {
	uc := NewGenerateLessonUseCase(nil, &FakeAsyncEnqueuer{}, zap.NewNop())
	_, err := uc.Handle(context.Background(), GenerateLessonRequest{
		SourceText: "Kubernetes networking...",
		Async:      false,
	})
	if !errors.Is(err, ErrLessonsServiceUnavailable) {
		t.Fatalf("want ErrLessonsServiceUnavailable, got %v", err)
	}
	status, _ := GenerateLessonErrMapper(err)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d", status)
	}
}

// TestGenerateLessonErrMapper — pin the use-case → HTTP status mapping.
// Three sentinel cases + ErrGenerateFailed + pass-through (0/"")
// delegates to transport.JSON's safe default.
func TestGenerateLessonErrMapper(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantMsgSub string
	}{
		{
			name:       "lessons service unavailable → 503",
			err:        ErrLessonsServiceUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantMsgSub: "lessons service",
		},
		{
			name:       "job system unavailable → 503",
			err:        ErrJobsSystemUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantMsgSub: "job system",
		},
		{
			name:       "enqueue failure → 503 (load-bearing arm-4)",
			err:        ErrEnqueueFailed,
			wantStatus: http.StatusServiceUnavailable,
			wantMsgSub: "enqueue",
		},
		{
			name:       "ErrGenerateFailed → 500 with original message",
			err:        ErrGenerateFailed{Message: "all chapters failed"},
			wantStatus: http.StatusInternalServerError,
			wantMsgSub: "all chapters failed",
		},
		{
			name:       "unknown error → 0 (delegated to transport default)",
			err:        errors.New("unmapped"),
			wantStatus: 0,
			wantMsgSub: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, msg := GenerateLessonErrMapper(tc.err)
			if status != tc.wantStatus {
				t.Fatalf("status: want %d, got %d", tc.wantStatus, status)
			}
			if tc.wantMsgSub != "" && !contains(msg, tc.wantMsgSub) {
				t.Fatalf("msg: want substring %q, got %q", tc.wantMsgSub, msg)
			}
		})
	}
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
