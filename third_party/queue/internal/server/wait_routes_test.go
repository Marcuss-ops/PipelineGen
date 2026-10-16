package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/service"
)

func TestJobWaitReturnsTerminalJobAndNotFound(t *testing.T) {
	ts := newServer(t)
	post(t, ts.URL+"/jobs", `{"id":"job-1","render_plan":{"n":1}}`)
	post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)
	post(t, ts.URL+"/jobs/job-1/complete", `{"worker":"w1","data":{"storage_key":"abc","artifact_hash":"abc","size_bytes":1,"content_type":"video/mp4"}}`)

	resp, err := http.Get(ts.URL + "/jobs/job-1/wait?max_wait_ms=1000")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wait on terminal job: want 200, got %d", resp.StatusCode)
	}
	var job struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.State != "completed" {
		t.Fatalf("state = %q, want completed", job.State)
	}

	missing, err := http.Get(ts.URL + "/jobs/nope/wait?max_wait_ms=1000")
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("wait on unknown job: want 404, got %d", missing.StatusCode)
	}
}

func TestJobWaitTimesOutWithCurrentState(t *testing.T) {
	ts := newServer(t)
	post(t, ts.URL+"/jobs", `{"id":"job-1","render_plan":{"n":1}}`)

	start := time.Now()
	resp, err := http.Get(ts.URL + "/jobs/job-1/wait?max_wait_ms=200")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wait timeout: want 200 with the current job, got %d", resp.StatusCode)
	}
	var job struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.State != "pending" {
		t.Fatalf("state = %q, want pending", job.State)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("returned after %s; expected to honor max_wait_ms", elapsed)
	}
}

func TestJobWaitRejectsInvalidParameter(t *testing.T) {
	ts := newServer(t)
	resp, err := http.Get(ts.URL + "/jobs/job-1/wait?max_wait_ms=abc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid max_wait_ms: want 400, got %d", resp.StatusCode)
	}
}

func TestJobWaitWakesOnCompletion(t *testing.T) {
	repo := memory.New(30*time.Second, 3)
	svc := service.New(repo)
	ts := newServerWithService(t, svc)

	post(t, ts.URL+"/jobs", `{"id":"job-1","render_plan":{"n":1}}`)
	post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)

	result := make(chan int, 1)
	go func() {
		resp, err := http.Get(ts.URL + "/jobs/job-1/wait?max_wait_ms=1000")
		if err != nil {
			result <- 0
			return
		}
		defer resp.Body.Close()
		result <- resp.StatusCode
	}()

	// Park the HTTP handler on the wake channel, then complete the job. The
	// terminal transition must wake the producer long-poll immediately instead
	// of waiting out max_wait_ms.
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	resp := post(t, ts.URL+"/jobs/job-1/complete", `{"worker":"w1","data":{"storage_key":"abc","artifact_hash":"abc","size_bytes":1,"content_type":"video/mp4"}}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("complete: want 204, got %d", resp.StatusCode)
	}

	select {
	case status := <-result:
		if status != http.StatusOK {
			t.Fatalf("wait: want 200, got %d", status)
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Fatalf("completion wake took %s; expected event-driven wake", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("job wait did not wake on completion")
	}
}

func TestJobWaitCancellationNotifies(t *testing.T) {
	repo := memory.New(30*time.Second, 3)
	svc := service.New(repo)
	ts := newServerWithService(t, svc)

	post(t, ts.URL+"/jobs", `{"id":"job-1","render_plan":{"n":1}}`)

	result := make(chan string, 1)
	go func() {
		resp, err := http.Get(ts.URL + "/jobs/job-1/wait?max_wait_ms=1000")
		if err != nil {
			result <- "error"
			return
		}
		defer resp.Body.Close()
		var job struct {
			State string `json:"state"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&job)
		result <- job.State
	}()

	time.Sleep(100 * time.Millisecond)
	if err := svc.Cancel(context.Background(), "job-1"); err != nil {
		t.Fatal(err)
	}

	select {
	case state := <-result:
		if state != "cancelled" {
			t.Fatalf("state = %q, want cancelled", state)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("job wait did not wake on cancellation")
	}
}

// newServerWithService exposes the service backing an httptest server so a
// test can drive a state transition directly and prove the long-poll wake.
func newServerWithService(t *testing.T, svc *service.Service) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(New(svc).Handler())
	t.Cleanup(ts.Close)
	return ts
}
