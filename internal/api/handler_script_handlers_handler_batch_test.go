package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	ollama "github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/client"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/types"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/database"
)

// minimalTestSchema is a minimal subset of the production schema covering
// all tables the batch flow touches during DB persistence.
const minimalTestSchema = `
	CREATE TABLE scripts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		topic TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		duration INTEGER NOT NULL DEFAULT 0,
		language TEXT NOT NULL DEFAULT 'en',
		template TEXT NOT NULL DEFAULT '',
		mode TEXT NOT NULL DEFAULT '',
		tone TEXT NOT NULL DEFAULT '',
		target_words INTEGER NOT NULL DEFAULT 0,
		final_word_count INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'completed',
		narrative_text TEXT,
		timeline_json TEXT,
		entities_json TEXT,
		metadata_json TEXT NOT NULL DEFAULT '{}',
		full_document TEXT,
		model_used TEXT NOT NULL DEFAULT '',
		ollama_base_url TEXT NOT NULL DEFAULT '',
		version INTEGER NOT NULL DEFAULT 1,
		parent_script_id INTEGER,
		is_deleted INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE script_sections (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		script_id INTEGER NOT NULL,
		section_type TEXT NOT NULL DEFAULT '',
		section_title TEXT NOT NULL DEFAULT '',
		content TEXT,
		sort_order INTEGER NOT NULL DEFAULT 0,
		word_count INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'completed'
	);

	CREATE TABLE script_research_sources (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		script_id INTEGER NOT NULL,
		query TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		snippet TEXT NOT NULL DEFAULT '',
		used_in_sections TEXT NOT NULL DEFAULT '[]',
		source_type TEXT NOT NULL DEFAULT 'web',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE script_outline_sections (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		script_id INTEGER NOT NULL,
		section_index INTEGER NOT NULL DEFAULT 0,
		title TEXT NOT NULL DEFAULT '',
		purpose TEXT NOT NULL DEFAULT '',
		target_words INTEGER NOT NULL DEFAULT 0,
		key_points_json TEXT NOT NULL DEFAULT '[]',
		emotional_role TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE script_generation_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		script_id INTEGER NOT NULL,
		phase TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		input_words INTEGER NOT NULL DEFAULT 0,
		output_words INTEGER NOT NULL DEFAULT 0,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		retry_count INTEGER NOT NULL DEFAULT 0,
		cache_status TEXT NOT NULL DEFAULT '',
		prompt_hash TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE script_versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		script_id INTEGER NOT NULL,
		version INTEGER NOT NULL DEFAULT 1,
		change_summary TEXT NOT NULL DEFAULT '',
		changed_by TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);
`

