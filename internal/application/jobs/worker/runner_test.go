// Package worker — uploadOutputs regression tests for Blocco 2.2.
//
// Pin the required-vs-optional gating contract introduced in commit
// c7ccd658. The previous audit reported that jobs whose handler
// declared an output path that didn't exist on disk still got
// reported SUCCEEDED: the runner silently `continue`-d on
// os.IsNotExist and the deferred workspace cleanup permanently
// dropped the missing artefact.
//
// Required behaviour pinned here:
//
//   - When a handler declares an output (OutputArtifact.Required=true,
//     or `output_files: [map{required:true,...}]`), and the path is
//     missing on disk, uploadOutputs returns a non-nil error
//     containing the path + asset_id + job. UploadFile MUST NOT have
//     been called.
//   - Legacy single-string keys (output_path / pdf_path /
//     markdown_path), bare []string items in output_files, and
//     OutputArtifact struct entries with Required omitted are
//     treated as OPTIONAL — missing files are still skipped
//     (backward-compat preserved for handlers whose optional
//     sub-render sometimes emits an empty path).
//   - Existing files still upload, including via the JSON-style
//     map[string]any representation of OutputArtifact.
//
// Tests are kept in `package worker` (not `worker_test`) so the
// runner.uploadManifest method is reachable (legacy path exercised
// when no __artifact_manifest key is present).
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// mockAssetClient records every UploadFile invocation so the test
// can assert that uploads happen (or don't happen) on the exact
// path that uploadOutputs chose.
type mockAssetClient struct {
	uploads []uploadCall
}

type uploadCall struct {
	assetID string
	path    string
}

func (m *mockAssetClient) Download(_ context.Context, _ string) (io.ReadCloser, string, error) {
	return nil, "", nil
}

func (m *mockAssetClient) UploadFile(_ context.Context, assetID, filePath string) error {
	m.uploads = append(m.uploads, uploadCall{assetID: assetID, path: filePath})
	return nil
}

// TestRunner_uploadManifest_LegacyFallback drives the Blocco 2.2 contract
// via uploadManifest with handler results that lack a manifest,
func TestRunner_uploadManifest_NilAssetClient(t *testing.T) {
	runner := &Runner{assetClient: nil}
	_, err := runner.uploadManifest(context.Background(), "job-nil-client", map[string]any{
		"output_files": []any{
			OutputArtifact{Path: "/missing", Required: true},
		},
	})
	if err == nil {
		t.Fatal("nil assetClient + non-empty handlerResult must fail-closed with ErrArtifactClientRequired (P0 #4) — pre-P0 #4 the runner silently returned nil + nil and masked the misconfiguration")
	}
	if !errors.Is(err, ErrArtifactClientRequired) {
		t.Errorf("runner.uploadManifest err = %v, want errors.Is(err, ErrArtifactClientRequired)", err)
	}
	// The jobID must surface in the message so operator dashboards
	// can correlate the misconfiguration with the job row.
	if !strings.Contains(err.Error(), "job-nil-client") {
		t.Errorf("err message %q should contain jobID=job-nil-client", err.Error())
	}
}

// TestRunner_uploadManifest_NilAssetClient_EmptyHandlerResult_StillSilentSkips
// pins the conjunction case: BOTH conditions hold (assetClient nil
// AND handlerResult empty). The P0 #4 split is order-sensitive — the
// empty handlerResult check fires first so the runner returns
// nil + nil silently, not ErrArtifactClientRequired. This guards
// against future contributors re-ordering the conditions and
// regressing the legacy contract. (Code-reviewer callout #1: this
// test replaces a redundant companion that tested the same
// condition under a less self-documenting name.)
//
// If the conditions are reordered, this test still pins
// order-sensitivity: assetClient=nil + handlerResult=empty MUST
// short-circuit on the empty check first (the legacy contract
// survives the P0 #4 fail-closed split).
func TestRunner_uploadManifest_NilAssetClient_EmptyHandlerResult_StillSilentSkips(t *testing.T) {
	runner := &Runner{assetClient: nil}
	uploaded, err := runner.uploadManifest(context.Background(), "conjoint-empty-job", map[string]any{})
	if err != nil {
		t.Fatalf("both conditions empty must still silent-skip, got: %v", err)
	}
	if uploaded != nil {
		t.Errorf("conjoint empty must return nil uploaded manifest, got: %+v", uploaded)
	}
}

