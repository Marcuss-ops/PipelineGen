package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubmitBatchRouteValidatesAndSubmitsAtomically(t *testing.T) {
	ts := newServer(t)

	if resp := post(t, ts.URL+"/jobs/batch", `{"jobs":[]}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty batch: want 400, got %d", resp.StatusCode)
	}
	if resp := post(t, ts.URL+"/jobs/batch", `not-json`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed batch: want 400, got %d", resp.StatusCode)
	}
	if resp := post(t, ts.URL+"/jobs/batch", `{"jobs":[{"id":"batch-a"},{"id":"batch-b"}]}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("valid batch: want 201, got %d", resp.StatusCode)
	}

	first := post(t, ts.URL+"/jobs/claim", `{"worker":"worker-a"}`)
	second := post(t, ts.URL+"/jobs/claim", `{"worker":"worker-b"}`)
	if first.StatusCode != http.StatusOK || second.StatusCode != http.StatusOK {
		t.Fatalf("claims after batch: status pair %d/%d, want 200/200", first.StatusCode, second.StatusCode)
	}
}

func TestSubmitBatchDuplicateIsConflictWithoutPartialInsert(t *testing.T) {
	ts := newServer(t)
	post(t, ts.URL+"/jobs", `{"id":"already-exists"}`)

	resp := post(t, ts.URL+"/jobs/batch", `{"jobs":[{"id":"batch-new"},{"id":"already-exists"}]}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("batch with duplicate: want 409, got %d", resp.StatusCode)
	}
	if next := post(t, ts.URL+"/jobs/claim", `{"worker":"worker"}`); next.StatusCode != http.StatusOK {
		t.Fatalf("existing pending job was not retained: claim status=%d", next.StatusCode)
	}
	if next := post(t, ts.URL+"/jobs/claim", `{"worker":"worker"}`); next.StatusCode != http.StatusNoContent {
		t.Fatalf("failed batch partially inserted a job: next claim status=%d", next.StatusCode)
	}
}

func TestParseJobIDSupportsSingleAndParentLanguageRoutes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, parseJobID(r))
	})
	mux.HandleFunc("GET /jobs/{parent}/{lang}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, parseJobID(r))
	})
	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/jobs/job-123", want: "job-123"},
		{path: "/jobs/master-123/it", want: "master-123/it"},
	} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != http.StatusOK || recorder.Body.String() != test.want {
			t.Errorf("parseJobID(%s) response = %d/%q, want 200/%q", test.path, recorder.Code, recorder.Body.String(), test.want)
		}
	}
}

func TestNewIDHasExpectedPrefixAndUniqueHexPayload(t *testing.T) {
	first, err := newID()
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	second, err := newID()
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	if !strings.HasPrefix(first, "job-") || len(first) != len("job-")+16 {
		t.Fatalf("generated id = %q, want job- plus 16 hex characters", first)
	}
	for _, digit := range first[len("job-"):] {
		if !strings.ContainsRune("0123456789abcdef", digit) {
			t.Fatalf("generated id contains non-hex character %q in %q", digit, first)
		}
	}
	if first == second {
		t.Fatalf("two generated ids unexpectedly collided: %q", first)
	}
}

func TestSubmitBatchEnforcesRequestSizeLimit(t *testing.T) {
	ts := newServer(t)
	body := `{"jobs":[{"id":"oversize","render_plan":{"padding":"` + strings.Repeat("x", maxSubmitBytes) + `"}}]}`
	resp := post(t, ts.URL+"/jobs/batch", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized batch: want 400, got %d", resp.StatusCode)
	}
}
