package rustexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

func newQualityGateTestAdapter(t *testing.T, response string, requestPath string) *SemanticEngineAdapter {
	t.Helper()
	dir := t.TempDir()
	workerPath := filepath.Join(dir, "quality-gate-worker.sh")
	script := fmt.Sprintf(`#!/bin/sh
while IFS= read -r line; do
  printf '%%s\n' "$line" > %q
  printf '%%s\n' %q
done
`, requestPath, response)
	if err := os.WriteFile(workerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(workerPath, "ffmpeg", zap.NewNop())
	t.Cleanup(executor.Close)
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func qualityGateFixture() (*scriptpkg.GenerationResult, scriptpkg.GenerationItemV2, scriptpkg.ResolvedGenerationPlan) {
	item := scriptpkg.GenerationItemV2{ID: "qg-item"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Language:    "en",
		SourceText:  "The quick brown fox jumps over the lazy dog in Italy.",
		TargetWords: 11,
		SourceKind:  "text",
	}
	result := &scriptpkg.GenerationResult{
		ItemID: "qg-item",
		Output: scriptpkg.ScriptOutput{
			Text:      "The quick brown fox jumps over the lazy dog in Italy.",
			WordCount: 11,
		},
	}
	return result, item, plan
}

func TestSemanticEngineQualityGateUsesWorkerVerdict(t *testing.T) {
	requestPath := filepath.Join(t.TempDir(), "request.json")
	response := `{"ok":true,"quality":{"language_requested":"en","language_detected":"en","source_text_coverage":1,"source_text_coverage_status":"EVALUATED","clip_evidence_coverage":1,"unsupported_claims":0,"target_words":11,"actual_words":11,"passed":true},"reasons":[]}`
	adapter := newQualityGateTestAdapter(t, response, requestPath)

	result, item, plan := qualityGateFixture()
	quality, err := adapter.EvaluateQualityGate(context.Background(), result, item, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if quality == nil || !quality.Passed {
		t.Fatalf("expected passing quality, got %+v", quality)
	}

	raw, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Operation string `json:"operation"`
		Item      struct {
			ID string `json:"id"`
		} `json:"item"`
		Plan struct {
			Language    string `json:"language"`
			SourceText  string `json:"source_text"`
			TargetWords int    `json:"target_words"`
		} `json:"plan"`
		Result struct {
			Output struct {
				Text      string `json:"text"`
				WordCount int    `json:"word_count"`
			} `json:"output"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.Operation != "quality_gate" || request.Item.ID != item.ID {
		t.Fatalf("unexpected request identity: %+v", request)
	}
	if request.Plan.Language != plan.Language || request.Plan.SourceText != plan.SourceText || request.Plan.TargetWords != plan.TargetWords {
		t.Fatalf("unexpected plan projection: %+v", request.Plan)
	}
	if request.Result.Output.WordCount != result.Output.WordCount {
		t.Fatalf("unexpected result projection: %+v", request.Result)
	}
}

func TestSemanticEngineQualityGateReturnsTypedFailure(t *testing.T) {
	response := `{"ok":true,"quality":{"language_requested":"en","source_text_coverage":0.1,"source_text_coverage_status":"EVALUATED","target_words":11,"actual_words":11,"passed":false},"reasons":["source_text coverage below threshold"]}`
	adapter := newQualityGateTestAdapter(t, response, filepath.Join(t.TempDir(), "request.json"))

	result, item, plan := qualityGateFixture()
	quality, err := adapter.EvaluateQualityGate(context.Background(), result, item, plan)
	if err == nil {
		t.Fatal("expected quality gate failure")
	}
	var gateErr *scriptpkg.QualityGateError
	if !errors.As(err, &gateErr) {
		t.Fatalf("expected *QualityGateError, got %T", err)
	}
	if quality == nil || quality.Passed {
		t.Fatalf("expected failed quality, got %+v", quality)
	}
	if len(gateErr.Reasons) != 1 || gateErr.ItemID != item.ID {
		t.Fatalf("unexpected gate error: %+v", gateErr)
	}
}

func TestSemanticEngineQualityGateFailsClosedOnProtocolError(t *testing.T) {
	adapter := newQualityGateTestAdapter(t, `{"ok":false,"error":"invalid quality_gate request"}`, filepath.Join(t.TempDir(), "request.json"))

	result, item, plan := qualityGateFixture()
	if _, err := adapter.EvaluateQualityGate(context.Background(), result, item, plan); err == nil {
		t.Fatal("expected a protocol error")
	}
}

func TestSemanticEngineRealBinaryEvaluatesQualityGate(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed; run make rust-fetch")
	}
	executor := NewSemanticEngineExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	t.Cleanup(executor.Close)
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}

	result, item, plan := qualityGateFixture()
	quality, err := adapter.EvaluateQualityGate(context.Background(), result, item, plan)
	if err != nil {
		t.Fatalf("real binary quality gate failed: %v", err)
	}
	if quality == nil || !quality.Passed {
		t.Fatalf("expected the local bundle to pass the clean fixture, got %+v", quality)
	}
}
