// by_parent_routes_test.go pins GET /jobs?parent_job_id=<id> — the run-scoped
// read ("what did this run enqueue?") added because the only pre-existing read
// into parent_job_id was the assembly fan-in GET /jobs/{id}/children, which
// orders by chunk index and serves a different question.
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func get(t *testing.T, rawURL string) *http.Response {
	t.Helper()
	resp, err := http.Get(rawURL)
	if err != nil {
		t.Fatalf("get %s: %v", rawURL, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// seedRunJobs submits two jobs under `run-a` and one under `run-b` so the read
// has to SELECT by parent instead of returning whatever is in the queue.
func seedRunJobs(t *testing.T, ts *httptest.Server) {
	t.Helper()
	for _, body := range []string{
		`{"id":"run-a:render:1","parent_job_id":"run-a","render_plan":{"n":1}}`,
		`{"id":"run-a:render:2","parent_job_id":"run-a","render_plan":{"n":2}}`,
		`{"id":"run-b:render:1","parent_job_id":"run-b","render_plan":{"n":3}}`,
	} {
		resp := post(t, ts.URL+"/jobs", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed submit %s: want 201, got %d", body, resp.StatusCode)
		}
	}
}

func TestListJobsByParentReturnsOnlyThatRun(t *testing.T) {
	ts := newServer(t)
	seedRunJobs(t, ts)

	resp := get(t, ts.URL+"/jobs?parent_job_id="+url.QueryEscape("run-a"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: want 200, got %d", resp.StatusCode)
	}
	var jobs []struct {
		ID          string `json:"id"`
		ParentJobID string `json:"parent_job_id"`
		State       string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("run-a returned %d job(s), want exactly its 2 renders: %+v", len(jobs), jobs)
	}
	seen := map[string]bool{}
	for _, job := range jobs {
		if job.ParentJobID != "run-a" {
			t.Errorf("job %s carries parent %q, want run-a", job.ID, job.ParentJobID)
		}
		if job.State == "" {
			t.Errorf("job %s has no state: a listing must be informative on its own", job.ID)
		}
		seen[job.ID] = true
	}
	for _, want := range []string{"run-a:render:1", "run-a:render:2"} {
		if !seen[want] {
			t.Errorf("job %s missing from the run-scoped listing", want)
		}
	}
	if seen["run-b:render:1"] {
		t.Error("a job belonging to another run must not leak into the listing")
	}
}

// TestListJobsByParentUnknownIdIsEmptyNotNotFound pins the documented contract:
// "this run enqueued nothing" is an answer (200 + empty array), not a miss. A
// 404 here would make a run whose renders were never enqueued indistinguishable
// from a typo in the run id.
func TestListJobsByParentUnknownIdIsEmptyNotNotFound(t *testing.T) {
	ts := newServer(t)
	seedRunJobs(t, ts)

	resp := get(t, ts.URL+"/jobs?parent_job_id=run-does-not-exist")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown parent: want 200, got %d", resp.StatusCode)
	}
	var jobs []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("unknown parent returned %d job(s), want none", len(jobs))
	}
}

// TestListJobsRequiresParentQueryParameter pins that the endpoint asks its one
// question explicitly instead of degenerating into an unbounded "list every
// job" API.
func TestListJobsRequiresParentQueryParameter(t *testing.T) {
	ts := newServer(t)
	resp := get(t, ts.URL+"/jobs")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing parent_job_id: want 400, got %d", resp.StatusCode)
	}
}
