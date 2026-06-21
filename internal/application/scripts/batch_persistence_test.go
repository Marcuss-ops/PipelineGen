package scripts

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

// testRepo is a minimal ScriptRepository backed by a *sql.DB for
// batch persistence tests. It implements the methods exercised by
// saveBatchScript: SaveScript, SaveOutlineSections, SaveResearchSources,
// SaveGenerationLog, NextVersionForTopic. Remaining methods are no-ops.
type testRepo struct {
	db *sql.DB
}

var _ ScriptRepository = (*testRepo)(nil)

func newTestRepo(db *sql.DB) *testRepo { return &testRepo{db: db} }

func (r *testRepo) SaveScript(ctx context.Context, rec *ScriptRecord, sections []ScriptSectionRecord, matches []ScriptStockMatchRecord) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO scripts (topic, title, duration, language, mode, tone, target_words, final_word_count, status, full_document, model_used, version) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		rec.Topic, rec.Title, rec.Duration, rec.Language, rec.Mode, rec.Tone, rec.TargetWords, rec.FinalWordCount, rec.Status, rec.FullDocument, rec.ModelUsed, rec.Version,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, s := range sections {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO script_sections (script_id, section_type, section_title, content, sort_order, word_count, status) VALUES (?,?,?,?,?,?,?)`,
			id, s.SectionType, s.SectionTitle, s.Content, s.SortOrder, s.WordCount, s.Status,
		); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

func (r *testRepo) SaveOutlineSections(ctx context.Context, scriptID int64, sections []ScriptOutlineSectionRecord) error {
	for _, s := range sections {
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO script_outline_sections (script_id, section_index, title, purpose, target_words, key_points_json, emotional_role) VALUES (?,?,?,?,?,?,?)`,
			scriptID, s.SectionIndex, s.Title, s.Purpose, s.TargetWords, s.KeyPointsJSON, s.EmotionalRole,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *testRepo) SaveResearchSources(ctx context.Context, scriptID int64, sources []ScriptResearchSource) error {
	for _, s := range sources {
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO script_research_sources (script_id, query, url, title, snippet, used_in_sections, source_type) VALUES (?,?,?,?,?,?,?)`,
			scriptID, s.Query, s.URL, s.Title, s.Snippet, s.UsedInSections, s.SourceType,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *testRepo) SaveGenerationLog(ctx context.Context, log ScriptGenerationLog) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO script_generation_logs (script_id, phase, model, input_words, output_words, duration_ms, retry_count, cache_status, prompt_hash, error) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		log.ScriptID, log.Phase, log.Model, log.InputWords, log.OutputWords, log.DurationMs, log.RetryCount, log.CacheStatus, "", log.Error,
	)
	return err
}

func (r *testRepo) NextVersionForTopic(ctx context.Context, topic, language, mode string) (int, error) {
	return 1, nil
}

func (r *testRepo) UpdateScriptFinalContent(ctx context.Context, id int64, text string, wordCount int, status, metadata, model, baseURL string, version int) error {
	return nil
}

func (r *testRepo) GetSectionByID(ctx context.Context, sectionID int64) (*ScriptSectionRecord, error) {
	return nil, nil
}

func (r *testRepo) GetScriptByID(id int64) (*ScriptRecord, []ScriptSectionRecord, []ScriptStockMatchRecord, error) {
	return nil, nil, nil, nil
}

func (r *testRepo) GetAdjacentSections(ctx context.Context, scriptID int64, sortOrder int) (*ScriptSectionRecord, *ScriptSectionRecord, error) {
	return nil, nil, nil
}

func (r *testRepo) UpdateSectionContent(ctx context.Context, sectionID int64, content string) error {
	return nil
}

func (r *testRepo) ListScripts(ctx context.Context, filter ScriptListFilter) ([]*ScriptRecord, error) {
	return nil, nil
}

// ── Persistence Tests ─────────────────────────────────────────────────────

func TestSaveBatchScript_SaveToDB_SavesAllTables(t *testing.T) {
	db := drive.NewTestDBWithSchema(t, minimalTestSchema)
	defer db.Close()

	repo := newTestRepo(db)
	svc := &BatchService{
		scriptsRepo: repo,
		log:         zap.NewNop(),
	}

	req := &GenerateBatchRequest{
		DocTitle:  "Test Book",
		Language:  "en",
		Tone:      "professional",
		Duration:  600,
		SaveToDB:  true,
		NoChapters: false,
	}

	rec := &batchDBRecord{
		docTitle:     "Test Book",
		mergedScript: "This is the full script content for the test book. It contains multiple sentences to ensure a reasonable word count.",
		sections: []ScriptSectionRecord{
			{SectionType: "item", SectionTitle: "Chapter 1", Content: "Chapter one content.", SortOrder: 1, WordCount: 4, Status: "completed"},
			{SectionType: "item", SectionTitle: "Chapter 2", Content: "Chapter two content.", SortOrder: 2, WordCount: 4, Status: "completed"},
		},
		outlineSections: []ScriptOutlineSectionRecord{
			{SectionIndex: 1, Title: "Chapter 1", Purpose: "introduce", TargetWords: 900, KeyPointsJSON: "[]", EmotionalRole: "curiosity"},
			{SectionIndex: 2, Title: "Chapter 2", Purpose: "deepen", TargetWords: 900, KeyPointsJSON: "[]", EmotionalRole: "confidence"},
		},
		generationLogs: []ScriptGenerationLog{
			{Phase: "generate_Chapter 1", Model: "test-model", OutputWords: 100, DurationMs: 500, RetryCount: 0, CacheStatus: "miss"},
			{Phase: "generate_Chapter 2", Model: "test-model", OutputWords: 100, DurationMs: 500, RetryCount: 0, CacheStatus: "miss"},
		},
		targetWords: 1400,
		noChapters:  false,
	}

	scriptID := svc.saveBatchScript(context.Background(), req, rec, nil)
	require.Greater(t, scriptID, int64(0), "expected positive script_id")

	// Verify scripts row
	var title, language, status string
	var targetWords, finalWordCount int
	err := db.QueryRow("SELECT title, language, target_words, final_word_count, status FROM scripts WHERE id = ?", scriptID).Scan(&title, &language, &targetWords, &finalWordCount, &status)
	require.NoError(t, err)
	assert.Equal(t, "Test Book", title)
	assert.Equal(t, "en", language)
	assert.Equal(t, 1400, targetWords)
	assert.Greater(t, finalWordCount, 0)
	assert.Equal(t, "completed", status)

	// Verify script content was stored
	var fullDoc string
	err = db.QueryRow("SELECT full_document FROM scripts WHERE id = ?", scriptID).Scan(&fullDoc)
	require.NoError(t, err)
	assert.Contains(t, fullDoc, "full script content")

	// Verify script_sections
	sectionCount := drive.CountRows(t, db, "SELECT COUNT(*) FROM script_sections WHERE script_id = ?", scriptID)
	assert.Equal(t, 2, sectionCount)

	var secWordCount int
	var secStatus string
	err = db.QueryRow("SELECT word_count, status FROM script_sections WHERE script_id = ? LIMIT 1", scriptID).Scan(&secWordCount, &secStatus)
	require.NoError(t, err)
	assert.Greater(t, secWordCount, 0)
	assert.Equal(t, "completed", secStatus)

	// Verify script_outline_sections
	outlineCount := drive.CountRows(t, db, "SELECT COUNT(*) FROM script_outline_sections WHERE script_id = ?", scriptID)
	assert.Equal(t, 2, outlineCount)

	// Verify script_generation_logs
	logCount := drive.CountRows(t, db, "SELECT COUNT(*) FROM script_generation_logs WHERE script_id = ?", scriptID)
	assert.Equal(t, 2, logCount)
}

func TestSaveBatchScript_SaveToDBFalse_SkipsPersistence(t *testing.T) {
	db := drive.NewTestDBWithSchema(t, minimalTestSchema)
	defer db.Close()

	repo := newTestRepo(db)
	svc := &BatchService{
		scriptsRepo: repo,
		log:         zap.NewNop(),
	}

	req := &GenerateBatchRequest{
		DocTitle: "Skipped Book",
		SaveToDB: false,
	}

	rec := &batchDBRecord{
		docTitle:     "Skipped Book",
		mergedScript: "Some content.",
		sections: []ScriptSectionRecord{
			{SectionType: "item", SectionTitle: "Ch", Content: "Content.", SortOrder: 1, WordCount: 1, Status: "completed"},
		},
	}

	scriptID := svc.saveBatchScript(context.Background(), req, rec, nil)
	assert.Equal(t, int64(0), scriptID, "should return 0 when SaveToDB is false")

	// Verify zero writes
	totalScripts := drive.CountRows(t, db, "SELECT COUNT(*) FROM scripts")
	assert.Equal(t, 0, totalScripts)

	totalSections := drive.CountRows(t, db, "SELECT COUNT(*) FROM script_sections")
	assert.Equal(t, 0, totalSections)

	totalOutlines := drive.CountRows(t, db, "SELECT COUNT(*) FROM script_outline_sections")
	assert.Equal(t, 0, totalOutlines)
}

func TestSaveBatchScript_NoChapters_DoesNotFail(t *testing.T) {
	db := drive.NewTestDBWithSchema(t, minimalTestSchema)
	defer db.Close()

	repo := newTestRepo(db)
	svc := &BatchService{
		scriptsRepo: repo,
		log:         zap.NewNop(),
	}

	req := &GenerateBatchRequest{
		DocTitle:   "No Chapters Book",
		Language:   "it",
		SaveToDB:   true,
		NoChapters: true,
	}

	rec := &batchDBRecord{
		docTitle:     "No Chapters Book",
		mergedScript: "Un singolo testo fluido senza capitoli.",
		sections: []ScriptSectionRecord{
			{SectionType: "item", SectionTitle: "Topic A", Content: "Contenuto fluido.", SortOrder: 1, WordCount: 3, Status: "completed"},
		},
		outlineSections: []ScriptOutlineSectionRecord{
			{SectionIndex: 1, Title: "Topic A", Purpose: "spiegare", TargetWords: 1800, KeyPointsJSON: "[]", EmotionalRole: "neutral"},
		},
		generationLogs: []ScriptGenerationLog{
			{Phase: "generate_Topic A", Model: "test-model", OutputWords: 50, DurationMs: 300},
		},
		targetWords: 1800,
		noChapters:  true,
	}

	scriptID := svc.saveBatchScript(context.Background(), req, rec, nil)
	assert.Greater(t, scriptID, int64(0), "should save even with NoChapters")

	// Verify language was recorded
	var recordedLang string
	err := db.QueryRow("SELECT language FROM scripts WHERE id = ?", scriptID).Scan(&recordedLang)
	require.NoError(t, err)
	assert.Equal(t, "it", recordedLang)

	// Verify one section exists
	sectionCount := drive.CountRows(t, db, "SELECT COUNT(*) FROM script_sections WHERE script_id = ?", scriptID)
	assert.Equal(t, 1, sectionCount)

	// Verify outline exists
	outlineCount := drive.CountRows(t, db, "SELECT COUNT(*) FROM script_outline_sections WHERE script_id = ?", scriptID)
	assert.Equal(t, 1, outlineCount)

	// Title should be correct
	var title string
	err = db.QueryRow("SELECT title FROM scripts WHERE id = ?", scriptID).Scan(&title)
	require.NoError(t, err)
	assert.Equal(t, "No Chapters Book", title)
}

func TestSaveBatchScript_ScriptsRepoNil_SkipsPersistence(t *testing.T) {
	db := drive.NewTestDBWithSchema(t, minimalTestSchema)
	defer db.Close()

	svc := &BatchService{
		scriptsRepo: nil,
		log:         zap.NewNop(),
	}

	req := &GenerateBatchRequest{
		DocTitle: "Nil Repo Book",
		SaveToDB: true,
	}

	rec := &batchDBRecord{
		docTitle:     "Nil Repo Book",
		mergedScript: "Content.",
	}

	scriptID := svc.saveBatchScript(context.Background(), req, rec, nil)
	assert.Equal(t, int64(0), scriptID, "should return 0 when scriptsRepo is nil")
}

func TestSaveBatchScript_SavesResearchSources(t *testing.T) {
	db := drive.NewTestDBWithSchema(t, minimalTestSchema)
	defer db.Close()

	repo := newTestRepo(db)
	svc := &BatchService{
		scriptsRepo: repo,
		log:         zap.NewNop(),
	}

	req := &GenerateBatchRequest{
		DocTitle: "Research Book",
		SaveToDB: true,
	}

	rec := &batchDBRecord{
		docTitle:     "Research Book",
		mergedScript: "Script with research.",
		sections: []ScriptSectionRecord{
			{SectionType: "item", SectionTitle: "Ch1", Content: "Content.", SortOrder: 1, WordCount: 1, Status: "completed"},
		},
	}

	sources := []ScriptResearchSource{
		{Query: "test query", URL: "https://example.com", Title: "Example", Snippet: "A snippet", SourceType: "web"},
		{Query: "another query", URL: "https://example.org", Title: "Example 2", Snippet: "Another", SourceType: "web"},
	}

	scriptID := svc.saveBatchScript(context.Background(), req, rec, sources)
	require.Greater(t, scriptID, int64(0))

	srcCount := drive.CountRows(t, db, "SELECT COUNT(*) FROM script_research_sources WHERE script_id = ?", scriptID)
	assert.Equal(t, 2, srcCount)

	// Verify research source content
	var query, url string
	err := db.QueryRow("SELECT query, url FROM script_research_sources WHERE script_id = ? LIMIT 1", scriptID).Scan(&query, &url)
	require.NoError(t, err)
	assert.Equal(t, "test query", query)
	assert.Equal(t, "https://example.com", url)
}

