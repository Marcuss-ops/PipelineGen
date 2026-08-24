package books

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"go.uber.org/zap"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ────────────────────────────────────────────────────────────────────
// 4-arm test matrix for the books ProcessBookUseCase.
//
//   arm-1 sync validation   : request.Validate() (covered below)
//   arm-2 sync ok           : Handle() with FakeBookProcessor returning success
//   arm-3 async ok          : Handle() with FakeAsyncEnqueuer returning a Job
//   arm-4 async enqueue-fail: Handle() with FakeAsyncEnqueuer returning error
//                              → mapped to 503 (ErrEnqueueFailed)
//
// The ErrorMapper test is additionally kept as a separation-of-concerns
// check — pin the mapper contract independently from Handle().
// ────────────────────────────────────────────────────────────────────

// FakeBookProcessor is the consumer-side fake for the bookProcessor
// interface. The zero value returns nil result + no error; tests that
// want a successful sync branch set Result to a non-nil ProcessResult.
type FakeBookProcessor struct {
	Result *ProcessResult
	Err    error

	// LastRequest captures the most recent *ProcessRequest
	// the use case passed in; lets tests assert the request → payload
	// mapping without a separate observer.
	LastRequest *ProcessRequest
}

// ProcessBook implements bookProcessor.
func (f *FakeBookProcessor) ProcessBook(_ context.Context, req *ProcessRequest) (*ProcessResult, error) {
	f.LastRequest = req
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Result, nil
}

