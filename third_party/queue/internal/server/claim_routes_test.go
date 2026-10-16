package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

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
