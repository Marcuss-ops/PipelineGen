package remotejob

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// masterStub is the smallest Master double the phase tests need: it counts the
// PREPARE/FINALIZE calls and answers status reads from a script.
func masterStub(t *testing.T, statuses ...string) (*httptest.Server, *int, *int, *int) {
	t.Helper()
	prepares, finalizes, polls := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/pre":
			prepares++
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job_id":"master-job-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/master-job-1/finalize":
			finalizes++
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs/master-job-1":
			status := "RUNNING"
			if polls < len(statuses) {
				status = statuses[polls]
			}
			polls++
			if status == "FAILED" {
				_, _ = w.Write([]byte(`{"job_id":"master-job-1","status":"FAILED","error":"render exploded"}`))
				return
			}
			_, _ = w.Write([]byte(`{"job_id":"master-job-1","status":"` + status + `"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	return server, &prepares, &finalizes, &polls
}

// TestSplitPhasesAttachWithoutResubmitting pins the split itself: PREPARE once,
// FINALIZE once, and then the poll phase can run any number of times against
// the SAME Master job. That is what makes the wait hand-off-able — a second
// PREPARE would create a second render.
func TestSplitPhasesAttachWithoutResubmitting(t *testing.T) {
	server, prepares, finalizes, polls := masterStub(t, "RUNNING", "RUNNING", "SUCCEEDED")
	defer server.Close()
	client := New(server.URL, "test-token")
	client.PollInterval = time.Millisecond
	client.PollTimeout = time.Second

	jobID, err := client.Prepare(context.Background(), map[string]any{"job_type": "scene.composite.v1"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if jobID != "master-job-1" {
		t.Fatalf("Prepare job id = %q", jobID)
	}
	if err := client.Finalize(context.Background(), jobID, map[string]any{"runtime_assets": []any{}}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// One poll: the render is still running, and the caller keeps the handle.
	pending, err := client.Poll(context.Background(), jobID)
	if !errors.Is(err, ErrRemotePending) {
		t.Fatalf("Poll on a running job = %v, want ErrRemotePending", err)
	}
	if pending.JobID != jobID || pending.Status != "RUNNING" {
		t.Fatalf("pending result = %#v, want the job handle and the raw status", pending)
	}

	// Attach resumes the poll with no second submission.
	got, err := client.Attach(context.Background(), jobID, 5*time.Second)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if got.Status != "SUCCEEDED" {
		t.Fatalf("Attach result = %#v", got)
	}
	if *prepares != 1 || *finalizes != 1 {
		t.Fatalf("PREPARE=%d FINALIZE=%d, want exactly one submission", *prepares, *finalizes)
	}
	if *polls < 3 {
		t.Fatalf("GET=%d, want the poll phase to have run every time", *polls)
	}
}

// TestAttachBudgetYieldsTheJobHandle pins the yield contract: a bounded attach
// returns the job handle and ErrRemotePending instead of throwing the handle
// away in a failure, and it returns when the budget expires rather than holding
// the caller's worker for the client's full poll timeout.
func TestAttachBudgetYieldsTheJobHandle(t *testing.T) {
	server, _, _, _ := masterStub(t, "RUNNING")
	defer server.Close()
	client := New(server.URL, "test-token")
	client.PollInterval = time.Millisecond
	client.PollTimeout = time.Hour

	started := time.Now()
	result, err := client.Attach(context.Background(), "master-job-1", 40*time.Millisecond)
	elapsed := time.Since(started)
	if !errors.Is(err, ErrRemotePending) {
		t.Fatalf("bounded Attach = %v, want ErrRemotePending", err)
	}
	if result.JobID != "master-job-1" || result.Status != "RUNNING" {
		t.Fatalf("yielded result = %#v, want the job handle preserved", result)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("bounded Attach held the caller for %s, want about the 40ms budget", elapsed)
	}
}

// TestPollSeparatesTerminalFailureFromPending pins that a dead job is NOT
// reported as "still running": the two decisions need different handling, and
// only one of them is worth retrying.
func TestPollSeparatesTerminalFailureFromPending(t *testing.T) {
	server, _, _, _ := masterStub(t, "FAILED")
	defer server.Close()
	client := New(server.URL, "test-token")
	client.PollInterval = time.Millisecond

	result, err := client.Poll(context.Background(), "master-job-1")
	if err == nil {
		t.Fatal("Poll on a failed job returned no error")
	}
	if errors.Is(err, ErrRemotePending) {
		t.Fatalf("Poll on a failed job reported pending: %v", err)
	}
	if result.Status != "FAILED" || !strings.Contains(err.Error(), "render exploded") {
		t.Fatalf("result = %#v err = %v, want the terminal failure and its reason", result, err)
	}
}

// TestAttachDecomposesQueueExecutionAndPollLag pins the phase decomposition the
// remote-final-job stage relies on: the Master's created_at/started_at/
// completed_at produce queue and execution, while the attach loop reports its
// own poll count and poll lag. Missing boundaries stay zero, never negative.
func TestAttachDecomposesQueueExecutionAndPollLag(t *testing.T) {
	now := time.Now().UTC()
	created := now.Add(-120 * time.Second).Format(time.RFC3339Nano)
	startedAt := now.Add(-100 * time.Second).Format(time.RFC3339Nano)
	completedAt := now.Add(-2 * time.Second).Format(time.RFC3339Nano)
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/jobs/master-job-1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		polls++
		switch polls {
		case 1:
			_, _ = w.Write([]byte(`{"job_id":"master-job-1","status":"QUEUED","created_at":"` + created + `"}`))
		case 2:
			_, _ = w.Write([]byte(`{"job_id":"master-job-1","status":"RUNNING","created_at":"` + created + `","started_at":"` + startedAt + `"}`))
		default:
			_, _ = w.Write([]byte(`{"job_id":"master-job-1","status":"SUCCEEDED","artifact_url":"http://master/final.mp4","created_at":"` + created + `","started_at":"` + startedAt + `","completed_at":"` + completedAt + `"}`))
		}
	}))
	defer server.Close()
	client := New(server.URL, "test-token")
	client.PollInterval = time.Millisecond
	client.PollTimeout = 5 * time.Second

	got, err := client.Attach(context.Background(), "master-job-1", 5*time.Second)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if got.Status != "SUCCEEDED" {
		t.Fatalf("Attach result = %#v", got)
	}
	if got.Timing.QueueWait != 20*time.Second {
		t.Fatalf("queue wait = %s, want 20s (created_at → started_at)", got.Timing.QueueWait)
	}
	if got.Timing.Execution != 98*time.Second {
		t.Fatalf("execution = %s, want 98s (started_at → completed_at)", got.Timing.Execution)
	}
	if got.Timing.Polls != 3 {
		t.Fatalf("polls = %d, want 3", got.Timing.Polls)
	}
	if got.Timing.Wait <= 0 {
		t.Fatalf("wait = %s, want the attach wall", got.Timing.Wait)
	}
	if got.Timing.PollLag < time.Second {
		t.Fatalf("poll lag = %s, want the observed completion delay", got.Timing.PollLag)
	}
	if got.Timing.CreatedAt.IsZero() || !got.Timing.CreatedAt.Before(got.Timing.StartedAt) {
		t.Fatalf("Master boundaries were not parsed: created=%s started=%s", got.Timing.CreatedAt, got.Timing.StartedAt)
	}
}

// TestTimingWithoutMasterBoundariesStaysZero pins the fallback: a Master that
// exposes only a status cannot produce a phase, and an unstarted phase must not
// turn into a negative duration.
func TestTimingWithoutMasterBoundariesStaysZero(t *testing.T) {
	server, _, _, _ := masterStub(t, "SUCCEEDED")
	defer server.Close()
	client := New(server.URL, "test-token")
	client.PollInterval = time.Millisecond
	client.PollTimeout = time.Second
	got, err := client.Attach(context.Background(), "master-job-1", time.Second)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if got.Timing.QueueWait != 0 || got.Timing.Execution != 0 || got.Timing.PollLag != 0 {
		t.Fatalf("timing = %#v, want zero phases when the Master reports none", got.Timing)
	}
	if got.Timing.Polls != 1 {
		t.Fatalf("polls = %d, want 1", got.Timing.Polls)
	}
}

func TestSubmitPerformsCompleteMasterHandoff(t *testing.T) {
	prepares, finalizes, gets := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/pre":
			prepares++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode PREPARE payload: %v", err)
			}
			if payload["job_type"] != "scene.composite.v1" || payload["scenes"] == nil {
				t.Errorf("PREPARE payload = %#v, want scene plan", payload)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job_id":"master-job-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/master-job-1/finalize":
			finalizes++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode FINALIZE payload: %v", err)
			}
			if payload["runtime_assets"] == nil || payload["overlays"] == nil {
				t.Errorf("FINALIZE payload is missing runtime assets or overlays: %#v", payload)
			}
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs/master-job-1":
			gets++
			_, _ = w.Write([]byte(`{"job_id":"master-job-1","status":"SUCCEEDED"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := New(server.URL, "test-token")
	client.PollInterval = time.Millisecond
	client.PollTimeout = time.Second
	got, err := client.Submit(context.Background(),
		map[string]any{"job_type": "scene.composite.v1", "scenes": []any{map[string]any{"stock": map[string]any{"drive_file_id": "stock-drive"}}}},
		map[string]any{"runtime_assets": []any{map[string]any{"role": "final_audio"}}, "overlays": []any{}},
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got.JobID != "master-job-1" || got.Status != "SUCCEEDED" {
		t.Fatalf("Submit result = %#v", got)
	}
	if prepares != 1 || finalizes != 1 || gets == 0 {
		t.Fatalf("request counts: PREPARE=%d FINALIZE=%d GET=%d, want one complete handoff and at least one poll", prepares, finalizes, gets)
	}
}

func TestPollReadsFlatMasterArtifactFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/jobs/master-job-1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"job_id":"master-job-1","status":"SUCCEEDED","artifact_url":"http://master/artifacts/final.mp4","sha256":"deadbeef"}`))
	}))
	defer server.Close()

	got, err := New(server.URL, "test-token").Poll(context.Background(), "master-job-1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got.ArtifactURL != "http://master/artifacts/final.mp4" || got.SHA256 != "deadbeef" {
		t.Fatalf("Poll receipt = %#v, want the flat artifact URL and checksum", got)
	}
}

// TestWorkerTimingIsRelayed pins the worker-side split: the Master relays the
// worker's own phases verbatim, so a render's execution time can be attributed
// to download/decode/compose/encode/mux/upload instead of staying one number.
func TestWorkerTimingIsRelayed(t *testing.T) {
	body := `{
  "job_id": "job_worker_1",
  "status": "SUCCEEDED",
  "worker_id": "velox-worker-523925eb",
  "artifact_url": "https://objectstore/final.mp4",
  "sha256": "abc",
  "created_at": "2026-11-01T08:00:00Z",
  "started_at": "2026-11-01T08:00:30Z",
  "completed_at": "2026-11-01T08:05:00Z",
  "worker_timing": {
    "leased_at": "2026-11-01T08:00:30Z",
    "started_at": "2026-11-01T08:00:32Z",
    "finished_at": "2026-11-01T08:04:59Z",
    "download_ms": 1200,
    "decode_ms": 3400,
    "compose_ms": 182000,
    "encode_ms": 96000,
    "mux_ms": 900,
    "upload_ms": 14800,
    "frames": 6550,
    "encode_fps": 68.2
  }
}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	client := New(server.URL, "test-token")
	got, err := client.Poll(context.Background(), "job_worker_1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got.Timing.Worker.Compose != 182*time.Second || got.Timing.Worker.Encode != 96*time.Second {
		t.Fatalf("worker phases = %#v, want compose 182s encode 96s", got.Timing.Worker)
	}
	if got.Timing.Worker.Upload != 14800*time.Millisecond || got.Timing.Worker.Decode != 3400*time.Millisecond {
		t.Fatalf("worker transfer phases = %#v", got.Timing.Worker)
	}
	if got.Timing.Worker.Frames != 6550 || got.Timing.Worker.EncodeFPS != 68.2 {
		t.Fatalf("worker frames = %d fps = %v", got.Timing.Worker.Frames, got.Timing.Worker.EncodeFPS)
	}
	if !got.Timing.Worker.StartedAt.Equal(time.Date(2026, 11, 1, 8, 0, 32, 0, time.UTC)) {
		t.Fatalf("worker started_at = %s", got.Timing.Worker.StartedAt)
	}
}

// TestWorkerTimingSurvivesTheJobWrapper pins the older-Master shape: the status
// object is nested while worker_timing stays at the top level. Dropping it there
// would silently lose the only worker-side telemetry.
func TestWorkerTimingSurvivesTheJobWrapper(t *testing.T) {
	body := `{
  "job": {"job_id": "job_wrapped", "status": "SUCCEEDED"},
  "worker_timing": {"encode_ms": 4200, "frames": 120}
}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	client := New(server.URL, "test-token")
	got, err := client.Poll(context.Background(), "job_wrapped")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got.Timing.Worker.Encode != 4200*time.Millisecond || got.Timing.Worker.Frames != 120 {
		t.Fatalf("worker timing = %#v, want the top-level block kept", got.Timing.Worker)
	}
}

// TestMalformedWorkerTimingStaysUnknown pins the observability contract: a
// garbled or negative worker report is "not reported", never an error and never
// a negative duration that would corrupt the total.
func TestMalformedWorkerTimingStaysUnknown(t *testing.T) {
	cases := []string{
		`{"status":"SUCCEEDED","worker_timing":"nope"}`,
		`{"status":"SUCCEEDED","worker_timing":{"encode_ms":-5000,"upload_ms":-1}}`,
	}
	for _, body := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		client := New(server.URL, "test-token")
		got, err := client.Poll(context.Background(), "job_bad")
		server.Close()
		if err != nil {
			t.Fatalf("Poll(%s): %v", body, err)
		}
		if got.Timing.Worker.Encode < 0 || got.Timing.Worker.Upload < 0 {
			t.Fatalf("worker timing(%s) = %#v, want no negative phase", body, got.Timing.Worker)
		}
	}
}
