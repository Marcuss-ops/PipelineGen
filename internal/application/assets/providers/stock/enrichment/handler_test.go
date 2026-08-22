// Package enrichment — handler_test.go (PR-011A + PR-011C, July 2026).
//
// 9 hermetic TDD tests pinning the EnrichmentHandler contract.
// The tests use ONLY the canonical stub adapters (no SQLite, no
// ollama, no real network) so they pass in any environment
// that can compile the package.
//
// Test taxonomy:
//  1. TestNewEnrichmentHandler_NilDeps_ReturnsTypedSentinel —
//     composition-time fail-closed (nil LLMClient / nil AssetRepo).
//  2. TestEnrichmentHandler_HandleJob_EmptyChunkID_ReturnsTerminalSentinel —
//     terminal sentinel on empty chunk_id (no LLM call).
//  3. TestEnrichmentHandler_HandleJob_LLMUnavailable_ReturnsRetryableSentinel —
//     retryable sentinel when the stub LLM client returns
//     ErrEnrichmentLLMUnavailable (the canonical PR-011A
//     end-to-end retry-path exercise).
//  4. TestEnrichmentHandler_HandleJob_ValidChunkID_InvokesLLMClient —
//     happy-path: the handler builds the canonical
//     EnrichmentRequest from the AssetRow and forwards to the
//     LLMClient (the canonical PR-011A contract test for the
//     handler's request-shape contract).
//  5. TestEnrichmentHandler_HandleJob_MalformedPayload_ReturnsTypedSentinel —
//     terminal sentinel on non-JSON payload (producer-side
//     schema-drift signal).
//  6. TestEnrichmentWrapHelpers_PreserveSentinelChain —
//     dual-%w Go 1.20+ chain-preservation contract.
//  7. TestEnrichmentHandler_HandleJob_HappyPath_EmitsV1Envelope (PR-011C) —
//     happy-path: handler builds canonical v1 envelope and emits
//     via the AssetPublishedEmitter port; stub captures the
//     emitted payload for hermetic TDD assertions.
//  8. TestEnrichmentHandler_HandleJob_IdempotencyKeyStable (PR-011C) —
//     idempotency-key byte-stability across multiple invocations.
//  9. TestEnrichmentHandler_HandleJob_EmitterError_ReturnsRetryableSentinel
//     (PR-011C) — emitter-side failure (e.g. SQLite locked) is
//     wrapped in ErrEnrichmentEmitFailed (retryable per
//     godlike/07 worker-pool exponential backoff).
//
// godlike/06 SSOT (one canonical owner per fact): the 9 tests
// live ONLY in this file. Future contract additions MUST extend
// this file (NOT introduce a parallel test surface).
//
// godlike/07 minimum-blast-radius: zero external dependencies
// (no real SQLite, no real ollama). The test surface is
// hermetic and idempotent — `go test -short -count=1` passes
// deterministically on any Go toolchain.
package enrichment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
)

// errorAssetPublishedEmitter is a hermetic test double that
// returns a specified error from EmitAssetPublished. Used by
// the retryable-error test to exercise the ErrEnrichmentEmitFailed
// wrap path (PR-011C).
type errorAssetPublishedEmitter struct {
	err error
}

func (e *errorAssetPublishedEmitter) EmitAssetPublished(ctx context.Context, payload outbox.AssetPublishedRequestV1) error {
	return e.err
}

// fakeAssetRepo is a hermetic test double for AssetRepository.
// Populates the row map with whatever the test wants the
// GetByID call to return; tracks UpdateEnrichedMetadata calls.
type fakeAssetRepo struct {
	// row is the canonical AssetRow returned by GetByID. When
	// nil, GetByID returns (nil, WrapChunkNotFound(id)).
	row *AssetRow

	// updateCalls tracks UpdateEnrichedMetadata invocations
	// for the happy-path test.
	updateCalls []string

	// updateErr, when non-nil, is returned from
	// UpdateEnrichedMetadata (canonical SQL-side failure
	// simulation).
	updateErr error
}

func (f *fakeAssetRepo) GetByID(ctx context.Context, id string) (*AssetRow, error) {
	if f.row == nil {
		return nil, WrapChunkNotFound(id)
	}
	return f.row, nil
}

func (f *fakeAssetRepo) UpdateEnrichedMetadata(ctx context.Context, id string, fields EnrichedFields) error {
	f.updateCalls = append(f.updateCalls, id)
	return f.updateErr
}

