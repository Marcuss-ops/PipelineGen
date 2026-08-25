package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
)

// Compile-time pins (godlike/06 SSOT discipline): the stubs
// MUST satisfy the canonical interfaces. Drift in the Broker
// 9-method surface or the AssetTransferService 4-method
// surface would otherwise surface as a method-shape panic only
// at first invocation. Looking through examples like
// `var _ drive.Admin = (*Uploader)(nil)` in
// internal/infrastructure/drive/uploader.go: the discipline is
// used widely to surface contract drift at build-failure rather
// than runtime.
var _ Broker = (*stubBroker)(nil)
var _ AssetTransferService = (*stubAssetTransfer)(nil)

// stubBroker is a focused stub for the new CompleteWithArtifacts
// surface. ALL OTHER Broker methods fail-closed via errors.New —
// any regression that accidentally routes a non-CWA call into this
// stub (or vice versa) surfaces immediately in tests rather than
// silently delegating to a non-existent implementation.
type stubBroker struct {
	called         bool
	lastCmd        CompleteWithArtifactsCommand
	returnErr      error
	returnAssetIDs []string
}

func (s *stubBroker) RegisterWorker(_ context.Context, _ RegisterWorkerCommand) (*WorkerSession, error) {
	return nil, errors.New("stubBroker: RegisterWorker not implemented")
}
func (s *stubBroker) Heartbeat(_ context.Context, _ HeartbeatCommand) error {
	return errors.New("stubBroker: Heartbeat not implemented")
}
func (s *stubBroker) Claim(_ context.Context, _ ClaimCommand) (*Lease, error) {
	return nil, errors.New("stubBroker: Claim not implemented")
}
func (s *stubBroker) Renew(_ context.Context, _ RenewCommand) (*Lease, error) {
	return nil, errors.New("stubBroker: Renew not implemented")
}
func (s *stubBroker) Progress(_ context.Context, _ ProgressCommand) error {
	return errors.New("stubBroker: Progress not implemented")
}
func (s *stubBroker) Complete(_ context.Context, _ CompleteCommand) error {
	return errors.New("stubBroker: Complete not implemented")
}
func (s *stubBroker) CompleteWithArtifacts(_ context.Context, cmd CompleteWithArtifactsCommand) ([]string, error) {
	s.called = true
	s.lastCmd = cmd
	return s.returnAssetIDs, s.returnErr
}
func (s *stubBroker) Fail(_ context.Context, _ FailCommand) error {
	return errors.New("stubBroker: Fail not implemented")
}
func (s *stubBroker) IsCancelled(_ context.Context, _, _ string) (bool, error) {
	return false, errors.New("stubBroker: IsCancelled not implemented")
}

// stubAssetTransfer is a non-nil AssetTransferService that errors
// out. The new CompleteWithArtifacts route does NOT call into
// the asset transfer surface (the artifacts have already been
// uploaded over /worker-assets/* — this endpoint only references
// them by ID), but a non-nil service keeps the handler's
// nil-tolerance consistent with the canonical pattern.
type stubAssetTransfer struct{}

func (s *stubAssetTransfer) Download(_ context.Context, _ string) (io.ReadCloser, string, error) {
	return nil, "", errors.New("stubAssetTransfer: Download not implemented")
}
func (s *stubAssetTransfer) InitiateUpload(_ context.Context, _ string) (*UploadResponse, error) {
	return nil, errors.New("stubAssetTransfer: InitiateUpload not implemented")
}
func (s *stubAssetTransfer) Upload(_ context.Context, _, _ string, _ io.Reader) error {
	return errors.New("stubAssetTransfer: Upload not implemented")
}
func (s *stubAssetTransfer) FinalizeUpload(_ context.Context, _ string) error {
	return errors.New("stubAssetTransfer: FinalizeUpload not implemented")
}