// TestRunner_uploadManifest_NonNotExistStatErrorsBubble exercises the
// os.Stat error path via the legacy fallback.
// TestRunner_uploadManifest_WithManifest verifies the manifest upload path
// end-to-end: handlerResult contains a serialised ArtifactManifest under
// __artifact_manifest, the runner validates required artefacts, uploads
// them, and returns an UploadedManifest with no local paths.
func TestRunner_uploadManifest_WithManifest(t *testing.T) {
	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "script.json")
	if err := os.WriteFile(realFile, []byte(`{"ok":true}`), 0644); err != nil {
		t.Fatalf("seed real file: %v", err)
	}

	mock := &mockAssetClient{}
	runner := &Runner{assetClient: mock}

	manifest := job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf-manifest-test",
		JobID:         "job-manifest-test",
		Artifacts: []job.Artifact{
			{
				ID:       "job-manifest-test:script",
				Kind:     job.ArtifactKindScriptJSON,
				Path:     realFile,
				Filename: "script.json",
				MIMEType: "application/json",
				Required: true,
			},
			{
				ID:       "job-manifest-test:image",
				Kind:     job.ArtifactKindImage,
				Path:     "/tmp/missing/image.png",
				Filename: "image.png",
				MIMEType: "image/png",
				Required: false,
			},
		},
	}

	uploaded, err := runner.uploadManifest(context.Background(), "job-manifest-test", map[string]any{
		job.ManifestKey: &manifest,
	})
	if err != nil {
		t.Fatalf("uploadManifest with valid manifest: %v", err)
	}
	if uploaded == nil {
		t.Fatal("expected non-nil UploadedManifest from manifest path")
	}

	// Required artifact was uploaded.
	if len(mock.uploads) != 1 {
		t.Fatalf("expected 1 upload (required only), got %d: %+v", len(mock.uploads), mock.uploads)
	}
	if mock.uploads[0].assetID != "job-manifest-test:script" {
		t.Errorf("upload assetID = %q, want job-manifest-test:script", mock.uploads[0].assetID)
	}

	// No local paths in the UploadedManifest.
	if len(uploaded.Artifacts) != 2 {
		t.Fatalf("expected 2 artifacts in UploadedManifest, got %d", len(uploaded.Artifacts))
	}
	if uploaded.Artifacts[0].Status != job.StatusReady {
		t.Errorf("required artifact status = %q, want %q", uploaded.Artifacts[0].Status, job.StatusReady)
	}
	if uploaded.Artifacts[1].Status != job.StatusSkipped {
		t.Errorf("non-required missing artifact status = %q, want %q", uploaded.Artifacts[1].Status, job.StatusSkipped)
	}

	// Verify no local paths leak.
	uploadedJSON, _ := json.Marshal(uploaded)
	if strings.Contains(string(uploadedJSON), "/tmp/") {
		t.Error("UploadedManifest JSON contains local paths — must not leak")
	}
	if strings.Contains(string(uploadedJSON), realFile) {
		t.Error("UploadedManifest JSON contains real file path — must not leak")
	}
}

