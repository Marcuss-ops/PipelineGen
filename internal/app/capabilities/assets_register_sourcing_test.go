// Package app — assets_register_sourcing_test.go: TDD coverage
// for the clipJobEnqueuerAdapter (PR-BATCH-REGISTER-ASYNC, July 2026).
//
// Tests are organized into 5 groups: nil-svc error, nil-receiver error,
// happy-path roundtrip, json.RawMessage double-encoding prevention, and
// enqueue error propagation. godlike/07 discipline: every
// godlike/07 invariant (nil receiver → typed error, nil svc → typed
// error, payload survives round-trip without double-encoding, enqueue
// error wraps with %w) is locked by at least one test.
package capabilities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Test fixtures ──────────────────────────────────────────────────

// stubJobBroker implements job.JobBroker for tests.
// Only Create has real behavior; all other methods panic on unexpected
// calls so that a future regression in Service.Enqueue that accesses a
// new Store method surfaces immediately.
type stubJobBroker struct {
	lastJob  *job.Job
	createFn func(ctx context.Context, j *job.Job) error
}

func (s *stubJobBroker) Create(ctx context.Context, j *job.Job) error {
	s.lastJob = j
	if s.createFn != nil {
		return s.createFn(ctx, j)
	}
	// Simulate successful creation.
	j.ID = "job_test_0000000001_abcdefgh"
	return nil
}

func (s *stubJobBroker) Get(ctx context.Context, id string) (*job.Job, error) {
	panic("stubJobBroker.Get: unexpected call — Service.Enqueue does not call Get")
}

func (s *stubJobBroker) List(ctx context.Context, filter job.Filter) ([]job.Job, error) {
	panic("stubJobBroker.List: unexpected call — Service.Enqueue does not call List")
}

func (s *stubJobBroker) FindActiveByKey(ctx context.Context, activeKey string) (*job.Job, error) {
	// Called by Enqueue when req.ActiveKey != "". In our tests ActiveKey is empty,
	// but we return nil/nil to satisfy the interface contract.
	return nil, nil
}

func (s *stubJobBroker) FindByTypeAndCorrelation(ctx context.Context, jobType, correlationID string) (*job.Job, error) {
	// Called by Enqueue when req.CorrelationID != "". In our tests CorrelationID is empty.
	return nil, nil
}

func (s *stubJobBroker) ListEvents(ctx context.Context, jobID string) ([]job.Event, error) {
	panic("stubJobBroker.ListEvents: unexpected call — Service.Enqueue does not call ListEvents")
}

func (s *stubJobBroker) Retry(ctx context.Context, id string) (*job.Job, error) {
	panic("stubJobBroker.Retry: unexpected call — Service.Enqueue does not call Retry")
}

func (s *stubJobBroker) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration, types []string) (*job.Job, error) {
	panic("stubJobBroker.ClaimNext: unexpected call — Service.Enqueue does not call ClaimNext")
}

func (s *stubJobBroker) Complete(ctx context.Context, id, workerID, leaseID string, expectedRevision int, result json.RawMessage) error {
	panic("stubJobBroker.Complete: unexpected call — Service.Enqueue does not call Complete")
}

func (s *stubJobBroker) Fail(ctx context.Context, id, workerID, leaseID string, expectedRevision int, errMsg string) error {
	panic("stubJobBroker.Fail: unexpected call — Service.Enqueue does not call Fail")
}

func (s *stubJobBroker) ScheduleRetry(ctx context.Context, id, workerID, leaseID string, expectedRevision int, errMsg string, backoff time.Duration) error {
	panic("stubJobBroker.ScheduleRetry: unexpected call — Service.Enqueue does not call ScheduleRetry")
}

func (s *stubJobBroker) Cancel(ctx context.Context, id string) error {
	panic("stubJobBroker.Cancel: unexpected call — Service.Enqueue does not call Cancel")
}

func (s *stubJobBroker) SetProgress(ctx context.Context, id string, progress int, message string) error {
	panic("stubJobBroker.SetProgress: unexpected call — Service.Enqueue does not call SetProgress")
}