// newTestEngine wires a WorkersBrokerHandler on a fresh gin
// engine under /internal/v1 (the canonical internal prefix).
// Mirrors the production wire shape but with stubs.
func newTestEngine(b Broker, a AssetTransferService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	h := NewWorkersBrokerHandler(b, a, zap.NewNop())
	group := engine.Group("/internal/v1")
	h.RegisterRoutes(group)
	return engine
}

// TestWorkersBrokerHandler_CompleteWithArtifacts_HappyPath pins
// the canonical wire contract: WorkID/SessionID/LeaseID/Revision
// round-trip verbatim, ArtifactManifest → PublishedArtifacts
// (byte-stable), URL :id → cmd.JobID, response JobID/Status
// shape with forward-declared empty AssetIDs.
func TestWorkersBrokerHandler_CompleteWithArtifacts_HappyPath(t *testing.T) {
	stub := &stubBroker{}
	engine := newTestEngine(stub, &stubAssetTransfer{})

	// P0-COMPL-5-WIRE-NAMING: the wire body now ships a typed
	// StagedArtifacts slice (3-field minimum: ArtifactID + Destination +
	// optional SHA256 hint) instead of an opaque ArtifactManifest
	// (json.RawMessage). The handler marshals it back to JSON bytes
	// (cmd.StagedArtifacts is still json.RawMessage for worker-side
	// byte-stability with the legacy finalizer pipeline).
	stagedRefs := []*remote.StagedArtifactReference{
		{ArtifactID: "art-1", Destination: "image", SHA256: "sha-art-1"},
		{ArtifactID: "art-2", Destination: "image", SHA256: "sha-art-2"},
	}
	wantStagedBytes, marshalErr := json.Marshal(stagedRefs)
	if marshalErr != nil {
		t.Fatalf("marshal stagedRefs: %v", marshalErr)
	}
	body := CompleteArtifactsRequest{
		WorkerID:         "worker-1",
		WorkerSessionID:  "session-1",
		LeaseID:          "lease-1",
		ExpectedRevision: 7,
		ResultData:       json.RawMessage(`{"hello":"world"}`),
		StagedArtifacts:  stagedRefs,
		OutboxEvents:     json.RawMessage(`[]`),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/jobs/job-42/complete-with-artifacts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d body=%s", w.Code, w.Body.String())
	}

	var resp CompleteArtifactsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, w.Body.String())
	}
	if string(resp.JobID) != "job-42" {
		t.Errorf("expected job_id=job-42 (from URL param), got %q", resp.JobID)
	}
	if resp.Status != "SUCCEEDED" {
		t.Errorf("expected status=SUCCEEDED, got %q", resp.Status)
	}
	if resp.AssetIDs == nil {
		t.Error("expected non-nil AssetIDs slice (forward-declared wire shape)")
	}
	if len(resp.AssetIDs) != 0 {
		t.Errorf("expected empty AssetIDs (stub returns nil slice), got %v", resp.AssetIDs)
	}

	if !stub.called {
		t.Fatal("broker.CompleteWithArtifacts should have been called")
	}
	if stub.lastCmd.JobID != "job-42" {
		t.Errorf("cmd.JobID should mirror URL :id; got %q want %q", stub.lastCmd.JobID, "job-42")
	}
	if stub.lastCmd.WorkerID != body.WorkerID {
		t.Errorf("WorkerID: got %q want %q", stub.lastCmd.WorkerID, body.WorkerID)
	}
	if stub.lastCmd.WorkerSessionID != body.WorkerSessionID {
		t.Errorf("WorkerSessionID: got %q want %q", stub.lastCmd.WorkerSessionID, body.WorkerSessionID)
	}
	if stub.lastCmd.LeaseID != body.LeaseID {
		t.Errorf("LeaseID: got %q want %q", stub.lastCmd.LeaseID, body.LeaseID)
	}
	if stub.lastCmd.ExpectedRevision != body.ExpectedRevision {
		t.Errorf("ExpectedRevision: got %d want %d", stub.lastCmd.ExpectedRevision, body.ExpectedRevision)
	}
	if !bytes.Equal(stub.lastCmd.StagedArtifacts, wantStagedBytes) {
		t.Errorf("StagedArtifacts byte-mismatch:\n got: %s\nwant: %s", stub.lastCmd.StagedArtifacts, wantStagedBytes)
	}
	if !bytes.Equal(stub.lastCmd.OutboxEvents, body.OutboxEvents) {
		t.Errorf("OutboxEvents byte-mismatch:\n got: %s\nwant: %s", stub.lastCmd.OutboxEvents, body.OutboxEvents)
	}
	if !bytes.Equal(stub.lastCmd.ResultData, body.ResultData) {
		t.Errorf("ResultData byte-mismatch:\n got: %s\nwant: %s", stub.lastCmd.ResultData, body.ResultData)
	}
}

