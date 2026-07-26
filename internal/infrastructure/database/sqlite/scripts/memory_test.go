package scripts

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestMemoryRepository_FindExactOutputScansSQLiteCreatedAtText(t *testing.T) {
	db := newMemoryTestDB(t)
	repo := NewMemoryRepository(db)

	_, err := db.Exec(`
		INSERT INTO gemma_script_outputs (
			id, channel_id, mode, language, title, prompt, normalized_input, input_hash,
			output_text, output_json, model, job_id, word_count, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"gen_test_1",
		"default",
		"clip_to_script",
		"it",
		"Test Title",
		"Test Prompt",
		"normalized-input",
		"hash-123",
		"cached output",
		`{"ok":true}`,
		"gemma4:e4b",
		"job-1",
		42,
	)
	if err != nil {
		t.Fatalf("insert cached row: %v", err)
	}

	out, err := repo.FindExactOutput(context.Background(), "default", "clip_to_script", "hash-123")
	if err != nil {
		t.Fatalf("FindExactOutput: %v", err)
	}
	if out == nil {
		t.Fatal("expected cached output, got nil")
	}
	if out.OutputText != "cached output" {
		t.Fatalf("unexpected output text: %q", out.OutputText)
	}
	if out.WordCount != 42 {
		t.Fatalf("unexpected word count: %d", out.WordCount)
	}
	if out.Model != "gemma4:e4b" {
		t.Fatalf("unexpected model: %q", out.Model)
	}
	if out.CreatedAt.IsZero() {
		t.Fatal("created_at should be populated from SQLite text")
	}
}