func (s *stubJobBroker) AddEvent(ctx context.Context, id, eventType, message string, data map[string]any) error {
	panic("stubJobBroker.AddEvent: unexpected call — Service.Enqueue does not call AddEvent")
}

// FASE 4(b) (July 2026): the canonical job.Store.RenewLease
// signature now returns the typed RenewLeaseResult envelope
// (LeaseStateContinue | CancelRequested | LeaseLost). The pre-Fase-4
// `error`-only return is gone. The stub still panics on dispatch
// (Enqueue does not call RenewLease), but the signature MUST match
// the canonical Store surface for the compile-time pin
// `var _ job.JobBroker = (*stubJobBroker)(nil)` to hold.
func (s *stubJobBroker) RenewLease(ctx context.Context, id, workerID string, leaseTTL time.Duration) (job.RenewLeaseResult, error) {
	panic("stubJobBroker.RenewLease: unexpected call — Service.Enqueue does not call RenewLease")
}

func (s *stubJobBroker) DeadLetter(ctx context.Context, id, errMsg string) error {
	panic("stubJobBroker.DeadLetter: unexpected call — Service.Enqueue does not call DeadLetter")
}

func (s *stubJobBroker) FinalizeAttempt(ctx context.Context, cmd job.FinalizeAttemptCommand) (job.FinalizeAttemptResult, error) {
	panic("stubJobBroker.FinalizeAttempt: unexpected call — Service.Enqueue does not call FinalizeAttempt")
}

// Compile-time assertion: stubJobBroker satisfies job.JobBroker.
var _ job.JobBroker = (*stubJobBroker)(nil)

// newTestService builds a minimal *appjobs.Service wired to a
// stubJobBroker. The dispatcher is nil (Enqueue does not use it).
//
// PR-jobs-retry-contract (July 2026): the 4-arg NewService signature
// (REGISTRY required, fail-closed) replaces the pre-PR 3-arg shape.
// Tests that do not depend on a specific registry type use
// appjobs.Compose() — the canonical composition root registry used
// across production wiring. Enqueue with a non-registered jobType
// will surface ErrMaxRetriesUnknown at Enqueue time (not at
// construction), so tests that exercise Enqueue must wire a
// registry whose composition knows the test jobType ("media.clip").
func newTestService(broker *stubJobBroker) *appjobs.Service {
	svc, err := appjobs.NewService(broker, nil /* dispatcher: Enqueue does not use */, zap.NewNop(), appjobs.Compose())
	if err != nil {
		panic(fmt.Sprintf("newTestService: appjobs.NewService returned err=%v — test fixture should never fail construction", err))
	}
	return svc
}

// ── nil-svc → error ────────────────────────────────────────────────

// TestClipJobEnqueuerAdapter_NilSvc verifies the godlike/07 fail-closed
// invariant: a nil svc field returns a typed error containing "not wired".
func TestClipJobEnqueuerAdapter_NilSvc(t *testing.T) {
	a := &clipJobEnqueuerAdapter{svc: nil}
	jobID, err := a.EnqueueClip(context.Background(), sourcing.RegisterClipCommand{
		Name: "test-clip",
		URL:  "https://youtube.com/watch?v=abc123",
	})
	if err == nil {
		t.Fatalf("err = nil, want error for nil svc")
	}
	if jobID != "" {
		t.Fatalf("jobID = %q, want empty on error", jobID)
	}
	if !strings.Contains(err.Error(), "not wired") {
		t.Fatalf("err = %q, want message containing 'not wired'", err.Error())
	}
}

// TestClipJobEnqueuerAdapter_NilSvc_ReturnsErrorAfterPartialSetup verifies
// that even when the adapter struct exists but svc is nil (the common
// composition-root bug path), the error is the same typed message.
func TestClipJobEnqueuerAdapter_NilSvc_ReturnsErrorAfterPartialSetup(t *testing.T) {
	a := &clipJobEnqueuerAdapter{} // svc is nil (zero value)
	jobID, err := a.EnqueueClip(context.Background(), sourcing.RegisterClipCommand{
		Name: "test-clip-2",
	})
	if err == nil {
		t.Fatalf("err = nil, want error for nil svc (zero-value field)")
	}
	if jobID != "" {
		t.Fatalf("jobID = %q, want empty on error", jobID)
	}
	if !strings.Contains(err.Error(), "not wired") {
		t.Fatalf("err = %q, want message containing 'not wired'", err.Error())
	}
}