// TestWorkersBrokerHandler_CompleteWithArtifacts_BadJSON pins
// the 400 BadRequest branch and verifies the broker is NOT
// invoked on malformed body (the typed contract is enforced at
// the transport boundary, not absorbed by silent delegation).
func TestWorkersBrokerHandler_CompleteWithArtifacts_BadJSON(t *testing.T) {
	stub := &stubBroker{}
	engine := newTestEngine(stub, &stubAssetTransfer{})

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/jobs/job-42/complete-with-artifacts", bytes.NewReader([]byte("not a json{")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 BadRequest, got %d", w.Code)
	}
	if stub.called {
		t.Error("broker must NOT be invoked on malformed JSON body")
	}
}

// TestWorkersBrokerHandler_CompleteWithArtifacts_BrokerErr pins
// the 500 path: when the broker (finalizer spine) returns an
// error, the handler surfaces it via apiutil.InternalError. The
// canonical ErrFinalizerNotConfigured raises here in production.
func TestWorkersBrokerHandler_CompleteWithArtifacts_BrokerErr(t *testing.T) {
	stub := &stubBroker{returnErr: errors.New("finalizer not wired — JobFinalizer required")}
	engine := newTestEngine(stub, &stubAssetTransfer{})

	body := CompleteArtifactsRequest{
		WorkerID:         "worker-1",
		WorkerSessionID:  "session-1",
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		ResultData:       json.RawMessage(`{}`),
		StagedArtifacts:  []*remote.StagedArtifactReference{},
		OutboxEvents:     nil,
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/jobs/job-42/complete-with-artifacts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on broker error, got %d body=%s", w.Code, w.Body.String())
	}
	if !stub.called {
		t.Error("broker must be invoked once before error surfaces")
	}
	if stub.lastCmd.JobID != "job-42" {
		t.Errorf("JobID: %q", stub.lastCmd.JobID)
	}
}

// TestWorkersBrokerHandler_CompleteWithArtifacts_URLIsCanonicalJobIDSource
// pins the canonical rule that the canonical JobID in the URL (:id)
// is the SOLE source of JobID in the canonical command.
//
// IMPORTANT — STRUCTURAL GUARANTEE (not a handler override): the
// typed CompleteArtifactsRequest struct deliberately has NO
// `JobID` field. Gin's JSON decoder silently drops unknown body
// fields, so a stray `"job_id"` field in the request body cannot
// leak into the command. The handler then sets
// `cmd.JobID = c.Param("id")` unconditionally — canonical URL
// value wins by construction.
//
// REGRESSION GUARD: if a future PR adds a `JobID string` field to
// the typed DTO (e.g. for parity with the worker's outer envelope),
// this test must be updated to verify the handler STILL prefers
// the URL value over the body field. Otherwise a worker-side
// regression could leak a stale tenant_job_id into the broker
// call and silently bypass the lease-CAS check.
func TestWorkersBrokerHandler_CompleteWithArtifacts_URLIsCanonicalJobIDSource(t *testing.T) {
	stub := &stubBroker{}
	engine := newTestEngine(stub, &stubAssetTransfer{})

	// Body deliberately contains a stray `job_id` field that
	// the typed DTO does NOT have — gin will silently drop it.
	// P0-COMPL-5-WIRE-NAMING: wire key `staged_artifacts` (typed
	// StagedArtifactReference slice), NOT `artifact_manifest` (opaque
	// json.RawMessage).
	payload := []byte(`{
		"worker_id": "w",
		"worker_session_id": "s",
		"job_id": "STRAY-FROM-BODY",
		"lease_id": "l",
		"expected_revision": 3,
		"result_data": {},
		"staged_artifacts": []
	}`)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/jobs/CANONICAL-FROM-URL/complete-with-artifacts", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d body=%s", w.Code, w.Body.String())
	}
	if stub.lastCmd.JobID != "CANONICAL-FROM-URL" {
		t.Fatalf("URL :id must be canonical JobID source; got %q want %q", stub.lastCmd.JobID, "CANONICAL-FROM-URL")
	}
}

