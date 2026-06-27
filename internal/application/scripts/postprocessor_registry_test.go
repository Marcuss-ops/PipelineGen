// Package scripts_test — postprocessor_registry_test.go exercises
// the PostProcessorRegistry: freeze, duplicate rejection, nil
// safety, skip disabled, fail when processor unavailable, and
// per-processor error isolation.
//
// PR 3 (June 2026): the PostProcessor interface changed to a
// 4-arg Process signature (ctx, plan, *ModelScriptOutputV1,
// accumulator *PostProcessArtifact). The fake countingProcessor
// follows the new signature, and Run() callers pass the typed
// model instead of the pre-PR-3 ProcessInput envelope.
package scripts_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Fakes ──────────────────────────────────────────────────────────

type countingProcessor struct {
	name  string
	calls int
	docID string
	err   error
}

func (p *countingProcessor) Name() string { return p.name }

// PR 3 (June 2026): 4-arg signature (ctx, plan, *ModelScriptOutputV1, accumulator).
func (p *countingProcessor) Process(
	_ context.Context,
	_ *scriptpkg.ResolvedGenerationPlan,
	_ *scriptpkg.ModelScriptOutputV1,
	_ *scripts.PostProcessArtifact,
) (*scripts.PostProcessArtifact, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	if p.docID != "" {
		return &scripts.PostProcessArtifact{
			Document: &scriptpkg.DocumentArtifact{
				DocID:   p.docID,
				DocLink: "https://docs.example.com/" + p.docID,
			},
		}, nil
	}
	return &scripts.PostProcessArtifact{}, nil
}

// emptyModel returns a typed zero-valued canonical model that
// satisfies Process's *ModelScriptOutputV1 argument. Tests do
// NOT need a meaningful model — the countingProcessor doesn't
// read it. Kept as a helper so calling code reads at one glance.
func emptyModel() *scriptpkg.ModelScriptOutputV1 {
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Generated script text.",
		SpecScene:     scriptpkg.SpecSceneOutput{Version: 1},
	}
}

// ── Registration ───────────────────────────────────────────────────

func TestRegistry_Register(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	ok := r.Register(&countingProcessor{name: "entities"})
	if !ok {
		t.Fatal("first register should succeed")
	}
	if !r.Registered("entities") {
		t.Error("entities should be registered")
	}
	if r.Len() != 1 {
		t.Errorf("len: %d", r.Len())
	}
}

func TestRegistry_RegisterNil(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	if r.Register(nil) {
		t.Error("nil processor should not register")
	}
	var nilReg *scripts.PostProcessorRegistry
	if nilReg.Register(&countingProcessor{name: "x"}) {
		t.Error("nil registry should not register")
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "doc"})
	ok := r.Register(&countingProcessor{name: "doc"})
	if ok {
		t.Error("duplicate registration should be rejected")
	}
	if r.Len() != 1 {
		t.Errorf("len after duplicate: %d", r.Len())
	}
}

// ── Freeze ────────────────────────────────────────────────────────

func TestRegistry_Freeze(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "entities"})
	r.Freeze()

	if !r.IsFrozen() {
		t.Error("should be frozen after Freeze()")
	}

	if r.Register(&countingProcessor{name: "metadata"}) {
		t.Error("register after freeze should fail")
	}
	if r.Len() != 1 {
		t.Errorf("len after freeze-register: %d", r.Len())
	}

	r.Freeze()
	if !r.IsFrozen() {
		t.Error("should still be frozen after second Freeze()")
	}
}

func TestRegistry_FreezeNil(t *testing.T) {
	var r *scripts.PostProcessorRegistry
	r.Freeze()
	if r.IsFrozen() {
		t.Error("nil registry should not be frozen")
	}
}

// ── Run ────────────────────────────────────────────────────────────

func TestRegistry_RunCallsEnabledProcessors(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	doc := &countingProcessor{name: "document", docID: "doc-1"}
	persist := &countingProcessor{name: "persistence"}
	r.Register(doc)
	r.Register(persist)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Title:          "Test",
		Postprocessors: []string{"document", "persistence"},
	}

	result, err := r.Run(context.Background(), plan, emptyModel())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if doc.calls != 1 {
		t.Errorf("document calls: %d", doc.calls)
	}
	if persist.calls != 1 {
		t.Errorf("persistence calls: %d", persist.calls)
	}
	if result.Document == nil || result.Document.DocID != "doc-1" {
		t.Errorf("Document artifact not aggregated: %+v", result.Document)
	}
}

func TestRegistry_RunSkipsDisabledProcessors(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	doc := &countingProcessor{name: "document", docID: "d1"}
	persist := &countingProcessor{name: "persistence"}
	r.Register(doc)
	r.Register(persist)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"document"},
	}

	_, err := r.Run(context.Background(), plan, emptyModel())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if doc.calls != 1 {
		t.Errorf("document should be called: got %d", doc.calls)
	}
	if persist.calls != 0 {
		t.Errorf("persistence should NOT be called: got %d", persist.calls)
	}
}

func TestRegistry_RunProcessorErrorIsIsolated(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	doc := &countingProcessor{name: "document", err: errors.New("drive api down")}
	persist := &countingProcessor{name: "persistence"}
	r.Register(doc)
	r.Register(persist)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"document", "persistence"},
	}

	_, err := r.Run(context.Background(), plan, emptyModel())
	if err != nil {
		t.Fatalf("run should not fail on partial error: %v", err)
	}
	if doc.calls != 1 {
		t.Errorf("document should have been attempted: %d", doc.calls)
	}
	if persist.calls != 1 {
		t.Errorf("persistence should still run after document error: %d", persist.calls)
	}
}

func TestRegistry_RunProcessorNotRegistered(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "document"})

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"voiceover"},
	}

	_, err := r.Run(context.Background(), plan, emptyModel())
	if err != nil {
		t.Fatalf("run should not fail on missing processor: %v", err)
	}
}

func TestRegistry_RunNilRegistry(t *testing.T) {
	var r *scripts.PostProcessorRegistry
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"document"},
	}

	result, err := r.Run(context.Background(), plan, emptyModel())
	if err != nil {
		t.Errorf("nil registry should return empty result: %v", err)
	}
	if result == nil {
		t.Fatal("nil registry should return non-nil empty result")
	}
}

func TestRegistry_RunEmptyRegistry(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"document"},
	}

	result, err := r.Run(context.Background(), plan, emptyModel())
	if err != nil {
		t.Errorf("empty registry should return empty result: %v", err)
	}
	if result == nil {
		t.Fatal("empty registry should return non-nil result")
	}
}

func TestRegistry_RunEmptyPostprocessors(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "document"})

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: nil,
	}

	_, err := r.Run(context.Background(), plan, emptyModel())
	if err != nil {
		t.Fatalf("empty postprocessors list should succeed: %v", err)
	}
}

// ── Merge ──────────────────────────────────────────────────────────

func TestRegistry_MergeAllFields(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())

	df := &countingProcessor{name: "document", docID: "doc-merged"}
	r.Register(df)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"document"},
	}

	result, err := r.Run(context.Background(), plan, emptyModel())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Document == nil || result.Document.DocID != "doc-merged" {
		t.Errorf("DocID not merged: %+v", result.Document)
	}
}