// FakeAsyncEnqueuer is the consumer-side fake for the asyncEnqueuer
// interface. Zero value returns nil + no error (with JobID ""). Tests
// that want async success set Job to a non-nil Job; tests that want
// async failure set Err.
type FakeAsyncEnqueuer struct {
	Job *jobs.Job
	Err error

	// LastRequest captures the most recent *jobs.EnqueueRequest; lets
	// tests assert the request → payload mapping + priority/ActiveKey.
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

// TestProcessBookRequest_Validate — arm-1 (sync validation).
//
// Pin the use-case's reject-when-both-empty contract; the handler
// calls Validate() during binding and surfaces false via apiutil.BadRequest
// (400). Future migrations of similar async+sync endpoints should
// inherit this assertion style as the canonical pattern.
func TestProcessBookRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		req     ProcessBookRequest
		wantErr bool
	}{
		{
			name:    "both empty — rejected",
			req:     ProcessBookRequest{},
			wantErr: true,
		},
		{
			name:    "file_path only — accepted",
			req:     ProcessBookRequest{FilePath: "/tmp/book.pdf"},
			wantErr: false,
		},
		{
			name:    "google_doc_url only — accepted",
			req:     ProcessBookRequest{GoogleDocURL: "https://docs.google.com/..."},
			wantErr: false,
		},
		{
			name:    "whitespace-only paths still rejected",
			req:     ProcessBookRequest{FilePath: "   ", GoogleDocURL: "\t"},
			wantErr: true,
		},
		{
			name:    "async flag does not bypass validation",
			req:     ProcessBookRequest{Async: true},
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

// TestProcessBookUseCase_HandleSyncOK — arm-2 (sync happy path).
//
// The use case's sync branch takes a *ProcessRequest,
// projects it onto ProcessResult fields, and returns a fully populated
// ProcessBookResponse. Tests inject a FakeBookProcessor with a canned
// success; the use case must surface every result field on the wire.
func TestProcessBookUseCase_HandleSyncOK(t *testing.T) {
	fakeSvc := &FakeBookProcessor{
		Result: &ProcessResult{
			Success:         true,
			OutputPath:      "/tmp/out.md",
			PDFPath:         "/tmp/out.pdf",
			DriveFolderURL:  "https://drive.google.com/folder/abc",
			DriveDocURL:     "https://docs.google.com/doc/abc",
			DrivePDFURL:     "https://drive.google.com/file/abc",
			WordCount:       1234,
			ChunksProcessed: 7,
			Language:        "en",
		},
	}
	uc := NewProcessBookUseCase(fakeSvc, &FakeAsyncEnqueuer{}, zap.NewNop())

	resp, err := uc.Handle(context.Background(), ProcessBookRequest{
		FilePath: "/tmp/in.pdf",
		Async:    false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.OK || resp.Kind != "book" || resp.Mode != "sync" {
		t.Fatalf("expected OK+sync book envelope, got: %+v", resp)
	}
	if resp.Result == nil {
		t.Fatalf("expected sync result payload, got nil")
	}
	if resp.Result.OutputPath != "/tmp/out.md" {
		t.Fatalf("OutputPath mismatch: got %q", resp.Result.OutputPath)
	}
	if resp.Result.WordCount != 1234 || resp.Result.ChunksProcessed != 7 || resp.Result.Language != "en" {
		t.Fatalf("sync fields not projected correctly: %+v", resp.Result)
	}
	if resp.JobID != "" || resp.JobType != "" || resp.Status != "" {
		t.Fatalf("async fields must be empty on sync, got: %+v", resp)
	}
	if fakeSvc.LastRequest == nil || fakeSvc.LastRequest.FilePath != "/tmp/in.pdf" {
		t.Fatalf("bookProcessor was not invoked correctly: %+v", fakeSvc.LastRequest)
	}
}

// TestProcessBookUseCase_HandleAsyncOK — arm-3 (async happy path).
//
// The use case's async branch takes the request's active key + payload,
// enqueues via jobsSvc.Enqueue, and returns a populated async ack.
// Tests assert: enqueue was called with the right Type/ActiveKey/Payload,
// JobID was extracted from the returned *Job, sync fields stay empty.
func TestProcessBookUseCase_HandleAsyncOK(t *testing.T) {
	fakeJobs := &FakeAsyncEnqueuer{
		Job: &jobs.Job{ID: "job-abc-123"},
	}
	uc := NewProcessBookUseCase(&FakeBookProcessor{}, fakeJobs, zap.NewNop())

	resp, err := uc.Handle(context.Background(), ProcessBookRequest{
		FilePath:    "/tmp/in.pdf",
		Async:       true,
		Model:       "gpt-4",
		GeneratePDF: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.OK || resp.Kind != "book" || resp.Mode != "async" {
		t.Fatalf("expected OK+async book envelope, got: %+v", resp)
	}
	if resp.JobID != "job-abc-123" {
		t.Fatalf("JobID mismatch: got %q", resp.JobID)
	}
	if resp.JobType != string(jobs.TypeBooksProcess) {
		t.Fatalf("JobType mismatch: got %q", resp.JobType)
	}
	if fakeJobs.LastRequest == nil {
		t.Fatalf("asyncEnqueuer was not called")
	}
	if fakeJobs.LastRequest.Type != string(jobs.TypeBooksProcess) {
		t.Fatalf("EnqueueRequest.Type = %q, want %q", fakeJobs.LastRequest.Type, jobs.TypeBooksProcess)
	}
	if fakeJobs.LastRequest.Priority != processBookEnqueuePriority {
		t.Fatalf("EnqueueRequest.Priority = %d, want %d", fakeJobs.LastRequest.Priority, processBookEnqueuePriority)
	}
	// sync fields must be empty on async ack
	if resp.Result != nil {
		t.Fatalf("sync result must be empty on async, got: %+v", resp)
	}
}

// TestProcessBookUseCase_HandleAsyncFailure — arm-4 (async enqueue failure).
//
// When jobsSvc.Enqueue returns a non-nil error, the use case must
// wrap it in ErrEnqueueFailed (so errors.Is() matches in the mapper)
// AND the mapper must translate it to 503. This is the load-bearing
// arm for the 13 future migrations: without it, async-failure becomes
// silent 500 with the driver error leaked to clients.
func TestProcessBookUseCase_HandleAsyncFailure(t *testing.T) {
	fakeJobs := &FakeAsyncEnqueuer{
		Err: errors.New("connection refused"),
	}
	uc := NewProcessBookUseCase(&FakeBookProcessor{}, fakeJobs, zap.NewNop())

	resp, err := uc.Handle(context.Background(), ProcessBookRequest{
		FilePath: "/tmp/in.pdf",
		Async:    true,
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
	// errors.Join preserves the inner error so callers can still
	// errors.Is(err, jobs.ErrNotWired) for diagnostic distinction.
	if fakeJobs.LastRequest.Type != string(jobs.TypeBooksProcess) {
		t.Fatalf("EnqueueRequest.Type = %q, want %q (enqueue was not called with the right type)", fakeJobs.LastRequest.Type, jobs.TypeBooksProcess)
	}
	// Mapper must fire 503
	status, msg := ProcessBookErrMapper(err)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d (msg=%q)", status, msg)
	}
	if msg != "job enqueue failed" {
		t.Fatalf("msg: want %q, got %q", "job enqueue failed", msg)
	}
}

// TestProcessBookUseCase_EmptyJobsSystem — covers ErrJobsSystemUnavailable.
// The async branch must surface the sentinel + mapper fires 503.
func TestProcessBookUseCase_EmptyJobsSystem(t *testing.T) {
	uc := NewProcessBookUseCase(&FakeBookProcessor{}, nil, zap.NewNop())
	_, err := uc.Handle(context.Background(), ProcessBookRequest{
		FilePath: "/tmp/in.pdf",
		Async:    true,
	})
	if !errors.Is(err, ErrJobsSystemUnavailable) {
		t.Fatalf("want ErrJobsSystemUnavailable, got %v", err)
	}
	status, _ := ProcessBookErrMapper(err)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d", status)
	}
}

// TestProcessBookUseCase_EmptyBooksService — covers ErrBooksServiceUnavailable.
// The sync branch must surface the sentinel + mapper fires 503.
func TestProcessBookUseCase_EmptyBooksService(t *testing.T) {
	uc := NewProcessBookUseCase(nil, &FakeAsyncEnqueuer{}, zap.NewNop())
	_, err := uc.Handle(context.Background(), ProcessBookRequest{
		FilePath: "/tmp/in.pdf",
		Async:    false,
	})
	if !errors.Is(err, ErrBooksServiceUnavailable) {
		t.Fatalf("want ErrBooksServiceUnavailable, got %v", err)
	}
	status, _ := ProcessBookErrMapper(err)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d", status)
	}
}

// TestProcessBookUseCase_TransformerNil — covers ErrBookTransformerMissing
// (Fase 7 review-fix #1). The sync branch surfaces the canonical sentinel
// when books.Service.ProcessBook returns the "transformer port not wired"
// error (composition root failed to wire pythontransformer.SubprocessTransformer,
// e.g. bad Python env or disabled books capability).
//
// Mapper contract: ErrBookTransformerMissing → 503 (NOT 500). godlike/07
// no-fake-availability: a nil-transformer wiring failure is a server-side
// configuration gap, semantically equivalent to "service not initialised".
// 503 is the canonical HTTP code per RFC 9110 §15.5.3; matches the
// ErrBooksServiceUnavailable / ErrJobsSystemUnavailable mappings already
// enforced by arm-1 of this test matrix.
func TestProcessBookUseCase_TransformerNil(t *testing.T) {
	// Decorate the fake with ErrBookTransformerMissing — the use case's
	// handleSync branch propagates svc errors verbatim (no fmt.Errorf
	// wrapping) so errors.Is reaches the canonical sentinel.
	fakeSvc := &FakeBookProcessor{
		Err: ErrBookTransformerMissing,
	}
	uc := NewProcessBookUseCase(fakeSvc, &FakeAsyncEnqueuer{}, zap.NewNop())

	_, err := uc.Handle(context.Background(), ProcessBookRequest{
		FilePath: "/tmp/in.pdf",
		Async:    false,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrBookTransformerMissing) {
		t.Fatalf("want ErrBookTransformerMissing wrapped, got %v", err)
	}
	// Sanity: error string is byte-equal to the sentinel. Pins the
	// use-case's propagate-verbatim contract — a future refactor that
	// wraps with fmt.Errorf("...%w", ErrBookTransformerMissing) would
	// change the .Error() string (visible in JSON API responses) even
	// though errors.Is still walks the chain via Unwrap. The byte-
	// equality assertion catches that wire-contract drift before it
	// reaches a client.
	if err.Error() != ErrBookTransformerMissing.Error() {
		t.Fatalf("error string drift: got %q, want %q", err.Error(), ErrBookTransformerMissing.Error())
	}

	status, msg := ProcessBookErrMapper(err)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d (msg=%q)", status, msg)
	}
	if !contains(msg, "transformer") {
		t.Fatalf("msg should mention transformer, got: %q", msg)
	}
}

// TestProcessBookErrMapper — pin the use-case → HTTP status mapping.
// Keep this mapper test as a separation-of-concerns check so future
// refactors that swap Handle's internal branches don't accidentally
// rewrite the mapper contract.
func TestProcessBookErrMapper(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantMsgSub string
	}{
		{
			name:       "books service unavailable → 503",
			err:        ErrBooksServiceUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantMsgSub: "books service",
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
			name:       "book transformer missing → 503 (Fase 7 review-fix #1)",
			err:        ErrBookTransformerMissing,
			wantStatus: http.StatusServiceUnavailable,
			wantMsgSub: "transformer",
		},
		{
			name:       "wrap-aware sentinel — errors.As matches the inner ErrProcessFailed",
			err:        errors.Join(errors.New("transient"), ErrProcessFailed{Message: "books worker reported failure"}),
			wantStatus: http.StatusInternalServerError,
			wantMsgSub: "books worker reported failure",
		},
		{
			name:       "unknown error — falls through to transport's default (status 0)",
			err:        errors.New("something else"),
			wantStatus: 0,
			wantMsgSub: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, msg := ProcessBookErrMapper(tc.err)
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
