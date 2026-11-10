package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/service"
)

// TestLeaseGenerationRejectsStaleHTTPReport proves the token survives the
// whole wire path: claim returns it, the report body carries it, the handler
// and service forward it, and the repository rejects the first attempt's
// generation after the same worker re-claims the job.
func TestLeaseGenerationRejectsStaleHTTPReport(t *testing.T) {
	ctx := context.Background()
	repo := memory.New(10*time.Millisecond, 3)
	svc := service.New(repo)
	ts := httptest.NewServer(New(svc).Handler())
	t.Cleanup(ts.Close)

	resp := post(t, ts.URL+"/jobs", `{"id":"fence-http","render_plan":{},"assets":[]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit: want 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first claim: want 200, got %d", resp.StatusCode)
	}
	var first model.Job
	if err := json.NewDecoder(resp.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if first.LeaseGeneration == 0 {
		t.Fatal("claim response did not carry lease_generation")
	}

	time.Sleep(20 * time.Millisecond)
	if _, err := svc.RequeueExpired(ctx, time.Now()); err != nil {
		t.Fatalf("requeue expired: %v", err)
	}

	resp = post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second claim: want 200, got %d", resp.StatusCode)
	}
	var second model.Job
	if err := json.NewDecoder(resp.Body).Decode(&second); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if second.LeaseGeneration <= first.LeaseGeneration {
		t.Fatalf("generation did not advance: first=%d second=%d", first.LeaseGeneration, second.LeaseGeneration)
	}

	staleBody := fmt.Sprintf(`{"worker":"w1","lease_generation":%d,"data":{"reason":"late"}}`, first.LeaseGeneration)
	resp = post(t, ts.URL+"/jobs/fence-http/fail", staleBody)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale report: want 409, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err := http.Get(ts.URL + "/jobs/fence-http")
	if err != nil {
		t.Fatal(err)
	}
	var afterStale model.Job
	if err := json.NewDecoder(resp.Body).Decode(&afterStale); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if afterStale.State != model.StateRunning || afterStale.LeaseGeneration != second.LeaseGeneration {
		t.Fatalf("stale report changed the job: state=%s generation=%d", afterStale.State, afterStale.LeaseGeneration)
	}

	currentBody := fmt.Sprintf(`{"worker":"w1","lease_generation":%d,"data":{"reason":"current"}}`, second.LeaseGeneration)
	resp = post(t, ts.URL+"/jobs/fence-http/fail", currentBody)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("current report: want 204, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestLeaseGenerationRequiredForRenewAndProgress pins that the non-terminal
// worker transitions are fenced too: omitting the token is a 400, not a
// generation-0 compatibility path.
func TestLeaseGenerationRequiredForRenewAndProgress(t *testing.T) {
	ts := newServer(t)
	resp := post(t, ts.URL+"/jobs", `{"id":"fence-nonterminal","render_plan":{},"assets":[]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit: want 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim: want 200, got %d", resp.StatusCode)
	}
	var claimed model.Job
	if err := json.NewDecoder(resp.Body).Decode(&claimed); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"renew", "/jobs/fence-nonterminal/renew", `{"worker":"w1"}`},
		{"progress", "/jobs/fence-nonterminal/progress", `{"worker":"w1","data":{"frames_done":1,"total_frames":10}}`},
	} {
		resp := post(t, ts.URL+tc.path, tc.body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s without lease_generation: want 400, got %d", tc.name, resp.StatusCode)
		}
		resp.Body.Close()
	}

	renewBody := fmt.Sprintf(`{"worker":"w1","lease_generation":%d}`, claimed.LeaseGeneration)
	resp = post(t, ts.URL+"/jobs/fence-nonterminal/renew", renewBody)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("renew with generation: want 204, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	progressBody := fmt.Sprintf(`{"worker":"w1","lease_generation":%d,"data":{"frames_done":1,"total_frames":10}}`, claimed.LeaseGeneration)
	resp = post(t, ts.URL+"/jobs/fence-nonterminal/progress", progressBody)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("progress with generation: want 204, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