// newMockOllamaServer creates an httptest.Server that returns a fixed
// Ollama ChatResponse for every /api/chat request.
func newMockOllamaServer(scriptText string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		resp := ollamatypes.ChatResponse{
			Message: ollamatypes.Message{
				Role:    "assistant",
				Content: scriptText,
			},
			Done: true,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// newTestHandlerWithMockOllama creates a ScriptFlowHandler backed by a real
// ollama.Generator that talks to a mock HTTP server, and a scriptcore.Engine
// for the full generation pipeline (memory gate, generation, normalization,
// save). This replaces the legacy pattern where engine was nil and the
// handler fell back to direct generator calls.
func newTestHandlerWithMockOllama(t *testing.T, scriptText string, repo *scripts.ScriptRepository) *ScriptFlowHandler {
	t.Helper()
	srv := newMockOllamaServer(scriptText)
	t.Cleanup(srv.Close)

	c := client.NewClient(srv.URL, "test-model", 30)
	gen := ollama.NewGenerator(c)

	// Wire up a real engine so tests exercise the full pipeline
	// (memory gate, GenerateAndNormalize, length normalization,
	// DB persistence) instead of the fallback path.
	engine := scripts.NewEngine(gen, nil, repo, zap.NewNop())

	return &ScriptFlowHandler{
		generator:   gen,
		engine:      engine,
		scriptsRepo: repo,
		log:         zap.NewNop(),
	}
}

func TestExecuteBatchGeneration_SavesToDB_WithAllIntermediateTables(t *testing.T) {
	ctx := context.Background()

	// 1. Set up a real test database with all required tables.
	db := storage.NewTestDBWithSchema(t, minimalTestSchema)
	defer db.Close()
	repo := scripts.NewScriptRepository(db)

	// 2. Build handler with mock Ollama backend.
	scriptText := "This is a generated chapter about testing. It contains several sentences to ensure the word count is meaningful and the script appears substantial for the test."
	handler := newTestHandlerWithMockOllama(t, scriptText, repo)

	// 3. Create a batch request with SaveToDB=true.
	req := GenerateBatchRequest{
		BaseGenerateRequest: BaseGenerateRequest{
			Language: "en",
			Tone:     "documentary",
			Duration: 600,
			Model:    "test-model",
			SaveToDB: true,
		},
		DocTitle: "Test Batch Script",
		BatchTopics: []BatchTopic{
			{Topic: "Chapter One"},
			{Topic: "Chapter Two"},
		},
		NoChapters: false,
	}

	// 4. Execute the batch generation.
	result, err := handler.ExecuteBatchGeneration(ctx, &req, nil)

	// 5. Assert execution succeeded.
	require.NoError(t, err, "ExecuteBatchGeneration should succeed")
	require.NotEmpty(t, result.Script, "script should be present in result")

	// 6. Verify the script was saved in DB.
	var scriptID int64
	if err := db.QueryRow("SELECT id FROM scripts WHERE topic = ?", req.DocTitle).Scan(&scriptID); err != nil {
		t.Fatalf("script not found in DB: %v", err)
	}
	require.Greater(t, scriptID, int64(0), "expected positive script_id")

	// 7. Verify script record has new fields populated.
	var title, tone, status string
	var targetWords, finalWordCount int
	if err := db.QueryRow(
		"SELECT title, tone, target_words, final_word_count, status FROM scripts WHERE id = ?",
		scriptID,
	).Scan(&title, &tone, &targetWords, &finalWordCount, &status); err != nil {
		t.Fatalf("failed to read script fields: %v", err)
	}
	assert.Equal(t, req.DocTitle, title, "title should match docTitle")
	assert.Equal(t, req.Tone, tone, "tone should match request tone")
	// effectiveTargetWords uses scriptcore.CalculateTargetWords when no override is given.
	assert.Equal(t, 1400, targetWords, "target_words should be 1400 for 600s duration (ceil(600/60)*140)")
	assert.Greater(t, finalWordCount, 0, "final_word_count should be > 0")
	assert.Equal(t, "completed", status, "status should be completed")

	// 8. Verify script_sections were created.
	sectionCount := storage.CountRows(t, db, "SELECT COUNT(*) FROM script_sections WHERE script_id = ?", scriptID)
	assert.Greater(t, sectionCount, 0, "should have at least one section")

	// 9. Verify script_sections have new fields (word_count, status).
	var secWordCount int
	var secStatus string
	if err := db.QueryRow(
		"SELECT word_count, status FROM script_sections WHERE script_id = ? LIMIT 1",
		scriptID,
	).Scan(&secWordCount, &secStatus); err != nil {
		t.Fatalf("failed to read section fields: %v", err)
	}
	assert.Greater(t, secWordCount, 0, "section word_count should be > 0")
	assert.Equal(t, "completed", secStatus, "section status should be completed")

	// 10. Verify script_outline_sections were created.
	outlineCount := storage.CountRows(t, db, "SELECT COUNT(*) FROM script_outline_sections WHERE script_id = ?", scriptID)
	assert.Equal(t, len(req.BatchTopics), outlineCount, "outline_sections should match batch topic count")

	// 11. Verify script_generation_logs were created.
	logCount := storage.CountRows(t, db, "SELECT COUNT(*) FROM script_generation_logs WHERE script_id = ?", scriptID)
	assert.Greater(t, logCount, 0, "should have at least one generation log")

	// 12. Verify the script content is clean (no markdown artifacts).
	scriptContent := result.Script
	assert.False(t, strings.Contains(scriptContent, "##"), "script should not contain markdown heading markers")
	assert.False(t, strings.Contains(scriptContent, "**"), "script should not contain bold markdown")
}

func TestExecuteBatchGeneration_WithNoChapters_SavesSections(t *testing.T) {
	ctx := context.Background()

	db := storage.NewTestDBWithSchema(t, minimalTestSchema)
	defer db.Close()
	repo := scripts.NewScriptRepository(db)

	scriptText := "A single flowing script without chapter headings. It reads as one continuous narrative from start to finish."
	handler := newTestHandlerWithMockOllama(t, scriptText, repo)

	req := GenerateBatchRequest{
		BaseGenerateRequest: BaseGenerateRequest{
			Language: "it",
			Tone:     "narrative",
			Duration: 300,
			Model:    "test-model",
			SaveToDB: true,
		},
		DocTitle: "No-Chapters Test",
		BatchTopics: []BatchTopic{
			{Topic: "Topic A"},
		},
		NoChapters: true,
	}

	result, err := handler.ExecuteBatchGeneration(ctx, &req, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	var scriptID int64
	if err := db.QueryRow("SELECT id FROM scripts WHERE topic = ?", req.DocTitle).Scan(&scriptID); err != nil {
		t.Fatalf("script not found: %v", err)
	}

	sectionCount := storage.CountRows(t, db, "SELECT COUNT(*) FROM script_sections WHERE script_id = ?", scriptID)
	assert.Greater(t, sectionCount, 0, "should have sections even with no chapters")

	outlineCount := storage.CountRows(t, db, "SELECT COUNT(*) FROM script_outline_sections WHERE script_id = ?", scriptID)
	assert.Greater(t, outlineCount, 0, "should have outline sections")
}

func TestExecuteBatchGeneration_SaveToDBFalse_SkipsPersistence(t *testing.T) {
	ctx := context.Background()

	db := storage.NewTestDBWithSchema(t, minimalTestSchema)
	defer db.Close()
	repo := scripts.NewScriptRepository(db)

	scriptText := "Short text for skip test."
	handler := newTestHandlerWithMockOllama(t, scriptText, repo)

	req := GenerateBatchRequest{
		BaseGenerateRequest: BaseGenerateRequest{
			Language: "en",
			Tone:     "documentary",
			Duration: 600,
			Model:    "test-model",
			SaveToDB: false,
		},
		DocTitle: "Skip DB Test",
		BatchTopics: []BatchTopic{
			{Topic: "Only Topic"},
		},
		NoChapters: false,
	}

	result, err := handler.ExecuteBatchGeneration(ctx, &req, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	scriptCount := storage.CountRows(t, db, "SELECT COUNT(*) FROM scripts WHERE topic = ?", req.DocTitle)
	assert.Equal(t, 0, scriptCount, "should not persist script when SaveToDB=false")
}
