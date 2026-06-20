package gemmamemory

import (
	"context"
	"database/sql"
	"testing"

	"go.uber.org/zap"
)

// ── NewRepository ───────────────────────────────────────────────────────

func TestNewRepository_WithDB(t *testing.T) {
	// Open an in-memory SQLite DB so the test is self-contained.
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Skipf("skipping: cannot open in-memory sqlite: %v", err)
	}
	defer db.Close()

	repo := NewRepository(db)
	if repo == nil {
		t.Fatal("NewRepository returned nil")
	}
	if repo.DB != db {
		t.Error("Repository.DB should be the passed-in handle")
	}
}

func TestNewRepository_NilDB(t *testing.T) {
	repo := NewRepository(nil)
	if repo == nil {
		t.Fatal("NewRepository returned nil")
	}
	if repo.DB != nil {
		t.Error("Repository.DB should be nil when nil passed")
	}
}

// ── NewService ──────────────────────────────────────────────────────────

func TestNewService_NonNil(t *testing.T) {
	repo := &Repository{DB: nil}
	svc := NewService(repo, nil)
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
	if svc.repo != repo {
		t.Error("Service.repo should be the passed-in repo")
	}
}

func TestNewService_NilLog(t *testing.T) {
	repo := &Repository{DB: nil}
	svc := NewService(repo, nil)
	// Should not panic — NewService replaces nil log with zap.NewNop().
	if svc.log == nil {
		t.Error("Service.log should be non-nil even when nil passed")
	}
	// Verify it doesn't panic on logging
	svc.log.Info("test log line")
}

func TestNewService_WithLog(t *testing.T) {
	repo := &Repository{DB: nil}
	customLog := zap.NewNop()
	svc := NewService(repo, customLog)
	if svc.log != customLog {
		t.Error("Service.log should be the passed-in logger")
	}
}

// ── CheckGate (stub) ───────────────────────────────────────────────────

func TestCheckGate_ReturnsNil(t *testing.T) {
	svc := NewService(&Repository{DB: nil}, nil)
	req := MemoryGateRequest{
		ChannelID:    "ch-1",
		Title:        "Test Title",
		Prompt:       "Write about X",
		Language:     "en",
		Mode:         ModeClipToScript,
		UseMemory:    true,
		ForceRefresh: false,
	}
	result, err := svc.CheckGate(context.Background(), req)
	if err != nil {
		t.Errorf("CheckGate should not error in stub: %v", err)
	}
	if result != nil {
		t.Error("CheckGate stub should return nil (cache miss)")
	}
}

func TestCheckGate_EmptyRequest(t *testing.T) {
	svc := NewService(&Repository{DB: nil}, nil)
	result, err := svc.CheckGate(context.Background(), MemoryGateRequest{})
	if err != nil {
		t.Errorf("CheckGate with empty request: %v", err)
	}
	if result != nil {
		t.Error("CheckGate with empty request should return nil")
	}
}

// ── SaveAfterGeneration (stub) ─────────────────────────────────────────

func TestSaveAfterGeneration_ReturnsZero(t *testing.T) {
	svc := NewService(&Repository{DB: nil}, nil)
	in := SaveGenerationInput{
		ChannelID:  "ch-1",
		Mode:       ModeClipToScript,
		Language:   "en",
		Title:      "Test",
		Prompt:     "Write about X",
		Model:      "gemma",
		OutputText: "Generated text",
		WordCount:  100,
	}
	n, err := svc.SaveAfterGeneration(context.Background(), in, "some output")
	if err != nil {
		t.Errorf("SaveAfterGeneration should not error in stub: %v", err)
	}
	if n != 0 {
		t.Errorf("SaveAfterGeneration stub should return 0, got %d", n)
	}
}

// ── BuildFreshVariantPrompt (stub) ──────────────────────────────────────

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
	if result != basePrompt {
		t.Error("BuildFreshVariantPrompt stub should return basePrompt even with output")
	}
}

// ── EvictExactOutputs (stub) ───────────────────────────────────────────

func TestEvictExactOutputs_ReturnsZero(t *testing.T) {
	svc := NewService(&Repository{DB: nil}, nil)
	n, err := svc.EvictExactOutputs(context.Background(), []string{"Title A", "Title B"})
	if err != nil {
		t.Errorf("EvictExactOutputs should not error in stub: %v", err)
	}
	if n != 0 {
		t.Errorf("EvictExactOutputs stub should return 0, got %d", n)
	}
}

func TestEvictExactOutputs_EmptyTitles(t *testing.T) {
	svc := NewService(&Repository{DB: nil}, nil)
	n, err := svc.EvictExactOutputs(context.Background(), nil)
	if err != nil {
		t.Errorf("EvictExactOutputs with nil titles: %v", err)
	}
	if n != 0 {
		t.Errorf("EvictExactOutputs with nil titles should return 0, got %d", n)
	}
}

// ── SweepAll (stub) ────────────────────────────────────────────────────

func TestSweepAll_ReturnsZero(t *testing.T) {
	repo := &Repository{DB: nil}
	n, err := repo.SweepAll(context.Background())
	if err != nil {
		t.Errorf("SweepAll should not error in stub: %v", err)
	}
	if n != 0 {
		t.Errorf("SweepAll stub should return 0, got %d", n)
	}
}

// ── Mode constants ──────────────────────────────────────────────────────

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
