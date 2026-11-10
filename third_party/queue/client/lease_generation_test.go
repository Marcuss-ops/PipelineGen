package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompleteSendsGeneration(t *testing.T) {
	var got struct {
		Worker          string   `json:"worker"`
		LeaseGeneration int64    `json:"lease_generation"`
		Data            Artifact `json:"data"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/job-1/complete" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := New(srv.URL).Complete(context.Background(), "job-1", "w1", 42, Artifact{ID: "art-1"}); err != nil {
		t.Fatal(err)
	}
	if got.Worker != "w1" || got.LeaseGeneration != 42 || got.Data.ID != "art-1" {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestReportProgressSendsGeneration(t *testing.T) {
	var got struct {
		Worker          string   `json:"worker"`
		LeaseGeneration int64    `json:"lease_generation"`
		Data            Progress `json:"data"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/job-1/progress" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := New(srv.URL).ReportProgress(context.Background(), "job-1", "w1", 42, Progress{FramesDone: 7, TotalFrames: 10}); err != nil {
		t.Fatal(err)
	}
	if got.Worker != "w1" || got.LeaseGeneration != 42 || got.Data.FramesDone != 7 || got.Data.TotalFrames != 10 {
		t.Fatalf("unexpected body: %+v", got)
	}
}
