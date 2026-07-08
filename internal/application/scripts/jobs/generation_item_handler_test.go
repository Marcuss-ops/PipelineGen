// Package jobs — generation_item_handler_test.go (PR-4 child→parent
// doc_id/doc_link propagation, 2026-07-08). Closes the canonical
// contract loop: the child handler writes doc_link/doc_id when the
// per-item pipeline produced a Google Doc; the parent aggregator
// collects these into per-item maps and surfaces them in the parent
// result.
//
// 4 hermetic TDD tests, all GREEN, all in-package (jobs_test).
// Production source: script_generation_item_handler.go. The
// canonical pure function under test is toScriptItemResultMap; the
// canonical handler entry is ScriptGenerateItemJobHandler.HandleJob.
//
// godlike/07 typed-error contract: tests assert key-presence + key-
// absence (the omitempty contract is load-bearing — operators must
// never see a doc_id field when no doc was created).
package jobs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

// stubGenerateOneExecutor is the canonical in-memory test stub for
// the GenerateOneExecutor narrow port. It returns the pre-configured
// result + err verbatim, letting each test pin the exact shape the
// canonical handler must propagate to toScriptItemResultMap.
type stubGenerateOneExecutor struct {
	Result *domainScript.GenerationResult
	Err    error
	Calls  int
}

// Execute satisfies GenerateOneExecutor (Pattern 0 — AGENTS.md). The
// tracker is intentionally ignored (nil-safe per the canonical
// GenerateOneUseCase contract). The tracker type MUST be exactly
// *usecase.ProgressTracker to satisfy the port interface — the
// canonical HandleJob passes a typed nil pointer through.
func (s *stubGenerateOneExecutor) Execute(
	_ context.Context,
	_ domainScript.GenerationItemV2,
	_ domainScript.Preset,
	_ *usecase.ProgressTracker,
) (*domainScript.GenerationResult, error) {
	s.Calls++
	return s.Result, s.Err
}

// Compile-time assertion: stubGenerateOneExecutor satisfies
// GenerateOneExecutor (catches future port-signature drift).
var _ GenerateOneExecutor = (*stubGenerateOneExecutor)(nil)

// Test 1 (PR-4 child side): toScriptItemResultMap writes doc_link +
// doc_id when res.Artifacts.Document is non-nil and both fields are
// populated. This is the canonical happy-path child emission — the
// aggregator's childDocLinks/childDocIDs maps depend on it.
func TestGenerationItemHandler_ToResultMap_PopulatesDocFields(t *testing.T) {
	res := &domainScript.GenerationResult{
		ItemID: "item-doc-1",
		Output: domainScript.ScriptOutput{Text: "Hello world"},
		Artifacts: domainScript.ArtifactResult{
			Document: &domainScript.DocumentArtifact{
				DocLink: "https://docs.google.com/document/d/abc123/edit",
				DocID:   "abc123",
				Status:  "completed",
			},
		},
	}

	m := toScriptItemResultMap("item-doc-1", "child-1", "parent-1", true, "", res)

	if got, ok := m["doc_link"].(string); !ok || got != "https://docs.google.com/document/d/abc123/edit" {
		t.Errorf("PR-4: toScriptItemResultMap must write doc_link from res.Artifacts.Document, got %v (ok=%v)", m["doc_link"], ok)
	}
	if got, ok := m["doc_id"].(string); !ok || got != "abc123" {
		t.Errorf("PR-4: toScriptItemResultMap must write doc_id from res.Artifacts.Document, got %v (ok=%v)", m["doc_id"], ok)
	}
	if m["ok"] != true {
		t.Errorf("PR-4: ok field must be true, got %v", m["ok"])
	}
	if m["item_id"] != "item-doc-1" {
		t.Errorf("PR-4: item_id must round-trip, got %v", m["item_id"])
	}
}

