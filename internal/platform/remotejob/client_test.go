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
