package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The batch endpoint exists so one RENDER_BATCH frame can carry several
// claimed jobs. It must be additive: an empty queue is a 204, a short window
// is a 200 with what exists, and every job in the window keeps its own lease.
func TestClaimBatchReturnsAWindowAndNoContentWhenEmpty(t *testing.T) {
	ts := newServer(t)
	if resp := post(t, ts.URL+"/jobs/claim/batch", `{"worker":"bw","limit":3}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("empty batch claim: want 204, got %d", resp.StatusCode)
	}
	for _, id := range []string{"batch-1", "batch-2"} {
		if resp := post(t, ts.URL+"/jobs", `{"id":"`+id+`","render_plan":{},"assets":[]}`); resp.StatusCode != http.StatusCreated {
			t.Fatalf("submit %s: want 201, got %d", id, resp.StatusCode)
		}
	}

	resp := post(t, ts.URL+"/jobs/claim/batch", `{"worker":"bw","limit":3}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batch claim: want 200, got %d", resp.StatusCode)
	}
	var envelope struct {
		Jobs []struct {
			ID              string `json:"id"`
			State           string `json:"state"`
			LeaseGeneration int64  `json:"lease_generation"`
		} `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if len(envelope.Jobs) != 2 {
		t.Fatalf("batch window = %d jobs, want 2 (short window, not an error)", len(envelope.Jobs))
	}
	seen := map[string]bool{}
	for _, job := range envelope.Jobs {
		if seen[job.ID] {
			t.Fatalf("job %q appears twice in one window", job.ID)
		}
		seen[job.ID] = true
		if job.State != "running" {
			t.Fatalf("job %q state = %q, want running", job.ID, job.State)
		}
		if job.LeaseGeneration <= 0 {
			t.Fatalf("job %q lease_generation = %d, want > 0", job.ID, job.LeaseGeneration)
		}
	}

	// The window is drained: a second ask must not re-hand a running job.
	if resp := post(t, ts.URL+"/jobs/claim/batch", `{"worker":"bw","limit":3}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("second batch claim: want 204, got %d", resp.StatusCode)
	}

	// A window larger than the protocol allows is a caller bug, not a big claim.
	if resp := post(t, ts.URL+"/jobs/claim/batch", `{"worker":"bw","limit":65}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized batch claim: want 400, got %d", resp.StatusCode)
	}
}

func TestClaimWaitReturnsNoContentWhenNoJobArrives(t *testing.T) {
	ts := newServer(t)
	start := time.Now()
	resp := post(t, ts.URL+"/jobs/claim/wait", `{"worker":"worker","max_wait_ms":60}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("empty claim/wait: want 204, got %d", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("claim/wait returned early after %s", elapsed)
	}
}

func TestClaimWaitWakesOnSubmissionAndCarriesLease(t *testing.T) {
	ts := newServer(t)
	result := make(chan struct {
		status int
		job    struct {
			ID    string        `json:"id"`
			Lease time.Duration `json:"lease"`
		}
		err error
	}, 1)
	go func() {
		resp, err := http.Post(ts.URL+"/jobs/claim/wait", "application/json", strings.NewReader(`{"worker":"worker-wait","max_wait_ms":1500}`))
		if err != nil {
			result <- struct {
				status int
				job    struct {
					ID    string        `json:"id"`
					Lease time.Duration `json:"lease"`
				}
				err error
			}{err: err}
			return
		}
		defer resp.Body.Close()
		got := struct {
			status int
			job    struct {
				ID    string        `json:"id"`
				Lease time.Duration `json:"lease"`
			}
			err error
		}{status: resp.StatusCode}
		if resp.StatusCode == http.StatusOK {
			got.err = json.NewDecoder(resp.Body).Decode(&got.job)
		}
		result <- got
	}()

	time.Sleep(40 * time.Millisecond)
	if resp := post(t, ts.URL+"/jobs", `{"id":"claim-wait-job"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit during wait: want 201, got %d", resp.StatusCode)
	}
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("claim/wait request: %v", got.err)
		}
		if got.status != http.StatusOK || got.job.ID != "claim-wait-job" || got.job.Lease != 30*time.Second {
			t.Fatalf("claim/wait response = status %d, job %+v; want 200, submitted job, 30s lease", got.status, got.job)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("claim/wait did not wake after submission")
	}
}

func TestClaimWaitMalformedRequestIsBadRequest(t *testing.T) {
	ts := newServer(t)
	if resp := post(t, ts.URL+"/jobs/claim/wait", `not-json`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed claim/wait: want 400, got %d", resp.StatusCode)
	}
}
