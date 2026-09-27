package remotejob

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