// fakeLLMClient is a hermetic test double for EnrichmentLLMClient.
// Override the err field to simulate specific failure modes
// (ErrEnrichmentLLMUnavailable, ErrEnrichmentInvalidLLMResponse).
type fakeLLMClient struct {
	// err is returned verbatim from Enrich (canonical
	// failure-mode simulation).
	err error

	// resp is returned when err is nil (canonical
	// happy-path response).
	resp *EnrichmentResponse

	// requests tracks the EnrichmentRequest values the
	// handler forwards to the LLMClient (canonical
	// request-shape contract test).
	requests []EnrichmentRequest
}

func (f *fakeLLMClient) Enrich(ctx context.Context, req EnrichmentRequest) (*EnrichmentResponse, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// validContentHash is the canonical 64-char lowercase hex
// content_hash used by the PR-011C emit tests. Mirrors the
// canonical wire-shape of media_assets.file_hash (lowercase
// sha256 hex of the chunk's video file).
const validContentHash = "a1b2c3d4e5f6789012345678901234567890123456789012345678901234abcd" // 64 chars

func (f *fakeLLMClient) Model() string {
	if f.resp != nil && f.resp.Model != "" {
		return f.resp.Model
	}
	return "test-model"
}

// Test 1: composition-time fail-closed on nil deps.
// PR-011C: signature is now 4-arg (llmClient, assetRepo, emitter, log);
// emitter is OPTIONAL (nil-allowed for composition-root disabled mode).
func TestNewEnrichmentHandler_NilDeps_ReturnsTypedSentinel(t *testing.T) {
	t.Run("nil LLMClient returns sentinel", func(t *testing.T) {
		repo := &fakeAssetRepo{row: &AssetRow{ID: "asset-1"}}
		h, err := NewEnrichmentHandler(nil, repo, nil, zap.NewNop())
		if h != nil {
			t.Errorf("expected nil handler, got %+v", h)
		}
		if !errors.Is(err, ErrEnrichmentHandlerNotConfigured) {
			t.Errorf("expected ErrEnrichmentHandlerNotConfigured, got %v", err)
		}
	})

	t.Run("nil AssetRepo returns sentinel", func(t *testing.T) {
		llm := &fakeLLMClient{}
		h, err := NewEnrichmentHandler(llm, nil, nil, zap.NewNop())
		if h != nil {
			t.Errorf("expected nil handler, got %+v", h)
		}
		if !errors.Is(err, ErrEnrichmentHandlerNotConfigured) {
			t.Errorf("expected ErrEnrichmentHandlerNotConfigured, got %v", err)
		}
	})

	t.Run("valid deps returns handler (nil emitter allowed)", func(t *testing.T) {
		llm := &fakeLLMClient{}
		repo := &fakeAssetRepo{row: &AssetRow{ID: "asset-1"}}
		h, err := NewEnrichmentHandler(llm, repo, nil, zap.NewNop())
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if h == nil {
			t.Fatal("expected non-nil handler")
		}
		if h.LLMClient != llm {
			t.Error("LLMClient not wired")
		}
		if h.AssetRepo != repo {
			t.Error("AssetRepo not wired")
		}
		if h.Emitter != nil {
			t.Error("Emitter should be nil (disabled-mode wiring)")
		}
		if h.Log == nil {
			t.Error("Log should be non-nil (zap.NewNop fallback)")
		}
	})

	t.Run("valid deps with stub emitter returns handler", func(t *testing.T) {
		llm := &fakeLLMClient{}
		repo := &fakeAssetRepo{row: &AssetRow{ID: "asset-1"}}
		emitter := &StubAssetPublishedEmitter{}
		h, err := NewEnrichmentHandler(llm, repo, emitter, zap.NewNop())
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if h == nil {
			t.Fatal("expected non-nil handler")
		}
		if h.Emitter != emitter {
			t.Error("Emitter not wired")
		}
	})
}

// Test 2: terminal sentinel on empty chunk_id (handler does not
// invoke the LLM client in this case).
func TestEnrichmentHandler_HandleJob_EmptyChunkID_ReturnsTerminalSentinel(t *testing.T) {
	llm := &fakeLLMClient{}
	repo := &fakeAssetRepo{row: &AssetRow{ID: "asset-1"}}
	h, err := NewEnrichmentHandler(llm, repo, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewEnrichmentHandler: %v", err)
	}

	// Job with empty payload (chunk_id not parseable).
	job := &appjobs.Job{ID: "job-1", Payload: []byte(`{}`)}

	_, err = h.HandleJob(context.Background(), job, nil)
	if !errors.Is(err, ErrEnrichmentChunkNotFound) {
		t.Errorf("expected ErrEnrichmentChunkNotFound, got %v", err)
	}

	// The LLM client must NOT be invoked when chunk_id is empty
	// (terminal short-circuit before LLM call).
	if len(llm.requests) != 0 {
		t.Errorf("LLM client should not be invoked on empty chunk_id, got %d calls", len(llm.requests))
	}
}

// Test 3: retryable sentinel when the LLM client returns
// ErrEnrichmentLLMUnavailable. The handler must propagate the
// sentinel verbatim so the worker's exponential backoff retries
// fire on the canonical typed-error.
func TestEnrichmentHandler_HandleJob_LLMUnavailable_ReturnsRetryableSentinel(t *testing.T) {
	llm := &fakeLLMClient{err: ErrEnrichmentLLMUnavailable}
	repo := &fakeAssetRepo{row: &AssetRow{ID: "asset-1"}}
	h, err := NewEnrichmentHandler(llm, repo, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewEnrichmentHandler: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"chunk_id": "asset-1"})
	job := &appjobs.Job{ID: "job-1", Payload: payload}

	_, err = h.HandleJob(context.Background(), job, nil)
	if !errors.Is(err, ErrEnrichmentLLMUnavailable) {
		t.Errorf("expected ErrEnrichmentLLMUnavailable, got %v", err)
	}

	// The LLM client IS invoked once (the stub returns the
	// error on the first call).
	if len(llm.requests) != 1 {
		t.Errorf("expected 1 LLM call, got %d", len(llm.requests))
	}

	// UpdateEnrichedMetadata must NOT be called when the
	// LLM call fails (PR-011A: no DB writes on LLM error).
	if len(repo.updateCalls) != 0 {
		t.Errorf("UpdateEnrichedMetadata should not be called on LLM error, got %d", len(repo.updateCalls))
	}
}