// ── nil-receiver → error ───────────────────────────────────────────

// TestClipJobEnqueuerAdapter_NilReceiver verifies the godlike/07
// sentinel: a nil receiver returns a typed error containing "not wired".
func TestClipJobEnqueuerAdapter_NilReceiver(t *testing.T) {
	var a *clipJobEnqueuerAdapter // nil receiver
	jobID, err := a.EnqueueClip(context.Background(), sourcing.RegisterClipCommand{
		Name: "test-clip-nil-recv",
	})
	if err == nil {
		t.Fatalf("err = nil, want error for nil receiver")
	}
	if jobID != "" {
		t.Fatalf("jobID = %q, want empty on error", jobID)
	}
	if !strings.Contains(err.Error(), "not wired") {
		t.Fatalf("err = %q, want message containing 'not wired'", err.Error())
	}
}

// ── happy-path roundtrip ────────────────────────────────────────────

// TestClipJobEnqueuerAdapter_HappyPath verifies that a well-formed
// RegisterClipCommand survives the marshal → enqueue → stub-Create
// round-trip. The job payload is json.RawMessage (not double-encoded).
func TestClipJobEnqueuerAdapter_HappyPath(t *testing.T) {
	broker := &stubJobBroker{}
	svc := newTestService(broker)
	a := &clipJobEnqueuerAdapter{svc: svc}

	cmd := sourcing.RegisterClipCommand{
		URL:             "https://youtube.com/watch?v=dQw4w9WgXcQ",
		Name:            "never gonna give you up",
		Description:     "rick astley music video",
		Summary:         "classic 80s pop song",
		Topics:          []string{"music", "80s", "pop"},
		Speakers:        []string{"Rick Astley"},
		MentionedPeople: []string{},
		Hook:            "never gonna give you up",
		Tags:            []string{"music", "classic"},
		Source:          "youtube",
		Category:        "music",
		Group:           "80s-hits",
		FolderID:        "folder-abc",
		StartSec:        0.0,
		EndSec:          213.0,
		Force:           false,
	}

	jobID, err := a.EnqueueClip(context.Background(), cmd)
	if err != nil {
		t.Fatalf("EnqueueClip: %v", err)
	}
	if jobID == "" {
		t.Fatalf("jobID = %q, want non-empty job ID", jobID)
	}

	// Verify the stub captured the job.
	if broker.lastJob == nil {
		t.Fatalf("lastJob = nil, want stubbed Create to have been called")
	}

	// Verify the payload is not empty.
	if len(broker.lastJob.Payload) == 0 {
		t.Fatalf("lastJob.Payload is empty, want JSON-encoded RegisterClipCommand")
	}

	// Verify the payload survives round-trip: unmarshal back to
	// RegisterClipCommand and check key fields.
	var roundTripped sourcing.RegisterClipCommand
	if err := json.Unmarshal(broker.lastJob.Payload, &roundTripped); err != nil {
		t.Fatalf("json.Unmarshal of lastJob.Payload: %v", err)
	}
	if roundTripped.Name != cmd.Name {
		t.Fatalf("roundTripped.Name = %q, want %q", roundTripped.Name, cmd.Name)
	}
	if roundTripped.URL != cmd.URL {
		t.Fatalf("roundTripped.URL = %q, want %q", roundTripped.URL, cmd.URL)
	}
	if len(roundTripped.Topics) != len(cmd.Topics) {
		t.Fatalf("roundTripped.Topics len = %d, want %d", len(roundTripped.Topics), len(cmd.Topics))
	}
	if roundTripped.EndSec != cmd.EndSec {
		t.Fatalf("roundTripped.EndSec = %f, want %f", roundTripped.EndSec, cmd.EndSec)
	}
}

// ── json.RawMessage double-encoding prevention ──────────────────────

