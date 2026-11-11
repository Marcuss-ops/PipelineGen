// Package wiring — remote_final_job_test.go pins the split final-job handoff at
// the adapter boundary: the wait budget knob, the transport→capability error
// mapping, and the handle projection that keeps the Master job id even when the
// wait expires. Those three facts are what let the runner hand the wait back to
// a later attempt without losing (or duplicating) the remote render.
package script

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remotejob"
)

func TestFinalJobAttachBudgetKnob(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		want    time.Duration
		wantErr bool
	}{
		{name: "unset uses the default", env: "", want: defaultFinalJobAttachBudget},
		{name: "explicit seconds", env: "30", want: 30 * time.Second},
		{name: "zero disables the hand-off and restores the blocking wait", env: "0", want: 0},
		{name: "malformed fails closed", env: "soon", wantErr: true},
		{name: "negative fails closed", env: "-5", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VELOX_FINAL_JOB_ATTACH_SECONDS", tc.env)
			got, err := finalJobAttachBudget()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("budget = %v, want a configuration error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("finalJobAttachBudget: %v", err)
			}
			if got != tc.want {
				t.Fatalf("budget = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFinalJobQueueBudgetKnob(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		want    time.Duration
		wantErr bool
	}{
		{name: "unset uses the default", env: "", want: defaultFinalJobQueueBudget},
		{name: "explicit seconds", env: "120", want: 2 * time.Minute},
		{name: "zero disables the queue budget", env: "0", want: 0},
		{name: "malformed fails closed", env: "soon", wantErr: true},
		{name: "negative fails closed", env: "-1", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VELOX_FINAL_JOB_QUEUE_SECONDS", tc.env)
			got, err := finalJobQueueBudget()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("queue budget = %v, want a configuration error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("finalJobQueueBudget: %v", err)
			}
			if got != tc.want {
				t.Fatalf("queue budget = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFinalJobWorkerPinIsOptIn pins the r9 remediation: there is no built-in
// worker pin. An empty setting means the Master places the job, and the
// retired worker-51 default is rejected instead of silently re-applied.
func TestFinalJobWorkerPinIsOptIn(t *testing.T) {
	t.Setenv("VELOX_FINAL_JOB_WORKER_ID", "")
	pin, err := finalJobWorkerPin()
	if err != nil {
		t.Fatalf("unset pin: %v", err)
	}
	if pin != "" {
		t.Fatalf("unset pin = %q, want master placement", pin)
	}

	t.Setenv("VELOX_FINAL_JOB_WORKER_ID", "  host_57_129_132_133  ")
	pin, err = finalJobWorkerPin()
	if err != nil {
		t.Fatalf("explicit pin: %v", err)
	}
	if pin != "host_57_129_132_133" {
		t.Fatalf("explicit pin = %q, want the trimmed worker id", pin)
	}

	t.Setenv("VELOX_FINAL_JOB_WORKER_ID", "velox-worker-523925eb")
	if _, err := finalJobWorkerPin(); err == nil {
		t.Fatal("the retired worker-51 pin must be rejected, not silently honoured")
	}
}

func TestFinalJobWaitErrorMapsTheTransportSentinel(t *testing.T) {
	if err := finalJobWaitError(nil); err != nil {
		t.Fatalf("nil must stay nil, got %v", err)
	}
	pending := finalJobWaitError(remotejob.ErrRemotePending)
	if !errors.Is(pending, scriptgen.ErrFinalJobPending) {
		t.Fatalf("error %v does not match the capability sentinel", pending)
	}
	if !errors.Is(pending, remotejob.ErrRemotePending) {
		t.Fatalf("error %v lost the transport sentinel", pending)
	}
	terminal := finalJobWaitError(errors.New("remote job master-1 ended FAILED: render exploded"))
	if errors.Is(terminal, scriptgen.ErrFinalJobPending) {
		t.Fatalf("a terminal Master failure must not be reported as a deferral: %v", terminal)
	}
}

// TestFinalJobResultKeepsTheHandleWhenTheWaitFails: the Master job exists
// whether or not the wait observed it finish, so the receipt must always carry
// its id. Losing it is what turns a resumable render into a duplicate one.
func TestFinalJobResultKeepsTheHandleWhenTheWaitFails(t *testing.T) {
	fromWait := finalJobResult("master-9", remotejob.Result{Status: "RUNNING"})
	if fromWait.JobID != "master-9" || fromWait.Status != "RUNNING" {
		t.Fatalf("receipt = %+v, want the prepared id with its raw status", fromWait)
	}
	fromResult := finalJobResult("master-9", remotejob.Result{
		JobID: "master-9", Status: "SUCCEEDED", WorkerID: "w-1",
		ArtifactURL: "velox-drive://final", SHA256: "deadbeef",
	})
	if fromResult.Status != "SUCCEEDED" || fromResult.ArtifactURL != "velox-drive://final" || fromResult.SHA256 != "deadbeef" || fromResult.WorkerID != "w-1" {
		t.Fatalf("receipt = %+v, want the full terminal projection", fromResult)
	}
}

func TestRemoteJobInFlightDistinguishesPreparedPendingFromDispatched(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{status: "PENDING", want: false},
		{status: "PREPARING", want: false},
		{status: "RUNNING", want: true},
		{status: "QUEUED", want: true},
		{status: "RENDERING", want: true},
		{status: "SUCCEEDED", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			if got := remoteJobInFlight(tc.status); got != tc.want {
				t.Fatalf("remoteJobInFlight(%q) = %t, want %t", tc.status, got, tc.want)
			}
		})
	}
}

// finalJobMasterStub answers status reads for one job; every read is counted so
// a test can tell "stopped at the budget" from "waited the poll timeout".
func finalJobMasterStub(t *testing.T, status string, body string) (*httptest.Server, *int) {
	t.Helper()
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/jobs/master-9" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		reads++
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	_ = status
	return server, &reads
}

func newAttachTestAdapter(t *testing.T, baseURL string, budget, pollTimeout time.Duration) *RemoteFinalJobAdapter {
	t.Helper()
	client := remotejob.New(baseURL, "test-token")
	client.PollInterval = 5 * time.Millisecond
	client.PollTimeout = pollTimeout
	return &RemoteFinalJobAdapter{client: client, attachBudget: budget}
}

// TestAttachFinalJobReportsPendingWithTheHandleWhenTheBudgetExpires is the
// adapter-level proof of the split: the bounded wait ends on ITS budget (not on
// the render) and still returns a receipt the runner can resume from.
func TestAttachFinalJobReportsPendingWithTheHandleWhenTheBudgetExpires(t *testing.T) {
	server, reads := finalJobMasterStub(t, "RUNNING", `{"job_id":"master-9","status":"RUNNING"}`)
	adapter := newAttachTestAdapter(t, server.URL, 30*time.Millisecond, 5*time.Second)

	started := time.Now()
	receipt, err := adapter.AttachFinalJob(context.Background(), "master-9", false)
	elapsed := time.Since(started)

	if !errors.Is(err, scriptgen.ErrFinalJobPending) {
		t.Fatalf("err = %v, want the capability pending sentinel", err)
	}
	if receipt.JobID != "master-9" || receipt.Status != "RUNNING" {
		t.Fatalf("receipt = %+v, want the Master handle", receipt)
	}
	if *reads < 1 {
		t.Fatal("the bounded wait never polled the Master")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("bounded wait took %s; it must end on the attach budget, not the poll timeout", elapsed)
	}
}

// TestAttachFinalJobWaitToCompletionIgnoresTheBudget pins the second half of the
// churn bound: once the wait has been handed back, the following attempt waits
// the job out with the full poll timeout instead of yielding again.
func TestAttachFinalJobWaitToCompletionIgnoresTheBudget(t *testing.T) {
	server, _ := finalJobMasterStub(t, "RUNNING", `{"job_id":"master-9","status":"RUNNING"}`)
	adapter := newAttachTestAdapter(t, server.URL, 10*time.Millisecond, 60*time.Millisecond)

	started := time.Now()
	receipt, err := adapter.AttachFinalJob(context.Background(), "master-9", true)
	elapsed := time.Since(started)

	if !errors.Is(err, scriptgen.ErrFinalJobPending) {
		t.Fatalf("err = %v, want the pending sentinel after the poll timeout", err)
	}
	if receipt.JobID != "master-9" {
		t.Fatalf("receipt = %+v, want the handle preserved through the timeout", receipt)
	}
	if elapsed < 45*time.Millisecond {
		t.Fatalf("wait-to-completion ended after %s; it must not stop at the 10ms attach budget", elapsed)
	}
}

// TestAttachFinalJobProjectsACompletedRender: the resume path returns the
// terminal projection, so a run that comes back after the render finished
// records the artifact exactly as a single blocking call used to.
func TestAttachFinalJobProjectsACompletedRender(t *testing.T) {
	server, _ := finalJobMasterStub(t, "SUCCEEDED",
		`{"job_id":"master-9","status":"SUCCEEDED","worker_id":"w-3","artifact":{"url":"velox-drive://final","sha256":"cafe"}}`)
	adapter := newAttachTestAdapter(t, server.URL, 30*time.Millisecond, time.Second)

	receipt, err := adapter.AttachFinalJob(context.Background(), "master-9", false)
	if err != nil {
		t.Fatalf("AttachFinalJob: %v", err)
	}
	if receipt.JobID != "master-9" || receipt.Status != "SUCCEEDED" {
		t.Fatalf("receipt = %+v, want the terminal receipt", receipt)
	}
	if receipt.ArtifactURL != "velox-drive://final" || receipt.SHA256 != "cafe" || receipt.WorkerID != "w-3" {
		t.Fatalf("receipt = %+v, want the artifact projection", receipt)
	}
}

// TestAttachFinalJobProjectsMasterTiming: the durable receipt must carry the
// phase split, not only the total. The Master reports its own boundaries; the
// adapter maps them onto the capability type and adds the poll count.
func TestAttachFinalJobProjectsMasterTiming(t *testing.T) {
	now := time.Now().UTC()
	created := now.Add(-140 * time.Second).Format(time.RFC3339Nano)
	startedAt := now.Add(-120 * time.Second).Format(time.RFC3339Nano)
	completedAt := now.Add(-10 * time.Second).Format(time.RFC3339Nano)
	server, _ := finalJobMasterStub(t, "SUCCEEDED",
		`{"job_id":"master-9","status":"SUCCEEDED","artifact_url":"https://master/final.mp4",`+
			`"created_at":"`+created+`","started_at":"`+startedAt+`","completed_at":"`+completedAt+`"}`)
	adapter := newAttachTestAdapter(t, server.URL, time.Second, time.Second)

	receipt, err := adapter.AttachFinalJob(context.Background(), "master-9", false)
	if err != nil {
		t.Fatalf("AttachFinalJob: %v", err)
	}
	if receipt.Timing == nil {
		t.Fatal("receipt has no timing block")
	}
	if receipt.Timing.QueueMS != 20_000 {
		t.Fatalf("queue_ms = %d, want 20000 (created_at → started_at)", receipt.Timing.QueueMS)
	}
	if receipt.Timing.ExecutionMS != 110_000 {
		t.Fatalf("execution_ms = %d, want 110000 (started_at → completed_at)", receipt.Timing.ExecutionMS)
	}
	if receipt.Timing.Polls < 1 {
		t.Fatalf("polls = %d, want at least one", receipt.Timing.Polls)
	}
	if receipt.Timing.MasterCompletedAt == "" {
		t.Fatal("master_completed_at was not persisted; the phase split cannot be audited")
	}
}

// TestMergeFinalJobTimingKeepsBothSides: the handoff is measured locally and
// the Master phases arrive from the attach. Neither may erase the other, and an
// unknown phase stays absent instead of becoming a fake zero measurement.
func TestMergeFinalJobTimingKeepsBothSides(t *testing.T) {
	local := &scriptgen.RemoteFinalJobTiming{HandoffMS: 4200}
	merged := mergeFinalJobTiming(local, &scriptgen.RemoteFinalJobTiming{QueueMS: 30_000, ExecutionMS: 260_000, Polls: 9})
	if merged.HandoffMS != 4200 || merged.QueueMS != 30_000 || merged.ExecutionMS != 260_000 || merged.Polls != 9 {
		t.Fatalf("merged = %+v, want the local handoff plus the Master phases", merged)
	}
	if merged.WaitMS != 0 || merged.PollLagMS != 0 {
		t.Fatalf("merged = %+v, want absent phases to stay zero", merged)
	}
	if finalJobTiming(remotejob.Timing{}) != nil {
		t.Fatal("an unmeasured wait must not project a timing block")
	}
}

func TestAttachFinalJobRequiresAJobID(t *testing.T) {
	adapter := &RemoteFinalJobAdapter{client: remotejob.New("http://127.0.0.1:1", "t")}
	if _, err := adapter.AttachFinalJob(context.Background(), "  ", false); err == nil {
		t.Fatal("an empty job id must be refused: there is nothing to attach to")
	}
}

// TestFinalJobTimingProjectsTheWorkerSplit is the projection proof: the worker
// phases relayed by the Master must reach the durable receipt, and their
// absence must stay nil instead of becoming a block of zeroes.
func TestFinalJobTimingProjectsTheWorkerSplit(t *testing.T) {
	timing := remotejob.Timing{
		CreatedAt:   time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC),
		StartedAt:   time.Date(2026, 11, 1, 8, 0, 30, 0, time.UTC),
		CompletedAt: time.Date(2026, 11, 1, 8, 5, 0, 0, time.UTC),
		Execution:   4*time.Minute + 30*time.Second,
		Worker: remotejob.WorkerTiming{
			Compose:   182 * time.Second,
			Encode:    96 * time.Second,
			Upload:    14800 * time.Millisecond,
			Frames:    6550,
			EncodeFPS: 68.2,
		},
	}
	projected := finalJobTiming(timing)
	if projected == nil || projected.Worker == nil {
		t.Fatalf("projected = %#v, want a worker block", projected)
	}
	if projected.Worker.ComposeMS != 182000 || projected.Worker.EncodeMS != 96000 {
		t.Fatalf("worker block = %#v, want compose 182000 encode 96000", projected.Worker)
	}
	if projected.Worker.Frames != 6550 || projected.Worker.EncodeFPS != 68.2 {
		t.Fatalf("worker frames = %#v", projected.Worker)
	}
	if empty := finalJobTiming(remotejob.Timing{}); empty != nil {
		t.Fatalf("empty timing = %#v, want nil (nothing was measured)", empty)
	}
}