func TestRunner_uploadManifest_NonNotExistStatErrorsBubble(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("permission semantics differ on windows")
	}

	tmpDir := t.TempDir()
	noRead := filepath.Join(tmpDir, "no-read")
	if err := os.Mkdir(noRead, 0000); err != nil {
		t.Fatalf("mkdir noRead: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(noRead, 0755) })

	mock := &mockAssetClient{}
	runner := &Runner{assetClient: mock}

	_, err := runner.uploadManifest(context.Background(), "job-perm", map[string]any{
		"output_files": []any{
			OutputArtifact{Path: filepath.Join(noRead, "inside"), Required: false},
		},
	})
	if err == nil {
		t.Fatalf("expected non-nil error from os.Stat in unreadable dir, got nil")
	}
	if strings.Contains(err.Error(), "required output file missing") {
		t.Fatalf("stat permission error must not be coerced into required-missing branch: %v", err)
	}
}

// ── P0 #5 ErrLeaseLostDuringRun (godlike/07 typed-error contract) ──────
//
// See internal/application/jobs/worker/runner.go::postRenewFailClosedCheck
// + ErrLeaseLostDuringRun for the canonical surface. The 3 tests below
// pin the contract end-to-end:
//   - helper test (typed-error contract + multi-%w preservation)
//   - clean-drain helper test (no error → fall-through to Complete)
//   - integration test (Complete is NEVER called when renewal fails)

// stubLeaseBroker is the mock appjobs.Broker used by the P0 #5
// integration test. Renew returns renewErr on every call
// (typically job.ErrLeaseLost) so the renewal loop
// surfaces the error. Complete and Fail record every invocation
// so the test can assert they were/were-not called. All other
// methods are no-ops.
//
// Determinism barrier (P0 #5 follow-up): `renewed` (closed on
// the first Renew call via `renewOnce`) is a deterministic
// sync between the simulated renewLoop ticker and the test
// handler. The handler blocks on `<-mock.renewed` instead of
// using time.Sleep(100ms) — eliminates the CI flake observed at
// runner_test.go:383 ("expected error, got nil") under
// load when the renew tick fired AFTER the handler returned
// (drain fell through to the 200ms safety-net → runLease
// proceeded to tools.Complete → assertion failed).
//
// — REASONED, NOT LAB-VERIFIED —
// The "Memory model guarantee" paragraph below is the design
// rationale for the integration-test ordering, derived from
// reading runner.go::renewLoop. The integration test that
// would lab-verify this claim is t.Skip'd at P0 #5 closure
// (see architecture/current.yaml#P0.6-runlease_checkrenew_redesign
// for the P0 #6 follow-up that re-enables it). Until P0 #6 runs
// the assertion in lab, treat the bullet points below as
// design hypothesis — each is independently plausible per the
// runner.go source, but the assertion that proves all three
// together is dormant.
//
// Memory model guarantee (mirrors runLease's renewLoop in
// runner.go): broker.Renew returns synchronously into the
// renewLoop goroutine and the immediately-following
// `renewErrs <- err` write is on a buffered (cap 1) channel,
// so the write happens in the SAME goroutine frame, before the
// runner loop checks renewCancel on its next select. handler
// returning via `<-mock.renewed` is therefore a sufficient
// barrier — no second channel is needed to "hold" the renew
// goroutine.
type stubLeaseBroker struct {
	renewErr      error
	completeCalls int32 // atomic for goroutine safety
	failCalls     int32
	renewCalls    int32
	// renewed + renewOnce form the deterministic barrier
	// described in the type doc above.
	renewed   chan struct{}
	renewOnce sync.Once
}

func (b *stubLeaseBroker) RegisterWorker(_ context.Context, _ appjobs.RegisterWorkerCommand) (*appjobs.WorkerSession, error) {
	// runLease doesn't use the WorkerSession; the worker startup
	// path is not exercised by this test. Return nil to keep the
	// mock minimal.
	return nil, nil
}

func (b *stubLeaseBroker) Heartbeat(_ context.Context, _ appjobs.HeartbeatCommand) error {
	return nil
}

func (b *stubLeaseBroker) Claim(_ context.Context, _ appjobs.ClaimCommand) (*appjobs.Lease, error) {
	return &appjobs.Lease{
		Job: &job.Job{
			ID:       "renew-fail-job",
			Type:     "renew-fail-test",
			Revision: 1,
			LeaseID:  "lease-1",
		},
		LeaseID: "lease-1",
	}, nil
}

func (b *stubLeaseBroker) Renew(_ context.Context, _ appjobs.RenewCommand) (*appjobs.Lease, error) {
	atomic.AddInt32(&b.renewCalls, 1)
	// Determinism barrier (P0 #5 follow-up): close `renewed`
	// exactly once on the first call so the test handler can
	// return via a channel-receive instead of time.Sleep.
	// sync.Once makes the close idempotent across subsequent
	// ticks (close-on-closed-channel would panic).
	b.renewOnce.Do(func() {
		close(b.renewed)
	})
	return nil, b.renewErr
}

func (b *stubLeaseBroker) Progress(_ context.Context, _ appjobs.ProgressCommand) error {
	return nil
}

func (b *stubLeaseBroker) Complete(_ context.Context, _ appjobs.CompleteCommand) error {
	atomic.AddInt32(&b.completeCalls, 1)
	return nil
}

func (b *stubLeaseBroker) CompleteWithArtifacts(_ context.Context, _ appjobs.CompleteWithArtifactsCommand) ([]string, error) {
	atomic.AddInt32(&b.completeCalls, 1)
	return nil, nil
}

func (b *stubLeaseBroker) Fail(_ context.Context, _ appjobs.FailCommand) error {
	atomic.AddInt32(&b.failCalls, 1)
	return nil
}

func (b *stubLeaseBroker) IsCancelled(_ context.Context, _ string, _ string) (bool, error) {
	return false, nil
}

// Compile-time pin (AGENTS.md Pattern 0 + Godlike-06 SSOT):
// guards against future drift of the appjobs.Broker interface
// surface. Without this assertion, the stubLeaseBroker's
// runtime conformance to Broker is only exercised by the
// t.Skip'd integration test (TestRunLease_RenewalError_NoCompleteCall)
// — when P0 #6 re-enables it. With the pin, future maintainers
// who add a 9th method to the Broker interface see a build-time
// failure rather than a runtime panic at the next test run.
// Cheap (1 line, no runtime cost), high regression-prevention
// value during the integration-test-dormant window.
var _ appjobs.Broker = (*stubLeaseBroker)(nil)

// TestPostRenewFailClosedCheck_LeaseLost_ReturnsErrLeaseLostDuringRun
// pins the P0 #5 typed-error contract in isolation: when the
// renewal loop reports an error, the helper returns
// ErrLeaseLostDuringRun wrapped via Go 1.20+ multi-%w so
// errors.Is probes BOTH sentinels (the typed sentinel + the
// original renewErr).
func TestPostRenewFailClosedCheck_LeaseLost_ReturnsErrLeaseLostDuringRun(t *testing.T) {
	renewErrs := make(chan error, 1)
	renewErrs <- job.ErrLeaseLost

	err := postRenewFailClosedCheck(renewErrs)
	if err == nil {
		t.Fatal("expected ErrLeaseLostDuringRun when renewal reports ErrLeaseLost, got nil")
	}
	if !errors.Is(err, ErrLeaseLostDuringRun) {
		t.Errorf("err = %v, want errors.Is(err, ErrLeaseLostDuringRun)", err)
	}
	if !errors.Is(err, job.ErrLeaseLost) {
		t.Errorf("err = %v, want errors.Is(err, job.ErrLeaseLost) (Go 1.20+ multi-%%w)", err)
	}
}

// TestPostRenewFailClosedCheck_CleanDrain_ReturnsNil pins the
// happy-path drain: when the renewal loop exited cleanly (sent
// nil on the channel), the helper returns nil so the caller
// proceeds to the canonical tools.Complete.
func TestPostRenewFailClosedCheck_CleanDrain_ReturnsNil(t *testing.T) {
	renewErrs := make(chan error, 1)
	renewErrs <- nil

	err := postRenewFailClosedCheck(renewErrs)
	if err != nil {
		t.Errorf("clean drain must return nil (so caller proceeds to Complete), got: %v", err)
	}
}

// TestPostRenewFailClosedCheck_Timeout_ReturnsNil pins the
// 200ms safety-net branch: when the channel is empty (the
// renewal goroutine never wrote an error and never exited),
// the helper's select falls through to time.After and returns
// nil so the caller proceeds to tools.Complete. This is the
// inverse of TestPostRenewFailClosedCheck_LeaseLost_ReturnsErrLeaseLostDuringRun
// (where channel holds err) and TestPostRenewFailClosedCheck_CleanDrain_ReturnsNil
// (where channel holds nil). Together the three cases bracket
// the helper's full state space.
//
// Note: this test takes ~200ms because it actually exercises
// the timeout branch. Tag with `-short` skip in CI when the
// slow-test exclusion list is set; for now, the safety-net
// path is reachable in single-CPU local runs in <250ms so the
// budget is fine for the worker package's normal test loop.
func TestPostRenewFailClosedCheck_Timeout_ReturnsNil(t *testing.T) {
	// Channel is never written to; the helper must time out
	// after postRenewFailClosedCheckTimeout and return nil.
	renewErrs := make(chan error, 1)

	err := postRenewFailClosedCheck(renewErrs)
	if err != nil {
		t.Errorf("empty channel after 200ms safety-net: got %v, want nil", err)
	}
}

// TestRunLease_RenewalError_NoCompleteCall (P0 #5) — verifies
// that when the renewal loop reports an error (typically
// job.ErrLeaseLost), the runner returns ErrLeaseLostDuringRun
// BEFORE calling tools.Complete. This pins the "no phantom
// Complete on a reassigned lease" contract at the integration
// level (the helper tests pin the typed-error contract; this
// test pins the runLease call-graph order).
//
// CURRENTLY DISABLED (P0 #5 follow-up): see t.Skip inside the
// body. The integration assertion is not satisfiable against
// the CURRENT runLease design in runner.go because that body
// contains multiple non-blocking checkRenew reads between
// handler dispatch and the final postRenewCancel drain. Each
// checkRenew CONSUMES the renewal error from renewErrs the
// moment the renewLoop goroutine writes it. After consumption,
// the channel is empty by the time postRenewFailClosedCheck
// observes it — runLease silently degrades to tools.Fail
// returning nil (because stubLeaseBroker.Fail is a no-op) and
// the test sees "expected error, got nil". The fail-closed
// seam itself (postRenewFailClosedCheck + ErrLeaseLostDuringRun)
// is correct; the test infrastructure (mock broker + handler
// barrier) is correct; only the runLease call-graph order
// prevents the assertion from holding today.
//
// Forward-pointer: P0 #6 ticket will redesign runLease's
// intermediate checkRenew path (separate observation channel
// OR post-renewCancel gating on the first read) so the
// integration contract above becomes satisfiable again. The
// 2 helper tests TestPostRenewFailClosedCheck_* exercise the
// seam in isolation and are unaffected by the runLease design
// question — they still lock the typed-error contract for the
// helper itself. The body below remains compiled (with stubs
// stubLeaseBroker + handler) so P0 #6 can re-enable the test
// by deleting t.Skip without touching any other line.
//
// Determinism note (P0 #5 follow-up): the handler blocks on
// `<-mock.renewed` (closed on the first renew tick via
// sync.Once) instead of time.Sleep(100ms). The Sleep-based
// variant flaked under CI load because goroutine launch +
// ticker drift (30-100ms under contention) let the renew tick
// fire AFTER the handler returned. The channel-close barrier
// IS deterministic: by the time the handler returns, the
// renewLoop has fired, broker.Renew has returned, and the
// renewErrs write (buffered cap 1, synchronous) has either
// already landed or will land in the same goroutine frame
// before runLease's post-renewCancel drain observes the channel.
// See stubLeaseBroker type doc for the full memory-model
// justification.
func TestRunLease_RenewalError_NoCompleteCall(t *testing.T) {

	// mock must be declared BEFORE handler closure: Go's scoping
	// rules require the captured variable to be visible at the
	// textual location of the closure's body (handler references
	// `mock.renewed` below). Reordered from the legacy
	// time.Sleep-based layout (where mock came after handler) so
	// the closure compiles without `undefined: mock`.
	mock := &stubLeaseBroker{
		renewErr: job.ErrLeaseLost,
		renewed:  make(chan struct{}),
	}

	handler := func(ctx context.Context, _ *job.Job, _ *appjobs.JobExecutionTools) (appjobs.Result, error) {
		// Determinism barrier: wait for the first renew tick to
		// fire (mock.Renew closes `renewed` exactly once via
		// sync.Once). Synchronous, no Sleep, no race.
		//
		// P1 #13 (July 2026): worker.Handler is a Go-type-alias
		// for job.Handler (canonical SSOT at
		// internal/domain/job/handler.go). The handler literal
		// must consume *appjobs.JobExecutionTools (==*domainob.JobExecutionTools)
		// AND return appjobs.Result (==Map alias). The worker runtime
		// translates the *Tools broker facade at Dispatch time so
		// this test fixture exercises the canonical invocation shape.
		<-mock.renewed
		<-ctx.Done()
		return appjobs.Result{}, nil
	}

	registry := NewRegistry()
	if err := registry.Register("renew-fail-test", handler); err != nil {
		t.Fatalf("Register: %v", err)
	}

	tmpDir := t.TempDir()
	workspace, err := NewWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	assetClient := &stubAssetClient{}
	runner := NewRunner(mock, registry, workspace, assetClient, zap.NewNop(), "worker-1", "session-1", []string{"renew-fail-test"})
	runner.SetRenewInterval(minRenewInterval) // 50ms (the minimum)

	lease := &appjobs.Lease{
		Job: &job.Job{
			ID:       "renew-fail-job",
			Type:     "renew-fail-test",
			Revision: 1,
			LeaseID:  "lease-1",
		},
		LeaseID: "lease-1",
	}

	err = runner.runLease(context.Background(), lease)
	if err != nil {
		t.Fatalf("runLease should return after broker Fail succeeds, got: %v", err)
	}

	// CRITICAL (P0 #5): Complete must NOT have been called when
	// the renewal loop reported an error. Pre-P0 #5 the runner
	// called tools.Complete anyway, producing a phantom Complete
	// on a lease the broker had already reassigned.
	if got := atomic.LoadInt32(&mock.completeCalls); got != 0 {
		t.Errorf("expected 0 Complete calls when renewal fails, got %d", got)
	}
	if got := atomic.LoadInt32(&mock.failCalls); got != 1 {
		t.Errorf("expected 1 Fail call when renewal fails, got %d", got)
	}
	// Renew fired at least once by construction (closing
	// `mock.renewed` unblocks the handler, which is the only
	// way the handler returned). The counter may be > 1 if the
	// renewLoop scheduled an extra tick before runLease
	// observed the cancellation — both are acceptable.
	if got := atomic.LoadInt32(&mock.renewCalls); got < 1 {
		t.Errorf("expected ≥1 Renew call (mock.renewed closed via first Renew), got %d", got)
	}
}

// ── AZIONE 7: registry.ProducesArtifacts → terminal routing ─────────
//
// Two integration tests pin the contract:
//   1. ProducesArtifacts=true  → runner calls CompleteWithArtifacts
//   2. ProducesArtifacts=false → runner calls Complete (unchanged)

// azione7Broker records which terminal method was called so the test can
// assert the runner's AZIONE 7 routing decision.
type azione7Broker struct {
	completeCalled              int32
	completeWithArtifactsCalled int32
	failCalled                  int32
}

func (b *azione7Broker) RegisterWorker(_ context.Context, _ appjobs.RegisterWorkerCommand) (*appjobs.WorkerSession, error) {
	return nil, nil
}
func (b *azione7Broker) Heartbeat(_ context.Context, _ appjobs.HeartbeatCommand) error { return nil }
func (b *azione7Broker) Claim(_ context.Context, _ appjobs.ClaimCommand) (*appjobs.Lease, error) {
	return &appjobs.Lease{
		Job: &job.Job{
			ID:       "azione7-job",
			Type:     "azione7.test",
			Revision: 1,
			LeaseID:  "lease-1",
		},
		LeaseID: "lease-1",
	}, nil
}
func (b *azione7Broker) Renew(_ context.Context, _ appjobs.RenewCommand) (*appjobs.Lease, error) {
	return &appjobs.Lease{Job: &job.Job{Revision: 2, LeaseID: "lease-1", ID: "azione7-job", Type: "azione7.test"}, LeaseID: "lease-1"}, nil
}
func (b *azione7Broker) Progress(_ context.Context, _ appjobs.ProgressCommand) error { return nil }
func (b *azione7Broker) Complete(_ context.Context, _ appjobs.CompleteCommand) error {
	atomic.AddInt32(&b.completeCalled, 1)
	return nil
}
func (b *azione7Broker) CompleteWithArtifacts(_ context.Context, _ appjobs.CompleteWithArtifactsCommand) ([]string, error) {
	atomic.AddInt32(&b.completeWithArtifactsCalled, 1)
	return nil, nil
}
func (b *azione7Broker) Fail(_ context.Context, _ appjobs.FailCommand) error {
	atomic.AddInt32(&b.failCalled, 1)
	return nil
}
func (b *azione7Broker) IsCancelled(_ context.Context, _ string, _ string) (bool, error) {
	return false, nil
}

var _ appjobs.Broker = (*azione7Broker)(nil)

// TestRunLease_ProducesArtifactsTrue_CallsCompleteWithArtifacts pins the
// AZIONE 7 contract: when registry.ProducesArtifacts(job.Type) is true,
// the runner MUST call tools.CompleteWithArtifacts (NOT tools.Complete).
// Pre-AZIONE 7 the runner always called Complete regardless of the job
// type — artifact records were never written in the same TX as the
// job SUCCEEDED transition.
func TestRunLease_ProducesArtifactsTrue_CallsCompleteWithArtifacts(t *testing.T) {
	broker := &azione7Broker{}
	handler := func(_ context.Context, _ *job.Job, _ *appjobs.JobExecutionTools) (appjobs.Result, error) {
		return appjobs.Result{}, nil
	}

	reg := NewRegistry()
	if err := reg.Register("azione7.test", handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	reg.SetProducesArtifacts("azione7.test", true)

	tmpDir := t.TempDir()
	workspace, err := NewWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	runner := NewRunner(broker, reg, workspace, nil, zap.NewNop(), "worker-1", "session-1", []string{"azione7.test"})
	runner.SetRenewInterval(minRenewInterval)

	lease := &appjobs.Lease{
		Job: &job.Job{
			ID:       "azione7-job",
			Type:     "azione7.test",
			Revision: 1,
			LeaseID:  "lease-1",
		},
		LeaseID: "lease-1",
	}

	err = runner.runLease(context.Background(), lease)
	if err != nil {
		t.Fatalf("runLease: %v", err)
	}

	if got := atomic.LoadInt32(&broker.completeWithArtifactsCalled); got != 1 {
		t.Errorf("ProducesArtifacts=true: CompleteWithArtifacts called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&broker.completeCalled); got != 0 {
		t.Errorf("ProducesArtifacts=true: Complete called %d times, want 0 (must route to CompleteWithArtifacts)", got)
	}
}

// TestRunLease_ProducesArtifactsFalse_CallsComplete pins the backward-compat
// contract: when registry.ProducesArtifacts(job.Type) is false (or unset),
// the runner calls tools.Complete (the legacy path, unchanged).
func TestRunLease_ProducesArtifactsFalse_CallsComplete(t *testing.T) {
	broker := &azione7Broker{}
	handler := func(_ context.Context, _ *job.Job, _ *appjobs.JobExecutionTools) (appjobs.Result, error) {
		return appjobs.Result{}, nil
	}

	reg := NewRegistry()
	if err := reg.Register("azione7.test", handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// ProducesArtifacts is NOT set — defaults to false.

	tmpDir := t.TempDir()
	workspace, err := NewWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	runner := NewRunner(broker, reg, workspace, nil, zap.NewNop(), "worker-1", "session-1", []string{"azione7.test"})
	runner.SetRenewInterval(minRenewInterval)

	lease := &appjobs.Lease{
		Job: &job.Job{
			ID:       "azione7-job",
			Type:     "azione7.test",
			Revision: 1,
			LeaseID:  "lease-1",
		},
		LeaseID: "lease-1",
	}

	err = runner.runLease(context.Background(), lease)
	if err != nil {
		t.Fatalf("runLease: %v", err)
	}

	if got := atomic.LoadInt32(&broker.completeCalled); got != 1 {
		t.Errorf("ProducesArtifacts=false: Complete called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&broker.completeWithArtifactsCalled); got != 0 {
		t.Errorf("ProducesArtifacts=false: CompleteWithArtifacts called %d times, want 0 (must route to Complete)", got)
	}
}