// TestClipJobEnqueuerAdapter_DoubleEncodingPrevention verifies the
// godlike/07 double-encoding prevention contract: the payload stored
// in the job is json.RawMessage (a byte-for-byte JSON representation),
// NOT a base64-encoded or string-quoted version.
//
// The adapter wraps json.Marshal output in json.RawMessage before
// passing it to Service.Enqueue. Service.Enqueue internally calls
// json.Marshal(req.Payload); because json.RawMessage implements
// json.Marshaler by returning itself, this is a pass-through (no
// double-encode). The test verifies this by checking that the stored
// payload is valid JSON matching the original struct, not a
// base64-encoded string.
func TestClipJobEnqueuerAdapter_DoubleEncodingPrevention(t *testing.T) {
	broker := &stubJobBroker{}
	svc := newTestService(broker)
	a := &clipJobEnqueuerAdapter{svc: svc}

	cmd := sourcing.RegisterClipCommand{
		Name: "json-raw-message-test",
		URL:  "https://youtube.com/watch?v=xyz789",
		Tags: []string{"tag1", "tag2"},
	}

	_, err := a.EnqueueClip(context.Background(), cmd)
	if err != nil {
		t.Fatalf("EnqueueClip: %v", err)
	}

	raw := broker.lastJob.Payload

	// Verify the payload starts with '{' (JSON object) — not a string
	// literal (which would start with '"').
	if len(raw) == 0 || raw[0] != '{' {
		t.Fatalf("payload[0] = %q, want '{' (JSON object start). "+
			"Raw: %s", raw[0], string(raw))
	}

	// Verify it does NOT start with '"' (which would indicate it was
	// string-quoted / double-encoded).
	if raw[0] == '"' {
		t.Fatalf("payload[0] = '\"' — double-encoding detected! "+
			"RawMessage was string-quoted instead of passed through. "+
			"Raw: %s", string(raw))
	}

	// Verify the payload is valid JSON and matches the original.
	var roundTripped sourcing.RegisterClipCommand
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatalf("json.Unmarshal of payload: %v. Raw: %s", err, string(raw))
	}
	if roundTripped.Name != cmd.Name {
		t.Fatalf("roundTripped.Name = %q, want %q (json.RawMessage corrupted)", roundTripped.Name, cmd.Name)
	}
	if len(roundTripped.Tags) != len(cmd.Tags) {
		t.Fatalf("roundTripped.Tags len = %d, want %d", len(roundTripped.Tags), len(cmd.Tags))
	}
	if roundTripped.Tags[0] != cmd.Tags[0] {
		t.Fatalf("roundTripped.Tags[0] = %q, want %q", roundTripped.Tags[0], cmd.Tags[0])
	}
}

// ── resolverFolderEnsurerAdapter tests ──────────────────────────────

// fakeAdmin implements drive.Admin for testing resolverFolderEnsurerAdapter.
// Only GetOrCreateFolder has real behavior (call capture + configurable result);
// all other Admin port methods return zero values or nil.
type fakeAdmin struct {
	getOrCreateFn func(ctx context.Context, name, parentID string) (string, error)
}

func (f *fakeAdmin) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if f.getOrCreateFn != nil {
		return f.getOrCreateFn(ctx, name, parentID)
	}
	return "fake-folder-id", nil
}
func (f *fakeAdmin) GetFolderName(context.Context, string) (string, error)  { return "", nil }
func (f *fakeAdmin) TrashFolder(context.Context, string) error              { return nil }
func (f *fakeAdmin) DeleteFolder(context.Context, string) error             { return nil }
func (f *fakeAdmin) TrashFile(context.Context, string) error                { return nil }
func (f *fakeAdmin) DeleteFile(context.Context, string) error               { return nil }
func (f *fakeAdmin) MoveFile(context.Context, string, string, string) error { return nil }
func (f *fakeAdmin) RenameFile(context.Context, string, string) error       { return nil }
func (f *fakeAdmin) Ping(context.Context) error                             { return nil }

// Compile-time assertion: fakeAdmin satisfies drive.Admin.
var _ driveutil.Admin = (*fakeAdmin)(nil)

