package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClaimDecodesJobAndLease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/claim" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["worker"] != "w1" {
			t.Errorf("worker = %q", body["worker"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"job-1","schema":"renderinggen.job","version":1,"render_plan":{"o":1},"assets":[{"hash":"abc","logical_path":"videos/base.mp4"}],"lease":60000000000}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	job, err := c.Claim(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.ID != "job-1" || job.Lease != 60*time.Second {
		t.Fatalf("unexpected job: %+v", job)
	}
	if len(job.Assets) != 1 || job.Assets[0].Hash != "abc" || job.Assets[0].LogicalPath != "videos/base.mp4" {
		t.Fatalf("assets: %+v", job.Assets)
	}
	if job.Schema != "renderinggen.job" || job.Version != 1 {
		t.Fatalf("envelope: schema=%q version=%d", job.Schema, job.Version)
	}
}

func TestClaimEmptyReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	job, err := c.Claim(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if job != nil {
		t.Fatalf("want nil job, got %+v", job)
	}
}

func TestCompleteSendsWorkerAndArtifact(t *testing.T) {
	var got struct {
		Worker string   `json:"worker"`
		Data   Artifact `json:"data"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/job-1/complete" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	err := c.Complete(context.Background(), "job-1", "w1", Artifact{
		ID: "art-1", ProfileID: CertifiedProfileVeloxH2641080p30V1, CopyEligible: true, ClosedGOP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Worker != "w1" || got.Data.ID != "art-1" || !got.Data.CopyEligible || !got.Data.ClosedGOP {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestFailSendsReason(t *testing.T) {
	var got struct {
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/job-1/fail" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Fail(context.Background(), "job-1", "w1", "boom"); err != nil {
		t.Fatal(err)
	}
	if got.Data.Reason != "boom" {
		t.Fatalf("reason = %q", got.Data.Reason)
	}
}

func TestFailPermanentlyUsesTerminalEndpoint(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/job-1/fail-permanent" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s, want permanent-fail endpoint", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := New(srv.URL).FailPermanently(context.Background(), "job-1", "w1", "Drive unavailable"); err != nil {
		t.Fatal(err)
	}
	data, ok := got["data"].(map[string]any)
	if !ok || data["reason"] != "Drive unavailable" {
		t.Fatalf("body = %v", got)
	}
}

func TestRenewSendsWorker(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/job-1/renew" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["worker"] != "w1" {
			t.Errorf("worker = %q", body["worker"])
		}
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Renew(context.Background(), "job-1", "w1"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("want 1 renew call, got %d", calls)
	}
}

func TestWorkerRegistrationAndHeartbeat(t *testing.T) {
	var registered Worker
	var heartbeatWorker string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/workers/register":
			if r.Method != http.MethodPost {
				t.Errorf("register method = %s", r.Method)
			}
			_ = json.NewDecoder(r.Body).Decode(&registered)
			w.WriteHeader(http.StatusNoContent)
		case "/workers/heartbeat":
			if r.Method != http.MethodPost {
				t.Errorf("heartbeat method = %s", r.Method)
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			heartbeatWorker = body["worker"]
			w.WriteHeader(http.StatusNoContent)
		case "/workers":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]Worker{registered})
		case "/workers/health":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(WorkerHealth{Ready: 1, Total: 1})
		case "/jobs/job-1/retry":
			if r.Method != http.MethodPost {
				t.Errorf("retry method = %s", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL)
	ctx := context.Background()
	err := c.RegisterWorker(ctx, Worker{
		ID:         "w-gpu-1",
		Status:     WorkerStatusReady,
		GPUBackend: "vulkan",
	})
	if err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}
	if registered.ID != "w-gpu-1" || registered.GPUBackend != "vulkan" {
		t.Fatalf("unexpected registered worker: %+v", registered)
	}

	if err := c.HeartbeatWorker(ctx, "w-gpu-1"); err != nil {
		t.Fatalf("HeartbeatWorker: %v", err)
	}
	if heartbeatWorker != "w-gpu-1" {
		t.Fatalf("unexpected heartbeat worker: %s", heartbeatWorker)
	}

	workers, err := c.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(workers) != 1 || workers[0].ID != "w-gpu-1" {
		t.Fatalf("unexpected workers list: %+v", workers)
	}

	health, err := c.WorkerHealth(ctx)
	if err != nil {
		t.Fatalf("WorkerHealth: %v", err)
	}
	if health.Ready != 1 || health.Total != 1 {
		t.Fatalf("unexpected health: %+v", health)
	}

	if err := c.Retry(ctx, "job-1"); err != nil {
		t.Fatalf("Retry: %v", err)
	}
}