// Test 4: happy-path contract — the handler builds the
// canonical EnrichmentRequest from the AssetRow and forwards
// to the LLMClient. The LLMClient returns a valid response.
//
// PR-011C: when the LLM call SUCCEEDS, the handler proceeds to
// the v1 emit step. The test uses a NIL emitter (disabled mode)
// so the handler skips the emit and returns a result envelope.
func TestEnrichmentHandler_HandleJob_ValidChunkID_InvokesLLMClient(t *testing.T) {
	llm := &fakeLLMClient{
		resp: &EnrichmentResponse{
			ChunkID: "asset-1",
			Fields: EnrichedFields{
				Category: "Boxe",
				Event:    "Pacquiao vs Broner",
				Round:    "9",
				Subject:  "Manny Pacquiao",
			},
			Model: "gemma4:e4b",
		},
	}
	repo := &fakeAssetRepo{row: &AssetRow{
		ID:             "asset-1",
		SourceURL:      "https://pexels.com/video/12345",
		Title:          "Pacquiao Broner Round 9",
		Description:    "Boxing match round 9",
		StartSec:       120.5,
		EndSec:         130.0,
		SourceProvider: "pexels",
		LegacyFileMD5:       validContentHash,
		DriveFileID:    "drive-file-12345",
		DrivePath:      "stock/Boxe/pexels/Manny-Pacquiao",
	}}
	h, err := NewEnrichmentHandler(llm, repo, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewEnrichmentHandler: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"chunk_id": "asset-1"})
	job := &appjobs.Job{ID: "job-1", Payload: payload}

	result, err := h.HandleJob(context.Background(), job, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The LLM client must be invoked once with the canonical
	// request shape (ChunkID + SourceURL + Title + ...
	// projected from the AssetRow).
	if len(llm.requests) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(llm.requests))
	}
	req := llm.requests[0]
	if req.ChunkID != "asset-1" {
		t.Errorf("ChunkID mismatch: got %q", req.ChunkID)
	}
	if req.SourceURL != "https://pexels.com/video/12345" {
		t.Errorf("SourceURL mismatch: got %q", req.SourceURL)
	}
	if req.Title != "Pacquiao Broner Round 9" {
		t.Errorf("Title mismatch: got %q", req.Title)
	}
	if req.SourceProvider != "pexels" {
		t.Errorf("SourceProvider mismatch: got %q", req.SourceProvider)
	}
	if req.StartSec != 120.5 {
		t.Errorf("StartSec mismatch: got %v", req.StartSec)
	}
	if req.EndSec != 130.0 {
		t.Errorf("EndSec mismatch: got %v", req.EndSec)
	}

	// Result envelope (PR-011C: stage label = "pr011c_v1_emitted"
	// describes the LLM + UPDATE + emit SEQUENCE; the actual
	// emit was skipped because emitter is nil — the Warn log
	// records that).
	if result["chunk_id"] != "asset-1" {
		t.Errorf("result.chunk_id mismatch: got %v", result["chunk_id"])
	}
	if result["model"] != "gemma4:e4b" {
		t.Errorf("result.model mismatch: got %v", result["model"])
	}
	if result["handler_stage"] != "pr011c_v1_emit_skipped_nil_emitter" {
		t.Errorf("result.handler_stage mismatch: got %v (want pr011c_v1_emit_skipped_nil_emitter)", result["handler_stage"])
	}
	if result["destination"] != "stock" {
		t.Errorf("result.destination mismatch: got %v", result["destination"])
	}
	if result["schema_version"] != outbox.AssetPublishedSchemaVersion {
		t.Errorf("result.schema_version mismatch: got %v", result["schema_version"])
	}
}