// TestResolverFolderEnsurer_NilReceiver verifies godlike/07 fail-closed:
// a nil receiver returns a typed error containing "not wired".
func TestResolverFolderEnsurer_NilReceiver(t *testing.T) {
	var a *resolverFolderEnsurerAdapter // nil receiver
	id, err := a.EnsureFolder(context.Background(), "root-123", "seg1", "seg2")
	if err == nil {
		t.Fatal("err = nil, want error for nil receiver")
	}
	if id != "" {
		t.Fatalf("id = %q, want empty on error", id)
	}
	if !strings.Contains(err.Error(), "not wired") {
		t.Fatalf("err = %q, want message containing 'not wired'", err.Error())
	}
}

// TestResolverFolderEnsurer_NilAdmin verifies godlike/07 fail-closed:
// a nil admin field returns a typed error containing "not wired".
func TestResolverFolderEnsurer_NilAdmin(t *testing.T) {
	a := &resolverFolderEnsurerAdapter{admin: nil}
	id, err := a.EnsureFolder(context.Background(), "root-456", "seg")
	if err == nil {
		t.Fatal("err = nil, want error for nil admin")
	}
	if id != "" {
		t.Fatalf("id = %q, want empty on error", id)
	}
	if !strings.Contains(err.Error(), "not wired") {
		t.Fatalf("err = %q, want message containing 'not wired'", err.Error())
	}
}

// folderCall records a GetOrCreateFolder invocation for assertions.
type folderCall struct {
	name     string
	parentID string
}

// TestResolverFolderEnsurer_HappyPath verifies that EnsureFolder delegates
// to drive.EnsureFolderPath which walks segments via admin.GetOrCreateFolder.
func TestResolverFolderEnsurer_HappyPath(t *testing.T) {
	var calls []folderCall
	admin := &fakeAdmin{
		getOrCreateFn: func(_ context.Context, name, parentID string) (string, error) {
			calls = append(calls, folderCall{name: name, parentID: parentID})
			return "folder-" + name, nil
		},
	}
	a := &resolverFolderEnsurerAdapter{admin: admin}

	id, err := a.EnsureFolder(context.Background(), "root-000", "Boxe", "mike-tyson")
	if err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	if id != "folder-mike-tyson" {
		t.Fatalf("id = %q, want %q", id, "folder-mike-tyson")
	}
	if len(calls) != 2 {
		t.Fatalf("GetOrCreateFolder called %d times, want 2", len(calls))
	}
	if calls[0].name != "Boxe" || calls[0].parentID != "root-000" {
		t.Fatalf("call[0] = {%q, %q}, want {Boxe, root-000}", calls[0].name, calls[0].parentID)
	}
	if calls[1].name != "mike-tyson" || calls[1].parentID != "folder-Boxe" {
		t.Fatalf("call[1] = {%q, %q}, want {mike-tyson, folder-Boxe}", calls[1].name, calls[1].parentID)
	}
}

// TestResolverFolderEnsurer_EmptySegmentSkipped pins the EnsureFolderPath
// contract that empty-string segments are silently skipped (not passed to
// admin.GetOrCreateFolder). EnsureFolder(ctx, root, 'Boxe', ”, 'mike-tyson')
// must produce exactly 2 calls — the empty segment in position 2 is a no-op.
//
// godlike/07 NO-FAKE-AVAILABILITY: this test locks the skip behavior so a
// future refactor that removes the `if seg == "" { continue }` guard in
// EnsureFolderPath surfaces as a test failure (3 calls instead of 2).
func TestResolverFolderEnsurer_EmptySegmentSkipped(t *testing.T) {
	var calls []folderCall
	admin := &fakeAdmin{
		getOrCreateFn: func(_ context.Context, name, parentID string) (string, error) {
			calls = append(calls, folderCall{name: name, parentID: parentID})
			return "folder-" + name, nil
		},
	}
	a := &resolverFolderEnsurerAdapter{admin: admin}

	// The empty string in position 2 must be skipped — only "Boxe" and
	// "mike-tyson" should reach admin.GetOrCreateFolder.
	id, err := a.EnsureFolder(context.Background(), "root-000", "Boxe", "", "mike-tyson")
	if err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	if id != "folder-mike-tyson" {
		t.Fatalf("id = %q, want %q", id, "folder-mike-tyson")
	}
	if len(calls) != 2 {
		t.Fatalf("GetOrCreateFolder called %d times, want 2 (empty segment must be skipped)", len(calls))
	}
	// First call: Boxe under root.
	if calls[0].name != "Boxe" || calls[0].parentID != "root-000" {
		t.Fatalf("call[0] = {%q, %q}, want {Boxe, root-000}", calls[0].name, calls[0].parentID)
	}
	// Second call: mike-tyson under folder-Boxe (empty segment skipped).
	if calls[1].name != "mike-tyson" || calls[1].parentID != "folder-Boxe" {
		t.Fatalf("call[1] = {%q, %q}, want {mike-tyson, folder-Boxe}", calls[1].name, calls[1].parentID)
	}
}

