package adapters

import (
	"context"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"go.uber.org/zap"
)

// fakeMemoryGate is an in-memory implementation of scriptports.MemoryGate
// suitable for testing the application-layer Service without SQLite.
type fakeMemoryGate struct {
	entries map[string]*scriptports.GenerationOutput
	saves   []scriptports.SaveGenerationInput
}

func newFakeMemoryGate() *fakeMemoryGate {
	return &fakeMemoryGate{
		entries: make(map[string]*scriptports.GenerationOutput),
	}
}

func (f *fakeMemoryGate) cacheKey(channelID, mode, inputHash string) string {
	return channelID + "|" + mode + "|" + inputHash
}

func (f *fakeMemoryGate) FindExactOutput(_ context.Context, channelID, mode, inputHash string) (*scriptports.GenerationOutput, error) {
	out, ok := f.entries[f.cacheKey(channelID, mode, inputHash)]
	if !ok {
		return nil, nil
	}
	return out, nil
}

func (f *fakeMemoryGate) SaveGeneration(_ context.Context, input scriptports.SaveGenerationInput, output string) (int64, error) {
	f.saves = append(f.saves, input)
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

func (f *fakeMemoryGate) DeleteExactOutputsByTitles(_ context.Context, titles []string) (int64, error) {
	titleSet := make(map[string]struct{}, len(titles))
	for _, t := range titles {
		titleSet[t] = struct{}{}
	}
	var toDelete []string
	for key := range f.entries {
		// The title is not stored on GenerationOutput in the fake;
		// we reconstruct the cache key and delete entries that were
		// saved with matching titles by scanning saved inputs. This
		// is sufficient for the adapter test.
		for _, in := range f.saves {
			if f.cacheKey(in.ChannelID, in.Mode, in.CacheKey) == key || (in.CacheKey == "" && f.cacheKey(in.ChannelID, in.Mode, in.Title) == key) {
				if _, ok := titleSet[in.Title]; ok {
					delete(f.entries, key)
					toDelete = append(toDelete, key)
				}
			}
		}
	}
	_ = toDelete
	return int64(len(toDelete)), nil
}

func (f *fakeMemoryGate) SweepAll(context.Context) (int64, error) {
	return 0, nil
}

// ── NewService ──────────────────────────────────────────────────────────

func TestNewService_NonNil(t *testing.T) {
	repo := newFakeMemoryGate()
	svc := NewService(repo, nil)
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
	if svc.repo != repo {
		t.Error("Service.repo should be the passed-in repo")
	}
}

func TestNewService_NilLog(t *testing.T) {
	repo := newFakeMemoryGate()
	svc := NewService(repo, nil)
	if svc.log == nil {
		t.Error("Service.log should be non-nil even when nil passed")
	}
	svc.log.Info("test log line")
}

func TestNewService_WithLog(t *testing.T) {
	repo := newFakeMemoryGate()
	customLog := zap.NewNop()
	svc := NewService(repo, customLog)
	if svc.log != customLog {
		t.Error("Service.log should be the passed-in logger")
	}
}

// ── CheckGate ───────────────────────────────────────────────────────────

func TestCheckGate_MissThenHit(t *testing.T) {
	repo := newFakeMemoryGate()
	svc := NewService(repo, zap.NewNop())

	req := MemoryGateRequest{
		ChannelID:    "ch-1",
		Title:        "Test Title",
		Prompt:       "Write about X",
		Language:     "en",
		Mode:         ModeClipToScript,
		CacheKey:     "deadbeef",
		UseMemory:    true,
		ForceRefresh: false,
	}

	res, err := svc.CheckGate(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckGate miss should not error: %v", err)
	}
	if res != nil {
		t.Fatalf("expected cache miss, got %+v", res)
	}

	_, err = svc.SaveAfterGeneration(context.Background(), SaveGenerationInput{
		ChannelID: req.ChannelID,
		Mode:      req.Mode,
		Language:  req.Language,
		Title:     req.Title,
		Prompt:    req.Prompt,
		Model:     "gemma-test",
		WordCount: 123,
		CacheKey:  req.CacheKey,
	}, "cached output text")
	if err != nil {
		t.Fatalf("SaveAfterGeneration failed: %v", err)
	}

	res, err = svc.CheckGate(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckGate hit should not error: %v", err)
	}
	if res == nil {
		t.Fatal("expected cache hit after saving")
	}
	if !res.Hit {
		t.Errorf("expected Hit=true, got %v", res.Hit)
	}
	if res.Output != "cached output text" {
		t.Errorf("output = %q, want %q", res.Output, "cached output text")
	}
	if res.WordCount != 123 {
		t.Errorf("word_count = %d, want 123", res.WordCount)
	}
	if res.Model != "gemma-test" {
		t.Errorf("model = %q, want gemma-test", res.Model)
	}
}

func TestCheckGate_UseMemoryFalse(t *testing.T) {
	svc := NewService(newFakeMemoryGate(), zap.NewNop())
	req := MemoryGateRequest{UseMemory: false, CacheKey: "key"}
	res, err := svc.CheckGate(context.Background(), req)
	if err != nil || res != nil {
		t.Fatalf("expected nil miss with UseMemory=false, got res=%+v err=%v", res, err)
	}
}

func TestCheckGate_ForceRefresh(t *testing.T) {
	svc := NewService(newFakeMemoryGate(), zap.NewNop())
	req := MemoryGateRequest{UseMemory: true, ForceRefresh: true, CacheKey: "key"}
	res, err := svc.CheckGate(context.Background(), req)
	if err != nil || res != nil {
		t.Fatalf("expected nil miss with ForceRefresh=true, got res=%+v err=%v", res, err)
	}
}

func TestCheckGate_EmptyRequest(t *testing.T) {
	svc := NewService(newFakeMemoryGate(), zap.NewNop())
	result, err := svc.CheckGate(context.Background(), MemoryGateRequest{})
	if err != nil {
		t.Errorf("CheckGate with empty request: %v", err)
	}
	if result != nil {
		t.Error("CheckGate with empty request should return nil")
	}
}

func TestCheckGate_NilRepo(t *testing.T) {
	svc := NewService(nil, zap.NewNop())
	req := MemoryGateRequest{UseMemory: true, CacheKey: "key"}
	res, err := svc.CheckGate(context.Background(), req)
	if err != nil || res != nil {
		t.Fatalf("expected nil when repo is nil, got res=%+v err=%v", res, err)
	}
}

// ── SaveAfterGeneration ────────────────────────────────────────────────

func TestSaveAfterGeneration_Upsert(t *testing.T) {
	repo := newFakeMemoryGate()
	svc := NewService(repo, zap.NewNop())

	in := SaveGenerationInput{
		ChannelID: "ch-1",
		Mode:      ModeText,
		Language:  "it",
		Title:     "Upsert",
		Prompt:    "prompt",
		Model:     "gemma",
		WordCount: 100,
		CacheKey:  "upsert-hash",
	}

	n, err := svc.SaveAfterGeneration(context.Background(), in, "first output")
	if err != nil {
		t.Fatalf("first save failed: %v", err)
	}
	if n != 1 {
		t.Errorf("first save rows = %d, want 1", n)
	}

	n, err = svc.SaveAfterGeneration(context.Background(), SaveGenerationInput{
		ChannelID: "ch-1",
		Mode:      ModeText,
		Language:  "it",
		Title:     "Upsert",
		Prompt:    "prompt",
		Model:     "gemma",
		WordCount: 200,
		CacheKey:  "upsert-hash",
	}, "second output")
	if err != nil {
		t.Fatalf("second save failed: %v", err)
	}
	if n != 1 {
		t.Errorf("upsert rows = %d, want 1", n)
	}

	res, err := svc.CheckGate(context.Background(), MemoryGateRequest{
		ChannelID: "ch-1",
		Mode:      ModeText,
		CacheKey:  "upsert-hash",
		UseMemory: true,
	})
	if err != nil {
		t.Fatalf("CheckGate after upsert failed: %v", err)
	}
	if res == nil || res.Output != "second output" || res.WordCount != 200 {
		t.Errorf("unexpected cached value: %+v", res)
	}
}

func TestSaveAfterGeneration_NilRepo(t *testing.T) {
	svc := NewService(nil, zap.NewNop())
	n, err := svc.SaveAfterGeneration(context.Background(), SaveGenerationInput{
		ChannelID: "ch-1",
		Mode:      ModeText,
		Language:  "en",
		Title:     "Test",
		Prompt:    "prompt",
		Model:     "gemma",
		WordCount: 10,
		CacheKey:  "key",
	}, "text")
	if err != nil {
		t.Fatalf("SaveAfterGeneration nil repo should not error: %v", err)
	}
	if n != 0 {
		t.Errorf("SaveAfterGeneration nil repo should return 0, got %d", n)
	}
}

// ── EvictExactOutputs ─────────────────────────────────────────────────

func TestEvictExactOutputs_RemovesMatchingTitles(t *testing.T) {
	repo := newFakeMemoryGate()
	svc := NewService(repo, zap.NewNop())

	for _, title := range []string{"Keep", "Evict A", "Evict B"} {
		in := SaveGenerationInput{
			ChannelID: "ch",
			Mode:      ModeText,
			Language:  "en",
			Title:     title,
			Prompt:    "p",
			Model:     "gemma",
			WordCount: 1,
			CacheKey:  title,
		}
		if _, err := svc.SaveAfterGeneration(context.Background(), in, title); err != nil {
			t.Fatalf("save %q: %v", title, err)
		}
	}

	n, err := svc.EvictExactOutputs(context.Background(), []string{"Evict A", "Evict B"})
	if err != nil {
		t.Fatalf("EvictExactOutputs failed: %v", err)
	}
	if n != 2 {
		t.Errorf("evicted = %d, want 2", n)
	}

	for _, title := range []string{"Keep", "Evict A", "Evict B"} {
		res, err := svc.CheckGate(context.Background(), MemoryGateRequest{
			ChannelID: "ch",
			Mode:      ModeText,
			CacheKey:  title,
			UseMemory: true,
		})
		if err != nil {
			t.Fatalf("CheckGate %q: %v", title, err)
		}
		if title == "Keep" {
			if res == nil {
				t.Errorf("Keep should still be cached")
			}
		} else if res != nil {
			t.Errorf("%q should have been evicted, got %+v", title, res)
		}
	}
}

func TestEvictExactOutputs_EmptyTitles(t *testing.T) {
	svc := NewService(newFakeMemoryGate(), zap.NewNop())
	n, err := svc.EvictExactOutputs(context.Background(), nil)
	if err != nil {
		t.Errorf("EvictExactOutputs with nil titles: %v", err)
	}
	if n != 0 {
		t.Errorf("EvictExactOutputs with nil titles should return 0, got %d", n)
	}
}

// ── BuildFreshVariantPrompt ─────────────────────────────────────────────

func TestBuildFreshVariantPrompt_Passthrough(t *testing.T) {
	basePrompt := "Write a summary of topic X"
	result := BuildFreshVariantPrompt(basePrompt, nil)
	if result != basePrompt {
		t.Errorf("BuildFreshVariantPrompt should return basePrompt unchanged, got %q", result)
	}
}

func TestBuildFreshVariantPrompt_WithOutput(t *testing.T) {
	basePrompt := "Write a summary"
	out := &GenerationOutput{OutputText: "Previously generated text"}
	result := BuildFreshVariantPrompt(basePrompt, out)
	if len(result) <= len(basePrompt) || result[:len(basePrompt)] != basePrompt {
		t.Errorf("BuildFreshVariantPrompt should start with basePrompt, got %q", result)
	}
	if !containsAny(result, []string{"[FRESH_VARIANT_INSTRUCTIONS]", "PREVIOUS_RUN_AVOID_LIST"}) {
		t.Error("BuildFreshVariantPrompt should inject variant instructions when output is provided")
	}
}

func containsAny(s string, parts []string) bool {
	for _, p := range parts {
		if contains(s, p) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── Mode constants ─────────────────────────────────────────────────────

func TestModeConstants(t *testing.T) {
	if ModeText != "text" {
		t.Errorf("ModeText = %q, want \"text\"", ModeText)
	}
	if ModeClipToScript != "clip_to_script" {
		t.Errorf("ModeClipToScript = %q, want \"clip_to_script\"", ModeClipToScript)
	}
	if ModeBook != "book" {
		t.Errorf("ModeBook = %q, want \"book\"", ModeBook)
	}
}