// ── AZIONE 5 (July 2026): AssetIDs population through stub broker ────
//
// 6 TDD tests that pin the canonical contract: broker.CompleteWithArtifacts
// returns ([]string, error), and the handler threads those AssetIDs into
// the CompleteArtifactsResponse wire field.

// TestCompleteArtifactsResponse_AssetIDs_PopulatedFromBroker pins the
// canonical happy-path: the stub broker returns asset IDs, and the handler
// propagates them into the JSON response under "asset_ids".
func TestCompleteArtifactsResponse_AssetIDs_PopulatedFromBroker(t *testing.T) {
	stub := &stubBroker{returnAssetIDs: []string{"a1", "a2", "a3"}}
	engine := newTestEngine(stub, &stubAssetTransfer{})

	body := CompleteArtifactsRequest{
		WorkerID:         "w",
		WorkerSessionID:  "s",
		LeaseID:          "l",
		ExpectedRevision: 1,
		ResultData:       json.RawMessage(`{}`),
		StagedArtifacts:  []*remote.StagedArtifactReference{},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/jobs/j-1/complete-with-artifacts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp CompleteArtifactsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.AssetIDs) != 3 {
		t.Errorf("expected 3 AssetIDs, got %d: %v", len(resp.AssetIDs), resp.AssetIDs)
	}
	if resp.AssetIDs[0] != "a1" || resp.AssetIDs[1] != "a2" || resp.AssetIDs[2] != "a3" {
		t.Errorf("AssetIDs mismatch: got %v, want [a1 a2 a3]", resp.AssetIDs)
	}
}

