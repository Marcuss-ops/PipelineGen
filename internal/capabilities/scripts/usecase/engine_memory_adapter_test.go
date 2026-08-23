package usecase

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"go.uber.org/zap"
)

// fakeMemoryGate is an in-memory implementation of
// scriptports.MemoryGate used to test the engine adapter without
// touching a real database.
type fakeScriptMemoryGate struct {
	entries map[string]*scriptports.GenerationOutput
}

func newFakeScriptMemoryGate() *fakeScriptMemoryGate {
	return &fakeScriptMemoryGate{entries: make(map[string]*scriptports.GenerationOutput)}
}

func (f *fakeScriptMemoryGate) cacheKey(channelID, mode, inputHash string) string {
	return channelID + "|" + mode + "|" + inputHash
}

func (f *fakeScriptMemoryGate) FindExactOutput(_ context.Context, channelID, mode, inputHash string) (*scriptports.GenerationOutput, error) {
	return f.entries[f.cacheKey(channelID, mode, inputHash)], nil
}

func (f *fakeScriptMemoryGate) SaveGeneration(_ context.Context, input scriptports.SaveGenerationInput, output string) (int64, error) {
	hashKey := input.CacheKey
	if hashKey == "" {
		hashKey = input.Title
	}
	f.entries[f.cacheKey(input.ChannelID, input.Mode, hashKey)] = &scriptports.GenerationOutput{
		OutputText: output,
		WordCount:  input.WordCount,
		Model:      input.Model,
	}
	return 1, nil
}

func (f *fakeScriptMemoryGate) DeleteExactOutputsByTitles(context.Context, []string) (int64, error) {
	return 0, nil
}

func (f *fakeScriptMemoryGate) SweepAll(context.Context) (int64, error) {
	return 0, nil
}

func TestMemoryGateAdapter_MissThenHit(t *testing.T) {
	gate := newFakeScriptMemoryGate()
	svc := adapters.NewService(gate, zap.NewNop())
	checker := NewMemoryGateChecker(svc)

	req := memoryGateRequest{
		ChannelID: "default",
		Title:     "Adapter",
		Language:  "en",
		Mode:      "text",
		CacheKey:  "adapter-key",
		UseMemory: true,
	}

	// First call is a miss.
	res, err := checker.CheckGate(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckGate miss errored: %v", err)
	}
	if res != nil {
		t.Fatalf("expected miss, got %+v", res)
	}

	// Save a row through the service.
	_, err = svc.SaveAfterGeneration(context.Background(), adapters.SaveGenerationInput{
		ChannelID: "default",
		Mode:      "text",
		Language:  "en",
		Title:     "Adapter",
		Prompt:    "p",
		Model:     "gemma",
		WordCount: 7,
		CacheKey:  "adapter-key",
	}, "adapter output")
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Second call is a hit and the result is translated to the
	// engine's in-package type.
	res, err = checker.CheckGate(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckGate hit errored: %v", err)
	}
	if res == nil {
		t.Fatal("expected hit after saving")
	}
	if !res.Hit {
		t.Errorf("expected Hit=true, got %v", res.Hit)
	}
	if res.Output != "adapter output" {
		t.Errorf("output = %q, want %q", res.Output, "adapter output")
	}
	if res.WordCount != 7 {
		t.Errorf("word_count = %d, want 7", res.WordCount)
	}
	if res.Model != "gemma" {
		t.Errorf("model = %q, want gemma", res.Model)
	}
}

func TestMemoryGateAdapter_NilService(t *testing.T) {
	checker := NewMemoryGateChecker(nil)
	req := memoryGateRequest{
		Title:     "Nil",
		CacheKey:  "nil-key",
		UseMemory: true,
	}
	res, err := checker.CheckGate(context.Background(), req)
	if err != nil || res != nil {
		t.Fatalf("nil service should report miss, got res=%+v err=%v", res, err)
	}
}
