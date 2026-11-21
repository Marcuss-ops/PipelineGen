package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The batch claim is the client half of RENDER_BATCH: one request asks for a
// window, the envelope carries the jobs, and an empty queue is a 204 with no
// jobs rather than an error. Both halves are pinned here because a worker that
// cannot tell "empty" from "failed" would spin.
func TestClaimBatchSendsLimitAndDecodesWindow(t *testing.T) {
	var got struct {
		Worker string `json:"worker"`
		Limit  int    `json:"limit"`
	}
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/claim/batch" {
			http.NotFound(w, r)
			return
		}
		calls++
		_ = json.NewDecoder(r.Body).Decode(&got)
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jobs": []map[string]any{
					{"id": "a", "state": "running", "lease_generation": 1},
					{"id": "b", "state": "running", "lease_generation": 1},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	client := New(ts.URL)
	jobs, err := client.ClaimBatch(context.Background(), "batch-worker", 3)
	if err != nil {
		t.Fatalf("claim batch: %v", err)
	}
	if got.Worker != "batch-worker" || got.Limit != 3 {
		t.Fatalf("request = %+v, want worker=batch-worker limit=3", got)
	}
	if len(jobs) != 2 || jobs[0].ID != "a" || jobs[1].ID != "b" {
		t.Fatalf("jobs = %+v, want a,b", jobs)
	}

	empty, err := client.ClaimBatch(context.Background(), "batch-worker", 3)
	if err != nil {
		t.Fatalf("claim batch on empty queue: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty window = %+v, want none", empty)
	}

	if _, err := client.ClaimBatch(context.Background(), "batch-worker", 0); err == nil {
		t.Fatal("limit 0 must be rejected before the request")
	}
}
