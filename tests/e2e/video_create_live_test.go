// Package e2e — video_create_live_test.go is the LIVE certification
// harness for the durable video.create workflow (the plan's §25 first
// test and its §26 acceptance table).
//
// It is LIVE: it submits against a running PipelineGen with the real
// media plane (rustexec), RenderingGen → Chronon, Google Drive and the
// media registry. Like the other tests in this directory it does not
// run in CI: it is SKIPPED unless VELOX_LIVE_E2E=1.
//
//	env VELOX_LIVE_E2E=1 \
//	    PIPELINEGEN_BASE_URL=http://127.0.0.1:8080 \
//	    PIPELINEGEN_SUBMIT_URL=http://127.0.0.1:8080/api/jobs \
//	    VELOX_LIVE_TOKEN=<admin bearer> \
//	    go test ./tests/e2e -run TestVideoCreateLiveE2E -v -timeout 4h
//
// Submission surface (§2/§22): POST /api/jobs with type=video.create
// — deliberately NO /api/v1/video/create endpoint. The M2M surface
// intentionally rejects video.create before certification, so this harness
// submits through the authenticated admin jobs surface. The catalog remains
// disabled until the live suite passes and production assembly is wired.
//
// The fixture is the plan's exact §25 smoke payload (60 seconds — never
// start with a 15-minute run).
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// liveGate skips the harness unless explicitly enabled.
func liveGate(t *testing.T) (baseURL, submitURL, token string) {
	t.Helper()
	if os.Getenv("VELOX_LIVE_E2E") != "1" {
		t.Skip("live E2E disabled (set VELOX_LIVE_E2E=1 against a running PipelineGen)")
	}
	baseURL = envOr("PIPELINEGEN_BASE_URL", "http://127.0.0.1:8080")
	submitURL = envOr("PIPELINEGEN_SUBMIT_URL", baseURL+"/api/jobs")
	token = os.Getenv("VELOX_LIVE_TOKEN")
	return baseURL, submitURL, token
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// TestVideoCreateLiveE2E runs the §26 acceptance matrix end-to-end:
// submit → whole chain → final_video.media_url/sha256/drive_file_id →
// REPLAY with the same idempotency key converging on the SAME job and
// the SAME published video (nessun duplicato).
func TestVideoCreateLiveE2E(t *testing.T) {
	baseURL, submitURL, token := liveGate(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour+30*time.Minute)
	defer cancel()

	raw, err := os.ReadFile(filepath.Join("fixtures", "video_create_e2e_001.json"))
	if err != nil {
		t.Fatalf("read §25 fixture: %v", err)
	}

	// ── Submit (§25) ────────────────────────────────────────────────
	jobID, err := submitJob(ctx, submitURL, token, raw)
	if err != nil {
		t.Fatalf("submit video.create: %v", err)
	}
	t.Logf("video.create accepted: job_id=%s", jobID)

	// ── Poll to terminal (the 51 does exactly this) ─────────────────
	result := pollTerminal(ctx, t, baseURL, token, jobID)
	status, _ := result["status"].(string)
	if status != "SUCCEEDED" {
		t.Fatalf("video.create ended %q (error=%v) — want SUCCEEDED ONLY at the end (§17/§26)", status, result["error"])
	}

	// ── §26 result assertions ───────────────────────────────────────
	jobResult := requireMap(t, result, "result")
	finalVideo := requireMap(t, jobResult, "final_video")
	for _, field := range []string{"asset_id", "media_url", "drive_file_id", "sha256"} {
		if s, _ := finalVideo[field].(string); s == "" {
			t.Errorf("final_video.%s is empty: %v", field, finalVideo)
		}
	}
	for _, field := range []string{"size_bytes", "duration_ms"} {
		if n := numeric(finalVideo[field]); n <= 0 {
			t.Errorf("final_video.%s = %v, want > 0", field, finalVideo[field])
		}
	}
	children := requireMap(t, jobResult, "children")
	if len(children) == 0 {
		t.Errorf("children ledger is empty: %v", result)
	}
	if thumb := jobResult["thumbnail_context"]; thumb == nil {
		t.Errorf("thumbnail_context missing (§19-B contract): %v", jobResult)
	}
	t.Logf("final_video.media_url = %v", finalVideo["media_url"])

	// ── Replay (§26: nessun duplicato) ──────────────────────────────
	replayID, err := submitJob(ctx, submitURL, token, raw)
	if err != nil {
		t.Fatalf("replay submit: %v", err)
	}
	if replayID != jobID {
		t.Fatalf("replay created a SECOND job %q (was %q) — idempotency_key dedup broken", replayID, jobID)
	}
	replay := pollTerminal(ctx, t, baseURL, token, jobID)
	replayResult := requireMap(t, replay, "result")
	replayVideo := requireMap(t, replayResult, "final_video")
	if replayVideo["media_url"] != finalVideo["media_url"] || replayVideo["sha256"] != finalVideo["sha256"] {
		t.Errorf("replay diverged: %v vs %v", finalVideo, replayVideo)
	}
	t.Log("replay converged on the same job and the same published video")
}

// TestVideoCreateLiveRestart documents + automates the §26 "Restart →
// workflow riprende" leg: it re-submits the SAME job id (redelivery)
// after an operator restarts the worker mid-run and asserts convergence.
// It runs only when the operator sets VELOX_LIVE_RESTART_MANUAL=1 and
// restarts the worker during the poll window below.
func TestVideoCreateLiveRestart(t *testing.T) {
	baseURL, submitURL, token := liveGate(t)
	if os.Getenv("VELOX_LIVE_RESTART_MANUAL") != "1" {
		t.Skip("manual restart leg (set VELOX_LIVE_RESTART_MANUAL=1 and restart the worker during the poll)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour+30*time.Minute)
	defer cancel()
	raw, err := os.ReadFile(filepath.Join("fixtures", "video_create_e2e_001.json"))
	if err != nil {
		t.Fatalf("read §25 fixture: %v", err)
	}
	jobID, err := submitJob(ctx, submitURL, token, raw)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	t.Logf("RESTART THE WORKER NOW (job %s is mid-run); polling continues…", jobID)
	result := pollTerminal(ctx, t, baseURL, token, jobID)
	if status, _ := result["status"].(string); status != "SUCCEEDED" {
		t.Fatalf("after restart the workflow ended %q — resume broken", status)
	}
	jobResult := requireMap(t, result, "result")
	finalVideo := requireMap(t, jobResult, "final_video")
	if finalVideo["media_url"] == "" {
		t.Fatalf("after restart no published final_video: %v", jobResult)
	}
}

// submitJob POSTs the §25 envelope and returns the broker job id.
func submitJob(ctx context.Context, submitURL, token string, envelope []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, submitURL, bytes.NewReader(envelope))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode submit response (%d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("submit HTTP %d: %v", resp.StatusCode, body)
	}
	id, _ := body["job_id"].(string)
	if id == "" {
		if nested, ok := body["job"].(map[string]any); ok {
			id, _ = nested["id"].(string)
		}
	}
	if id == "" {
		return "", fmt.Errorf("submit response has no job_id: %v", body)
	}
	return id, nil
}

// pollTerminal polls /api/jobs/{id}/full until the broker reports a
// terminal state (the documented polling surface).
func pollTerminal(ctx context.Context, t *testing.T, baseURL, token, jobID string) map[string]any {
	t.Helper()
	url := fmt.Sprintf("%s/api/jobs/%s/full", baseURL, jobID)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("poll request: %v", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("poll %s: %v", url, err)
		}
		var body map[string]any
		decodeErr := json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if decodeErr != nil {
			t.Fatalf("poll decode: %v", decodeErr)
		}
		status, _ := body["status"].(string)
		switch status {
		case "SUCCEEDED", "FAILED", "CANCELLED", "PARTIALLY_SUCCEEDED":
			return body
		}
		select {
		case <-ctx.Done():
			t.Fatalf("poll timed out in status %q: %v", status, body)
		case <-ticker.C:
		}
	}
}

func requireMap(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	m, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("result has no %q object: %v", key, parent)
	}
	return m
}

func numeric(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}