// Test 5: terminal sentinel on malformed payload (non-JSON).
// The handler must return a typed sentinel so the producer
// (PR-011C stock fan-out) can fix the enqueue call site.
func TestEnrichmentHandler_HandleJob_MalformedPayload_ReturnsTypedSentinel(t *testing.T) {
	llm := &fakeLLMClient{}
	repo := &fakeAssetRepo{row: &AssetRow{ID: "asset-1"}}
	h, err := NewEnrichmentHandler(llm, repo, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewEnrichmentHandler: %v", err)
	}

	// Non-JSON payload (invalid syntax).
	job := &appjobs.Job{ID: "job-1", Payload: []byte(`{not valid json`)}

	_, err = h.HandleJob(context.Background(), job, nil)
	if !errors.Is(err, ErrEnrichmentPayloadInvalid) {
		t.Errorf("expected ErrEnrichmentPayloadInvalid, got %v", err)
	}

	// The LLM client must NOT be invoked on malformed payload
	// (terminal short-circuit before LLM call).
	if len(llm.requests) != 0 {
		t.Errorf("LLM client should not be invoked on malformed payload, got %d calls", len(llm.requests))
	}
}

// Test 6: Wrap helper functions preserve the sentinel chain
// via errors.Is (Go 1.20+ dual-%w pattern).
func TestEnrichmentWrapHelpers_PreserveSentinelChain(t *testing.T) {
	baseErr := errors.New("underlying cause")

	t.Run("WrapHandlerNotConfigured", func(t *testing.T) {
		err := WrapHandlerNotConfigured("llmClient")
		if !errors.Is(err, ErrEnrichmentHandlerNotConfigured) {
			t.Errorf("expected ErrEnrichmentHandlerNotConfigured, got %v", err)
		}
	})

	t.Run("WrapChunkNotFound", func(t *testing.T) {
		err := WrapChunkNotFound("asset-1")
		if !errors.Is(err, ErrEnrichmentChunkNotFound) {
			t.Errorf("expected ErrEnrichmentChunkNotFound, got %v", err)
		}
	})

	t.Run("WrapLLMUnavailable", func(t *testing.T) {
		err := WrapLLMUnavailable(baseErr)
		if !errors.Is(err, ErrEnrichmentLLMUnavailable) {
			t.Errorf("expected ErrEnrichmentLLMUnavailable, got %v", err)
		}
	})

	t.Run("WrapInvalidLLMResponse", func(t *testing.T) {
		err := WrapInvalidLLMResponse(baseErr)
		if !errors.Is(err, ErrEnrichmentInvalidLLMResponse) {
			t.Errorf("expected ErrEnrichmentInvalidLLMResponse, got %v", err)
		}
	})

	t.Run("WrapPayloadInvalid", func(t *testing.T) {
		err := WrapPayloadInvalid(baseErr)
		if !errors.Is(err, ErrEnrichmentPayloadInvalid) {
			t.Errorf("expected ErrEnrichmentPayloadInvalid, got %v", err)
		}
	})

	t.Run("WrapPersistFailed", func(t *testing.T) {
		err := WrapPersistFailed(baseErr)
		if !errors.Is(err, ErrEnrichmentPersistFailed) {
			t.Errorf("expected ErrEnrichmentPersistFailed, got %v", err)
		}
	})

	t.Run("WrapEmitFailed", func(t *testing.T) {
		err := WrapEmitFailed(baseErr)
		if !errors.Is(err, ErrEnrichmentEmitFailed) {
			t.Errorf("expected ErrEnrichmentEmitFailed, got %v", err)
		}
	})
}