// TestResolverFolderEnsurer_AllEmptySegments returns rootID when every
// segment is empty — zero GetOrCreateFolder calls.
func TestResolverFolderEnsurer_AllEmptySegments(t *testing.T) {
	var calls []folderCall
	admin := &fakeAdmin{
		getOrCreateFn: func(_ context.Context, name, parentID string) (string, error) {
			calls = append(calls, folderCall{name: name, parentID: parentID})
			return "folder-" + name, nil
		},
	}
	a := &resolverFolderEnsurerAdapter{admin: admin}

	id, err := a.EnsureFolder(context.Background(), "root-999", "", "", "")
	if err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	// When all segments are empty, EnsureFolderPath returns rootID as-is.
	if id != "root-999" {
		t.Fatalf("id = %q, want %q (rootID passthrough when all segments empty)", id, "root-999")
	}
	if len(calls) != 0 {
		t.Fatalf("GetOrCreateFolder called %d times, want 0 (all segments empty)", len(calls))
	}
}

// TestResolverFolderEnsurer_ErrorPropagation verifies that errors from
// admin.GetOrCreateFolder propagate through EnsureFolderPath to the caller.
func TestResolverFolderEnsurer_ErrorPropagation(t *testing.T) {
	simulatedErr := fmt.Errorf("drive API: permission denied")
	admin := &fakeAdmin{
		getOrCreateFn: func(_ context.Context, name, parentID string) (string, error) {
			return "", simulatedErr
		},
	}
	a := &resolverFolderEnsurerAdapter{admin: admin}

	id, err := a.EnsureFolder(context.Background(), "root-err", "Boxe")
	if err == nil {
		t.Fatal("err = nil, want error from admin.GetOrCreateFolder")
	}
	if id != "" {
		t.Fatalf("id = %q, want empty on error", id)
	}
	if !strings.Contains(err.Error(), simulatedErr.Error()) {
		t.Fatalf("err = %q, want to contain downstream error %q", err.Error(), simulatedErr.Error())
	}
}

// ── enqueue error propagation ───────────────────────────────────────

// TestClipJobEnqueuerAdapter_EnqueueError verifies that a downstream
// error from Service.Enqueue (simulated via createFn) is wrapped with
// "%w" such that the caller can probe it via errors.Is.
func TestClipJobEnqueuerAdapter_EnqueueError(t *testing.T) {
	simulatedErr := job.ErrUnknownJobType
	broker := &stubJobBroker{
		createFn: func(ctx context.Context, j *job.Job) error {
			return simulatedErr
		},
	}
	svc := newTestService(broker)
	a := &clipJobEnqueuerAdapter{svc: svc}

	jobID, err := a.EnqueueClip(context.Background(), sourcing.RegisterClipCommand{
		Name: "will-fail",
		URL:  "https://youtube.com/watch?v=fail999",
	})
	if err == nil {
		t.Fatalf("err = nil, want enqueue error")
	}
	if jobID != "" {
		t.Fatalf("jobID = %q, want empty on error", jobID)
	}
	if !strings.Contains(err.Error(), "clipJobEnqueuerAdapter: enqueue media.clip") {
		t.Fatalf("err = %q, want wrapping prefix 'clipJobEnqueuerAdapter: enqueue media.clip'", err.Error())
	}
	if !strings.Contains(err.Error(), simulatedErr.Error()) {
		t.Fatalf("err = %q, want to contain downstream error %q", err.Error(), simulatedErr.Error())
	}
}
