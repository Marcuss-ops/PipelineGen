package jobbrokerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Compile-time pin (godlike/06 SSOT discipline): the test
// harness must keep the typed-cmd shape byte-stable with the
// production appjobs.CompleteWithArtifactsCommand. Drift in the
// command fields surfaces at build failure, not at runtime.
var _ appjobs.CompleteWithArtifactsCommand = testCmd

// testCmd is the canonical typed-cmd literal used across the
// 2 tests. Centralised so the JSON wire-shape is fixed and any
// future field addition is reflected in BOTH Tests simultaneously.
var testCmd = appjobs.CompleteWithArtifactsCommand{
	WorkerID:         "worker-1",
	WorkerSessionID:  "session-1",
	JobID:            "job-42",
	LeaseID:          "lease-1",
	ExpectedRevision: 7,
	ResultData:       json.RawMessage(`{"hello":"world"}`),
	StagedArtifacts:  json.RawMessage(`[{"artifact_id":"art-1","kind":"image/png"}]`),
	OutboxEvents:     json.RawMessage(`[]`),
}

// happyResponse is the canonical 200 OK body shape that the
// real server-side handler at
// internal/api/jobs/handler_workers.go::CompleteWithArtifacts
// emits (CompleteArtifactsResponse). Mirrored here so the test
// exercises the byte-routed decoding path in the real wire.
type happyResponse struct {
	JobID    string   `json:"job_id"`
	Status   string   `json:"status"`
	AssetIDs []string `json:"asset_ids"`
}

// TestClient_CompleteWithArtifacts_HappyPath pins the canonical
// wire contract for the artifact-completion round trip:
//
//  1. Real Client marshals cmd → JSON body
//  2. Real Client POSTs to /internal/v1/jobs/<cmd.JobID>/complete-with-artifacts
//  3. Server returns 200 + typed happyResponse shape
//  4. Client returns nil
//
// Assertions verify: (a) no error returned; (b) request path
// matches the cmd.JobID via the path-format constant; (c) request
// body byte-stably matches json.Marshal(cmd).
func TestClient_CompleteWithArtifacts_HappyPath(t *testing.T) {
	var (
		gotPath string
		gotBody []byte
		gotCT   string
		gotAuth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = b

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(happyResponse{
			JobID:    "job-42",
			Status:   "SUCCEEDED",
			AssetIDs: []string{"asset-1", "asset-2"},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "test-token")
	assetIDs, err := c.CompleteWithArtifacts(context.Background(), testCmd)
	if err != nil {
		t.Fatalf("expected nil err on happy-path, got %v", err)
	}

	// AZIONE 5 (July 2026): assert AssetIDs are decoded from response.
	if len(assetIDs) != 2 {
		t.Errorf("expected 2 AssetIDs from happy-path response, got %d: %v", len(assetIDs), assetIDs)
	}
	if assetIDs[0] != "asset-1" || assetIDs[1] != "asset-2" {
		t.Errorf("AssetIDs mismatch: got %v, want [asset-1 asset-2]", assetIDs)
	}

	// Verify path / wire-shape.
	wantSuffix := "/jobs/job-42/complete-with-artifacts"
	if !strings.HasSuffix(gotPath, wantSuffix) {
		t.Errorf("path: got %q, want suffix %q", gotPath, wantSuffix)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", gotCT)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization: got %q, want Bearer test-token", gotAuth)
	}

	// Verify body byte-stability via json.Marshal of the canonical
	// typed command. Round-trip both sides through canonical
	// decoding to defeat map-ordering nondeterminism.
	var gotDecoded, wantDecoded map[string]json.RawMessage
	if err := json.Unmarshal(gotBody, &gotDecoded); err != nil {
		t.Fatalf("unmarshal got body: %v\nbody=%s", err, gotBody)
	}
	wantBytes, _ := json.Marshal(testCmd)
	if err := json.Unmarshal(wantBytes, &wantDecoded); err != nil {
		t.Fatalf("unmarshal want body: %v", err)
	}

	for _, field := range []string{
		"worker_id", "worker_session_id", "job_id", "lease_id",
		"expected_revision", "result_data", "staged_artifacts",
		"outbox_events",
	} {
		if !bytes.Equal(gotDecoded[field], wantDecoded[field]) {
			t.Errorf("body field %s byte-mismatch:\n got: %s\nwant: %s",
				field, gotDecoded[field], wantDecoded[field])
		}
	}
}

// TestClient_CompleteWithArtifacts_LeaseMismatchTypedError pins
// the godlike/07 typed-error contract: when the server-side handler
// returns the typed-error envelope {kind:"lease_lost",...}, the
// client wraps the sentinel via fmt.Errorf(...: %w, ErrLeaseLost)
// so upstream callers use errors.Is(err, jobs.ErrLeaseLost) over
// both in-process (*local.Broker) and remote (this Client) worker
// executions.
//
// Forward-pointer: the server-side handler at
// internal/api/jobs/handler_workers.go::CompleteWithArtifacts
// currently emits a generic 500; the typed-error envelope emission
// lands in a follow-up PR. This client-side decode is forward-compatible
// today — the test verifies the canonical decoding + wrap path
// against an httptest server that emits the envelope shape.
func TestClient_CompleteWithArtifacts_LeaseMismatchTypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"kind":  "lease_lost",
			"error": "job job-42 lease mismatch: current revision stale",
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.CompleteWithArtifacts(context.Background(), testCmd)
	if err == nil {
		t.Fatal("expected non-nil err when server emits lease_lost envelope")
	}
	if !errors.Is(err, jobs.ErrLeaseLost) {
		t.Errorf("expected errors.Is(err, jobs.ErrLeaseLost); got %v (chain not wrapping sentinel)", err)
	}
	// Stray body should ALSO bubble up via the wrap-chain (godlike/07
	// no-fake-availability: the canonical error message carries
	// the upstream detail so operators can read the log directly).
	if msg := err.Error(); !strings.Contains(msg, "lease mismatch") && !strings.Contains(msg, "lease_lost") {
		t.Errorf("expected err message carries upstream detail; got %q", msg)
	}
}
