// Package enrichment — handler_test.go (PR-011A, July 2026).
//
// 4 hermetic TDD tests pinning the EnrichmentHandler contract.
// The tests use ONLY the canonical stub adapter (no SQLite, no
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
//
// godlike/06 SSOT (one canonical owner per fact): the 4 tests
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
)

// fakeAssetRepo is a hermetic test double for AssetRepository.
// Populates the row map with whatever the test wants the
// GetByID call to return; tracks UpdateEnrichedMetadata calls.
type fakeAssetRepo struct {
	// row is the canonical AssetRow returned by GetByID. When
	// nil, GetByID returns (nil, WrapChunkNotFound(id)).
	row *AssetRow

	// updateCalls tracks UpdateEnrichedMetadata invocations
	// for the happy-path happy-path test.
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

func (f *fakeLLMClient) Model() string {
	if f.resp != nil && f.resp.Model != "" {
		return f.resp.Model
	}
	return "test-model"
}

// Test 1: composition-time fail-closed on nil deps.
func TestNewEnrichmentHandler_NilDeps_ReturnsTypedSentinel(t *testing.T) {
	t.Run("nil LLMClient returns sentinel", func(t *testing.T) {
		repo := &fakeAssetRepo{row: &AssetRow{ID: "asset-1"}}
		h, err := NewEnrichmentHandler(nil, repo, zap.NewNop())
		if h != nil {
			t.Errorf("expected nil handler, got %+v", h)
		}
		if !errors.Is(err, ErrEnrichmentHandlerNotConfigured) {
			t.Errorf("expected ErrEnrichmentHandlerNotConfigured, got %v", err)
		}
	})

	t.Run("nil AssetRepo returns sentinel", func(t *testing.T) {
		llm := &fakeLLMClient{}
		h, err := NewEnrichmentHandler(llm, nil, zap.NewNop())
		if h != nil {
			t.Errorf("expected nil handler, got %+v", h)
		}
		if !errors.Is(err, ErrEnrichmentHandlerNotConfigured) {
			t.Errorf("expected ErrEnrichmentHandlerNotConfigured, got %v", err)
		}
	})

	t.Run("valid deps returns handler", func(t *testing.T) {
		llm := &fakeLLMClient{}
		repo := &fakeAssetRepo{row: &AssetRow{ID: "asset-1"}}
		h, err := NewEnrichmentHandler(llm, repo, zap.NewNop())
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
		if h.Log == nil {
			t.Error("Log should be non-nil (zap.NewNop fallback)")
		}
	})
}

// Test 2: terminal sentinel on empty chunk_id (handler does not
// invoke the LLM client in this case).
func TestEnrichmentHandler_HandleJob_EmptyChunkID_ReturnsTerminalSentinel(t *testing.T) {
	llm := &fakeLLMClient{}
	repo := &fakeAssetRepo{row: &AssetRow{ID: "asset-1"}}
	h, err := NewEnrichmentHandler(llm, repo, zap.NewNop())
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
	h, err := NewEnrichmentHandler(llm, repo, zap.NewNop())
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
// to the LLMClient. The LLMClient returns a valid response
// (the stub adapter in production will be replaced by the
// real ollama adapter in PR-011B).
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
	}}
	h, err := NewEnrichmentHandler(llm, repo, zap.NewNop())
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

	// Result envelope (PR-011A: minimal; PR-011B/C will add
	// the persistence + outbox emit fields).
	if result["chunk_id"] != "asset-1" {
		t.Errorf("result.chunk_id mismatch: got %v", result["chunk_id"])
	}
	if result["model"] != "gemma4:e4b" {
		t.Errorf("result.model mismatch: got %v", result["model"])
	}
	if result["handler_stage"] != "pr011a_stub_llm_called" {
		t.Errorf("result.handler_stage mismatch: got %v", result["handler_stage"])
	}
}

// Test 5: terminal sentinel on malformed payload (non-JSON).
// The handler must return a typed sentinel so the producer
// (PR-011C stock fan-out) can fix the enqueue call site.
func TestEnrichmentHandler_HandleJob_MalformedPayload_ReturnsTypedSentinel(t *testing.T) {
	llm := &fakeLLMClient{}
	repo := &fakeAssetRepo{row: &AssetRow{ID: "asset-1"}}
	h, err := NewEnrichmentHandler(llm, repo, zap.NewNop())
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
		if !errors.Is(err, ErrEnrichmentHandlerNotConfigured) {
			t.Errorf("sentinel chain broken")
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
}