// TestCompleteArtifactsResponse_AssetIDs_EmptySlice pins the zero-result
// contract: when the broker returns no artifacts (nil or empty slice),
// the handler propagates that faithfully.
func TestCompleteArtifactsResponse_AssetIDs_EmptySlice(t *testing.T) {
	stub := &stubBroker{returnAssetIDs: []string{}}
	engine := newTestEngine(stub, &stubAssetTransfer{})

	body := CompleteArtifactsRequest{
		WorkerID:         "w",
		WorkerSessionID:  "s",
		LeaseID:          "l",
		ExpectedRevision: 1,
		ResultData:       json.RawMessage(`{}`),
		StagedArtifacts:  []*remote.StagedArtifactReference{},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/jobs/j-1/complete-with-artifacts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp CompleteArtifactsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.AssetIDs == nil {
		t.Error("AssetIDs must be non-nil (empty slice, not nil)")
	}
	if len(resp.AssetIDs) != 0 {
		t.Errorf("expected 0 AssetIDs, got %d: %v", len(resp.AssetIDs), resp.AssetIDs)
	}
}

// TestCompleteArtifactsResponse_AssetIDs_NilBrokerReturn pins the nil-safe
// contract: broker returns (nil, nil) → handler sends empty slice.
func TestCompleteArtifactsResponse_AssetIDs_NilBrokerReturn(t *testing.T) {
	stub := &stubBroker{returnAssetIDs: nil}
	engine := newTestEngine(stub, &stubAssetTransfer{})

	body := CompleteArtifactsRequest{
		WorkerID:         "w",
		WorkerSessionID:  "s",
		LeaseID:          "l",
		ExpectedRevision: 1,
		ResultData:       json.RawMessage(`{}`),
		StagedArtifacts:  []*remote.StagedArtifactReference{},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/jobs/j-1/complete-with-artifacts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp CompleteArtifactsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.AssetIDs == nil {
		t.Error("AssetIDs must be non-nil even when broker returns nil slice")
	}
}

// TestCompleteArtifactsResponse_AssetIDs_PreservedAcrossBrokerErr pins
// the error-propagated contract: when broker returns an error, the handler
// must NOT include AssetIDs in a 200 response (the error path takes over).
func TestCompleteArtifactsResponse_AssetIDs_PreservedAcrossBrokerErr(t *testing.T) {
	stub := &stubBroker{returnErr: errors.New("finalizer not wired")}
	engine := newTestEngine(stub, &stubAssetTransfer{})

	body := CompleteArtifactsRequest{
		WorkerID:         "w",
		WorkerSessionID:  "s",
		LeaseID:          "l",
		ExpectedRevision: 1,
		ResultData:       json.RawMessage(`{}`),
		StagedArtifacts:  []*remote.StagedArtifactReference{},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/jobs/j-99/complete-with-artifacts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCompleteArtifactsResponse_AssetIDs_ByteStableJSON pins the wire
// shape contract: the response JSON round-trips cleanly with the typed
// CompleteArtifactsResponse struct.
func TestCompleteArtifactsResponse_AssetIDs_ByteStableJSON(t *testing.T) {
	stub := &stubBroker{returnAssetIDs: []string{"asset-x", "asset-y"}}
	engine := newTestEngine(stub, &stubAssetTransfer{})

	body := CompleteArtifactsRequest{
		WorkerID:         "w",
		WorkerSessionID:  "s",
		LeaseID:          "l",
		ExpectedRevision: 1,
		ResultData:       json.RawMessage(`{}`),
		StagedArtifacts:  []*remote.StagedArtifactReference{},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/jobs/j-1/complete-with-artifacts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Round-trip: decode, then re-marshal, then decode again.
	var resp1 CompleteArtifactsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("unmarshal round 1: %v", err)
	}
	remarshalled, err := json.Marshal(resp1)
	if err != nil {
		t.Fatalf("marshal round 2: %v", err)
	}
	var resp2 CompleteArtifactsResponse
	if err := json.Unmarshal(remarshalled, &resp2); err != nil {
		t.Fatalf("unmarshal round 2: %v", err)
	}

	if resp2.JobID != resp1.JobID || resp2.Status != resp1.Status {
		t.Errorf("identity fields drift on round-trip: R1=%+v R2=%+v", resp1, resp2)
	}
	if len(resp2.AssetIDs) != 2 || resp2.AssetIDs[0] != "asset-x" || resp2.AssetIDs[1] != "asset-y" {
		t.Errorf("AssetIDs drift on round-trip: R1=%v R2=%v", resp1.AssetIDs, resp2.AssetIDs)
	}
}

// TestCompleteArtifactsResponse_AssetIDs_SingleElement pins the singleton
// contract (common edge case: one artifact per job).
func TestCompleteArtifactsResponse_AssetIDs_SingleElement(t *testing.T) {
	stub := &stubBroker{returnAssetIDs: []string{"solo-artifact"}}
	engine := newTestEngine(stub, &stubAssetTransfer{})

	body := CompleteArtifactsRequest{
		WorkerID:         "w",
		WorkerSessionID:  "s",
		LeaseID:          "l",
		ExpectedRevision: 1,
		ResultData:       json.RawMessage(`{}`),
		StagedArtifacts:  []*remote.StagedArtifactReference{},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/jobs/j-1/complete-with-artifacts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp CompleteArtifactsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.AssetIDs) != 1 || resp.AssetIDs[0] != "solo-artifact" {
		t.Errorf("expected [solo-artifact], got %v", resp.AssetIDs)
	}
}