// Test 7 (PR-011C): happy-path emit — when the LLM call
// succeeds AND the emitter is wired, the handler builds the
// canonical v1 envelope and emits it via the AssetPublishedEmitter
// port. The stub captures the emitted payload for hermetic TDD
// assertions.
func TestEnrichmentHandler_HandleJob_HappyPath_EmitsV1Envelope(t *testing.T) {
	llm := &fakeLLMClient{
		resp: &EnrichmentResponse{
			ChunkID: "stock:abc:chunk:0",
			Fields: EnrichedFields{
				Category: "Boxe",
				Subject:  "Manny Pacquiao",
			},
			Model: "gemma4:e4b",
		},
	}
	repo := &fakeAssetRepo{row: &AssetRow{
		ID:             "stock:abc:chunk:0",
		SourceURL:      "https://pexels.com/video/12345",
		Title:          "Pacquiao Broner Round 9",
		SourceProvider: "pexels",
		LegacyFileMD5:       validContentHash,
		DriveFileID:    "drive-file-12345",
		DrivePath:      "stock/Boxe/pexels/Manny-Pacquiao",
	}}
	emitter := &StubAssetPublishedEmitter{}
	h, err := NewEnrichmentHandler(llm, repo, emitter, zap.NewNop())
	if err != nil {
		t.Fatalf("NewEnrichmentHandler: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"chunk_id": "stock:abc:chunk:0"})
	job := &appjobs.Job{ID: "job-1", Payload: payload}

	result, err := h.HandleJob(context.Background(), job, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The stub MUST have been invoked exactly once.
	if emitter.CallCount != 1 {
		t.Fatalf("expected emitter.CallCount=1, got %d", emitter.CallCount)
	}
	if emitter.LastPayload == nil {
		t.Fatal("emitter.LastPayload is nil (no payload captured)")
	}

	// Assert the canonical v1 envelope wire-shape.
	got := emitter.LastPayload
	if got.SchemaVersion != outbox.AssetPublishedSchemaVersion {
		t.Errorf("SchemaVersion mismatch: got %q, want %q", got.SchemaVersion, outbox.AssetPublishedSchemaVersion)
	}
	if got.AssetID != "stock:abc:chunk:0" {
		t.Errorf("AssetID mismatch: got %q", got.AssetID)
	}
	if got.Destination != "stock" {
		t.Errorf("Destination mismatch: got %q, want %q", got.Destination, "stock")
	}
	if got.Origin != "generated" {
		t.Errorf("Origin mismatch: got %q, want %q", got.Origin, "generated")
	}
	if got.Category != "Boxe" {
		t.Errorf("Category mismatch: got %q (from LLM response)", got.Category)
	}
	if got.Subject != "Manny Pacquiao" {
		t.Errorf("Subject mismatch: got %q (from LLM response)", got.Subject)
	}
	if got.Provider != "pexels" {
		t.Errorf("Provider mismatch: got %q (from AssetRow.SourceProvider)", got.Provider)
	}
	if got.DriveFileID != "drive-file-12345" {
		t.Errorf("DriveFileID mismatch: got %q (from AssetRow.DriveFileID)", got.DriveFileID)
	}
	if got.DrivePath != "stock/Boxe/pexels/Manny-Pacquiao" {
		t.Errorf("DrivePath mismatch: got %q (from AssetRow.DrivePath)", got.DrivePath)
	}
	if got.ContentType != "video" {
		t.Errorf("ContentType mismatch: got %q, want %q", got.ContentType, "video")
	}
	if got.IdempotencyKey == "" {
		t.Error("IdempotencyKey is empty")
	}
	if !IsValidEnrichmentIdempotencyKey(got.IdempotencyKey) || len(got.IdempotencyKey) != 64 {
		t.Errorf("IdempotencyKey is not 64-char hex: %q", got.IdempotencyKey)
	}
	if got.EventID == "" {
		t.Error("EventID is empty")
	}
	if got.RequestedAt == "" {
		t.Error("RequestedAt is empty")
	}

	// Result envelope assertions.
	if result["handler_stage"] != "pr011c_v1_emit_ok" {
		t.Errorf("result.handler_stage mismatch: got %v", result["handler_stage"])
	}
	if result["destination"] != "stock" {
		t.Errorf("result.destination mismatch: got %v", result["destination"])
	}
}

// Test 8 (PR-011C): idempotency-key byte-stability across
// multiple invocations with the same (chunkID, contentHash,
// version) triple.
func TestEnrichmentHandler_HandleJob_IdempotencyKeyStable(t *testing.T) {
	llm := &fakeLLMClient{
		resp: &EnrichmentResponse{
			ChunkID: "stock:abc:chunk:0",
			Fields:  EnrichedFields{Category: "Boxe", Subject: "Manny Pacquiao"},
			Model:   "gemma4:e4b",
		},
	}
	row := &AssetRow{
		ID:             "stock:abc:chunk:0",
		LegacyFileMD5:       validContentHash,
		SourceProvider: "pexels",
	}
	// Run HandleJob twice with the same row + LLM response and
	// capture both idempotency keys. They MUST be byte-identical.
	var key1, key2 string
	for i := 0; i < 2; i++ {
		emitter := &StubAssetPublishedEmitter{}
		repo := &fakeAssetRepo{row: row}
		h, err := NewEnrichmentHandler(llm, repo, emitter, zap.NewNop())
		if err != nil {
			t.Fatalf("iteration %d: NewEnrichmentHandler: %v", i, err)
		}
		payload, _ := json.Marshal(map[string]string{"chunk_id": "stock:abc:chunk:0"})
		job := &appjobs.Job{ID: "job-1", Payload: payload}
		if _, err := h.HandleJob(context.Background(), job, nil); err != nil {
			t.Fatalf("iteration %d: HandleJob: %v", i, err)
		}
		if emitter.LastPayload == nil {
			t.Fatalf("iteration %d: no payload emitted", i)
		}
		if i == 0 {
			key1 = emitter.LastPayload.IdempotencyKey
		} else {
			key2 = emitter.LastPayload.IdempotencyKey
		}
	}
	if key1 == "" || key2 == "" {
		t.Fatalf("idempotency key is empty: key1=%q key2=%q", key1, key2)
	}
	if key1 != key2 {
		t.Errorf("idempotency_key NOT byte-stable across retries: key1=%q key2=%q", key1, key2)
	}
}

// Test 9 (PR-011C): emitter-side failure (e.g. SQLite locked,
// I/O error) is wrapped in ErrEnrichmentEmitFailed (retryable
// per godlike/07 worker-pool exponential backoff).
func TestEnrichmentHandler_HandleJob_EmitterError_ReturnsRetryableSentinel(t *testing.T) {
	baseErr := errors.New("SQLite: database is locked (SQLITE_BUSY)")
	llm := &fakeLLMClient{
		resp: &EnrichmentResponse{
			ChunkID: "stock:abc:chunk:0",
			Fields:  EnrichedFields{Category: "Boxe"},
			Model:   "gemma4:e4b",
		},
	}
	repo := &fakeAssetRepo{row: &AssetRow{
		ID:             "stock:abc:chunk:0",
		LegacyFileMD5:       validContentHash,
		SourceProvider: "pexels",
	}}
	emitter := &errorAssetPublishedEmitter{err: baseErr}
	h, err := NewEnrichmentHandler(llm, repo, emitter, zap.NewNop())
	if err != nil {
		t.Fatalf("NewEnrichmentHandler: %v", err)
	}
	payload, _ := json.Marshal(map[string]string{"chunk_id": "stock:abc:chunk:0"})
	job := &appjobs.Job{ID: "job-1", Payload: payload}

	_, err = h.HandleJob(context.Background(), job, nil)
	if !errors.Is(err, ErrEnrichmentEmitFailed) {
		t.Errorf("expected ErrEnrichmentEmitFailed, got %v", err)
	}
	// The error chain MUST preserve the underlying cause.
	if !errors.Is(err, baseErr) && !contains(err.Error(), "SQLite") {
		t.Errorf("expected underlying cause in chain, got %v", err)
	}
}

// contains is a tiny helper for the emitter-error test.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