// Test 2 (PR-4 child side): toScriptItemResultMap OMITS doc_link +
// doc_id when (a) the Document artifact is nil OR (b) the fields are
// empty strings. The omitempty contract is load-bearing — operators
// must never see a doc_id field when no doc was produced.
func TestGenerationItemHandler_ToResultMap_OmitsEmptyDocFields(t *testing.T) {
	cases := []struct {
		name string
		res  *domainScript.GenerationResult
	}{
		{
			name: "nil_document_artifact",
			res: &domainScript.GenerationResult{
				Output:    domainScript.ScriptOutput{Text: "Hello"},
				Artifacts: domainScript.ArtifactResult{Document: nil},
			},
		},
		{
			name: "empty_doc_link_and_doc_id",
			res: &domainScript.GenerationResult{
				Output: domainScript.ScriptOutput{Text: "Hello"},
				Artifacts: domainScript.ArtifactResult{
					Document: &domainScript.DocumentArtifact{
						DocLink: "",
						DocID:   "",
						Status:  "completed",
					},
				},
			},
		},
		{
			name: "doc_link_only_present",
			res: &domainScript.GenerationResult{
				Output: domainScript.ScriptOutput{Text: "Hello"},
				Artifacts: domainScript.ArtifactResult{
					Document: &domainScript.DocumentArtifact{
						DocLink: "https://docs.google.com/document/d/abc/edit",
						DocID:   "",
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := toScriptItemResultMap("item-x", "child-x", "parent-x", true, "", tc.res)
			if _, present := m["doc_link"]; present && tc.res.Artifacts.Document != nil && tc.res.Artifacts.Document.DocLink == "" {
				t.Errorf("PR-4: doc_link must be omitted when Document.DocLink is empty, got %v", m["doc_link"])
			}
			if _, present := m["doc_id"]; present && tc.res.Artifacts.Document != nil && tc.res.Artifacts.Document.DocID == "" {
				t.Errorf("PR-4: doc_id must be omitted when Document.DocID is empty, got %v", m["doc_id"])
			}
			if _, present := m["doc_link"]; present && tc.res.Artifacts.Document == nil {
				t.Errorf("PR-4: doc_link must be omitted when Document artifact is nil, got %v", m["doc_link"])
			}
			if _, present := m["doc_id"]; present && tc.res.Artifacts.Document == nil {
				t.Errorf("PR-4: doc_id must be omitted when Document artifact is nil, got %v", m["doc_id"])
			}
		})
	}
}

// Test 3 (PR-4 child side): HandleJob end-to-end with a successful
// stub executor. Verifies the canonical child result map flows the
// doc_link/doc_id fields through HandleJob → toScriptItemResultMap
// → returned map[string]any. The JSON round-trip via json.Marshal
// proves the canonical wire-shape (omitempty contract preserved).
func TestGenerationItemHandler_HandleJob_SuccessWithDoc(t *testing.T) {
	stub := &stubGenerateOneExecutor{
		Result: &domainScript.GenerationResult{
			ItemID: "item-1",
			Output: domainScript.ScriptOutput{Text: "Hi"},
			Artifacts: domainScript.ArtifactResult{
				Document: &domainScript.DocumentArtifact{
					DocLink: "https://docs.google.com/document/d/xyz789/edit",
					DocID:   "xyz789",
					Status:  "completed",
				},
			},
		},
	}
	handler := NewScriptGenerateItemJobHandler(stub, zap.NewNop())

	payload, _ := json.Marshal(ScriptGenerateItemPayload{
		ParentJobID: "parent-1",
		Item:        domainScript.GenerationItemV2{ID: "item-1"},
		Preset:      domainScript.PresetCustom,
		ItemIndex:   0,
	})
	j := &appjobs.Job{ID: "child-1", Payload: payload}

	m, err := handler.HandleJob(context.Background(), j, nil)
	if err != nil {
		t.Fatalf("PR-4: HandleJob must return nil error on success, got %v", err)
	}
	if stub.Calls != 1 {
		t.Errorf("PR-4: stub.Execute must be called exactly once, got %d", stub.Calls)
	}

	// Wire-shape round-trip: marshal the returned map to JSON and
	// inspect the resulting bytes. This proves the canonical
	// map[string]any shape the dispatcher writes into job.Result
	// contains the doc fields (omitempty contract preserved).
	raw, mErr := json.Marshal(m)
	if mErr != nil {
		t.Fatalf("PR-4: json.Marshal of result map must succeed, got %v", mErr)
	}
	wire := string(raw)
	if !strings.Contains(wire, `"doc_link":"https://docs.google.com/document/d/xyz789/edit"`) {
		t.Errorf("PR-4: wire-shape must contain doc_link, got: %s", wire)
	}
	if !strings.Contains(wire, `"doc_id":"xyz789"`) {
		t.Errorf("PR-4: wire-shape must contain doc_id, got: %s", wire)
	}
	if !strings.Contains(wire, `"item_id":"item-1"`) {
		t.Errorf("PR-4: wire-shape must contain item_id, got: %s", wire)
	}
	if !strings.Contains(wire, `"ok":true`) {
		t.Errorf("PR-4: wire-shape must contain ok=true, got: %s", wire)
	}
}

// Test 4 (PR-4 child side): HandleJob with a failing executor. The
// returned map must carry the error field and MUST NOT carry any
// doc fields (per the scriptItemIsSuccessful false-success gate:
// a failed item cannot surface a doc link because the doc-creation
// postprocessor never ran).
func TestGenerationItemHandler_HandleJob_FailureOmitsDoc(t *testing.T) {
	stub := &stubGenerateOneExecutor{
		Err: &typedTestError{msg: "engine invocation failed"},
	}
	handler := NewScriptGenerateItemJobHandler(stub, zap.NewNop())

	payload, _ := json.Marshal(ScriptGenerateItemPayload{
		ParentJobID: "parent-1",
		Item:        domainScript.GenerationItemV2{ID: "item-fail"},
		Preset:      domainScript.PresetCustom,
		ItemIndex:   0,
	})
	j := &appjobs.Job{ID: "child-fail", Payload: payload}

	m, err := handler.HandleJob(context.Background(), j, nil)
	if err == nil {
		t.Fatal("PR-4: HandleJob must return non-nil error on executor failure")
	}
	if m == nil {
		t.Fatal("PR-4: HandleJob must return non-nil result map even on failure (so the dispatcher can write ok=false)")
	}

	// Result map must carry the error field and ok=false.
	if m["ok"] != false {
		t.Errorf("PR-4: failed item must have ok=false, got %v", m["ok"])
	}
	if _, present := m["error"]; !present {
		t.Errorf("PR-4: failed item must have error key in result map")
	}

	// Result map must NOT carry any doc fields — the per-item
	// pipeline produced no Google Doc, so the aggregator must not
	// see a doc link to propagate.
	raw, _ := json.Marshal(m)
	wire := string(raw)
	if strings.Contains(wire, `"doc_link"`) {
		t.Errorf("PR-4: failed item must NOT carry doc_link, got: %s", wire)
	}
	if strings.Contains(wire, `"doc_id"`) {
		t.Errorf("PR-4: failed item must NOT carry doc_id, got: %s", wire)
	}
}

// typedTestError is a minimal error type for the stub executor. It
// implements the error interface so it composes with the
// fmt.Errorf("%w", ...) wrap idiom in HandleJob.
type typedTestError struct{ msg string }

func (e *typedTestError) Error() string { return e.msg }
