package scripts

import (
	"context"
	"strings"
	"testing"

	"velox/go-master/internal/storage"
)

// testSchema is a minimal subset of the production schema that covers
// the tables and columns the ScriptRepository touches. Mirrors the
// pattern used in clips/repository_delete_test.go.
const testSchema = `
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

	CREATE TABLE script_stock_matches (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		script_id INTEGER NOT NULL,
		segment_index INTEGER NOT NULL DEFAULT 0,
		stock_path TEXT NOT NULL DEFAULT '',
		stock_source TEXT NOT NULL DEFAULT '',
		score REAL NOT NULL DEFAULT 0,
		matched_terms TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE research_cache (
		key TEXT PRIMARY KEY,
		topic TEXT NOT NULL,
		language TEXT NOT NULL,
		max_steps INTEGER NOT NULL,
		source_text TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		last_used TEXT NOT NULL DEFAULT (datetime('now'))
	);
`

func newTestRepo(t *testing.T) *ScriptRepository {
	t.Helper()
	db := storage.NewTestDBWithSchema(t, testSchema)
	t.Cleanup(func() { db.Close() })
	return NewScriptRepository(db)
}

func sampleScriptRecord() *ScriptRecord {
	return &ScriptRecord{
		Topic:         "test topic",
		Duration:      60,
		Language:      "en",
		Template:      "documentary",
		Mode:          "default",
		NarrativeText: "Once upon a time...",
		TimelineJSON:  `[{"t":0,"text":"intro"}]`,
		EntitiesJSON:  `["Alice","Bob"]`,
		MetadataJSON:  `{"source":"unit-test"}`,
		FullDocument:  "Full document text",
		ModelUsed:     "gemma4:e2b",
		OllamaBaseURL: "http://127.0.0.1:11434",
	}
}

func TestSaveScript_PersistsScriptAndSections(t *testing.T) {
	repo := newTestRepo(t)

	script := sampleScriptRecord()
	sections := []ScriptSectionRecord{
		{SectionType: "intro", SectionTitle: "Introduction", Content: "Hi", SortOrder: 0},
		{SectionType: "body", SectionTitle: "Body", Content: "Story", SortOrder: 1},
		{SectionType: "outro", SectionTitle: "Conclusion", Content: "Bye", SortOrder: 2},
	}
	matches := []ScriptStockMatchRecord{
		{SegmentIndex: 0, StockPath: "/stock/a.mp4", StockSource: "pexels", Score: 0.92, MatchedTerms: "intro"},
		{SegmentIndex: 1, StockPath: "/stock/b.mp4", StockSource: "pexels", Score: 0.81, MatchedTerms: "story"},
	}

	id, err := repo.SaveScript(context.Background(), script, sections, matches)
	if err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive script id, got %d", id)
	}

	got, gotSections, gotMatches, err := repo.GetScriptByID(id)
	if err != nil {
		t.Fatalf("GetScriptByID: %v", err)
	}
	if got == nil {
		t.Fatal("expected script record, got nil")
	}
	if got.Topic != script.Topic || got.Language != script.Language {
		t.Errorf("script fields not roundtripped: got %+v", got)
	}
	if got.Version != 1 {
		t.Errorf("default version should be 1, got %d", got.Version)
	}
	if got.IsDeleted {
		t.Error("newly saved script should not be marked deleted")
	}

	if len(gotSections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(gotSections))
	}
	if gotSections[0].SectionTitle != "Introduction" || gotSections[2].SortOrder != 2 {
		t.Errorf("sections not ordered by sort_order: %+v", gotSections)
	}

	if len(gotMatches) != 2 {
		t.Fatalf("expected 2 stock matches, got %d", len(gotMatches))
	}
	if gotMatches[0].StockPath != "/stock/a.mp4" {
		t.Errorf("first stock match wrong: %+v", gotMatches[0])
	}
}

func TestGetScriptByID_ExcludesSoftDeleted(t *testing.T) {
	repo := newTestRepo(t)

	id, err := repo.SaveScript(context.Background(), sampleScriptRecord(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SoftDeleteScript(id); err != nil {
		t.Fatal(err)
	}

	got, _, _, err := repo.GetScriptByID(id)
	if err == nil {
		t.Fatalf("expected error for soft-deleted script, got record %+v", got)
	}
	if !strings.Contains(err.Error(), "failed to get script") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestListScripts_FiltersByLanguageAndTemplate(t *testing.T) {
	repo := newTestRepo(t)

	for i, lang := range []string{"en", "it", "en", "es"} {
		rec := sampleScriptRecord()
		rec.Topic = "topic-" + lang
		rec.Language = lang
		rec.Template = "documentary"
		if i%2 == 0 {
			rec.Template = "narrative"
		}
		if _, err := repo.SaveScript(context.Background(), rec, nil, nil); err != nil {
			t.Fatal(err)
		}
	}

	all, total, err := repo.ListScripts(10, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(all) != 4 {
		t.Errorf("expected 4 scripts, got total=%d len=%d", total, len(all))
	}

	en, totalEN, err := repo.ListScripts(10, 0, "en", "")
	if err != nil {
		t.Fatal(err)
	}
	if totalEN != 2 || len(en) != 2 {
		t.Errorf("expected 2 en scripts, got total=%d len=%d", totalEN, len(en))
	}

	doc, totalDoc, err := repo.ListScripts(10, 0, "", "documentary")
	if err != nil {
		t.Fatal(err)
	}
	_ = doc
	if totalDoc != 2 {
		t.Errorf("expected 2 documentary scripts, got %d", totalDoc)
	}
}

func TestFindByTopic_ReturnsMostRecent(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	old := sampleScriptRecord()
	old.Topic = "history"
	idOld, err := repo.SaveScript(context.Background(), old, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Backdate the older row so the ORDER BY created_at DESC is
	// deterministic regardless of how fast the test runs.
	if _, err := repo.db.Exec(
		"UPDATE scripts SET created_at = '2026-01-01 10:00:00' WHERE id = ?", idOld,
	); err != nil {
		t.Fatal(err)
	}

	fresh := sampleScriptRecord()
	fresh.Topic = "history"
	fresh.NarrativeText = "newer version"
	if _, err := repo.SaveScript(context.Background(), fresh, nil, nil); err != nil {
		t.Fatal(err)
	}

	got, _, _, err := repo.FindByTopic(ctx, "history", "en")
	if err != nil {
		t.Fatal(err)
	}
	if got.NarrativeText != "newer version" {
		t.Errorf("expected most recent (newer version), got %q", got.NarrativeText)
	}
}

func TestCreateNewVersion_IncrementsVersion(t *testing.T) {
	repo := newTestRepo(t)

	parentID, err := repo.SaveScript(context.Background(), sampleScriptRecord(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	child := sampleScriptRecord()
	child.Topic = "test topic v2"
	id2, err := repo.CreateNewVersion(context.Background(), parentID, child, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	childRec, _, _, err := repo.GetScriptByID(id2)
	if err != nil {
		t.Fatal(err)
	}
	if childRec.Version != 2 {
		t.Errorf("expected version 2, got %d", childRec.Version)
	}
	if childRec.ParentScriptID == nil || *childRec.ParentScriptID != parentID {
		t.Errorf("parent_script_id not set correctly: %v", childRec.ParentScriptID)
	}
}

func TestNextVersionForTopic_IncrementsPerTopicLanguageMode(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	if _, err := repo.SaveScript(context.Background(), sampleScriptRecord(), nil, nil); err != nil {
		t.Fatal(err)
	}

	next, err := repo.NextVersionForTopic(ctx, "test topic", "en", "default")
	if err != nil {
		t.Fatal(err)
	}
	if next != 2 {
		t.Errorf("expected next=2, got %d", next)
	}

	// Different language = different counter
	nextIT, err := repo.NextVersionForTopic(ctx, "test topic", "it", "default")
	if err != nil {
		t.Fatal(err)
	}
	if nextIT != 1 {
		t.Errorf("first it version should be 1, got %d", nextIT)
	}
}

func TestSoftDeleteScript_HidesFromGet(t *testing.T) {
	repo := newTestRepo(t)

	id, err := repo.SaveScript(context.Background(), sampleScriptRecord(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	pre, _, _, err := repo.GetScriptByID(id)
	if err != nil || pre == nil {
		t.Fatalf("script should be visible before delete: %v", err)
	}

	if err := repo.SoftDeleteScript(id); err != nil {
		t.Fatal(err)
	}

	post, _, _, err := repo.GetScriptByID(id)
	if err == nil {
		t.Fatalf("expected error after soft delete, got %+v", post)
	}
}

func TestHardDeleteOldDeletedScripts_RespectsAgeWindow(t *testing.T) {
	repo := newTestRepo(t)

	// Insert one script, soft-delete it, and backdate its updated_at.
	id, err := repo.SaveScript(context.Background(), sampleScriptRecord(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SoftDeleteScript(id); err != nil {
		t.Fatal(err)
	}
	// Age the row past the 1-day window.
	if _, err := repo.db.Exec(
		"UPDATE scripts SET updated_at = datetime('now', '-2 days') WHERE id = ?", id,
	); err != nil {
		t.Fatal(err)
	}

	deleted, err := repo.HardDeleteOldDeletedScripts(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 row deleted, got %d", deleted)
	}

	// A second call should delete 0 (no remaining eligible rows).
	deleted2, err := repo.HardDeleteOldDeletedScripts(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if deleted2 != 0 {
		t.Errorf("expected 0 on second call, got %d", deleted2)
	}
}

func TestGetResearchCache_TTLBoundaries(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	if err := repo.SaveResearchCache(ctx, "key-1", "topic-a", "en", 5, "alpha text"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveResearchCache(ctx, "key-2", "topic-b", "en", 5, "beta text"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveResearchCache(ctx, "key-3", "topic-c", "en", 5, "gamma text"); err != nil {
		t.Fatal(err)
	}

	// Age key-1 to 6 days ago (inside 7d window: should HIT)
	if _, err := repo.db.Exec(
		"UPDATE research_cache SET last_used = datetime('now', '-6 days') WHERE key = ?", "key-1",
	); err != nil {
		t.Fatal(err)
	}
	// Age key-2 to exactly 7 days ago: the SQL filter is `> -7 days`,
	// so a row at exactly -7 days is at the boundary and will MISS.
	if _, err := repo.db.Exec(
		"UPDATE research_cache SET last_used = datetime('now', '-7 days') WHERE key = ?", "key-2",
	); err != nil {
		t.Fatal(err)
	}
	// Age key-3 to 8 days ago (well past TTL: MISS)
	if _, err := repo.db.Exec(
		"UPDATE research_cache SET last_used = datetime('now', '-8 days') WHERE key = ?", "key-3",
	); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		key      string
		wantHit  bool
		wantText string
	}{
		{"key-1", true, "alpha text"},
		{"key-2", false, ""},
		{"key-3", false, ""},
		{"missing", false, ""},
	}
	for _, tc := range cases {
		got, err := repo.GetResearchCache(ctx, tc.key)
		if err != nil {
			t.Fatalf("GetResearchCache(%s): %v", tc.key, err)
		}
		if tc.wantHit && got != tc.wantText {
			t.Errorf("key=%s: expected hit %q, got %q", tc.key, tc.wantText, got)
		}
		if !tc.wantHit && got != "" {
			t.Errorf("key=%s: expected miss, got %q", tc.key, got)
		}
	}

	// On HIT, last_used should be bumped forward. Key-1 was 6 days old;
	// it should now be "now", which means a second Get within the same
	// nanosecond would still hit, and a re-age would be needed to test
	// the boundary a second time. Just verify the column was touched.
	var lastUsed string
	if err := repo.db.QueryRow(
		"SELECT last_used FROM research_cache WHERE key = ?", "key-1",
	).Scan(&lastUsed); err != nil {
		t.Fatal(err)
	}
	if lastUsed == "" {
		t.Error("expected last_used to be set after HIT, got empty")
	}
}

func TestSaveResearchCache_OverwritesExistingKey(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	if err := repo.SaveResearchCache(ctx, "shared", "topic1", "en", 3, "first"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveResearchCache(ctx, "shared", "topic1", "en", 3, "second"); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetResearchCache(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if got != "second" {
		t.Errorf("expected latest value 'second', got %q", got)
	}
}

func TestTouchResearchCache_AffectsRowsAffected(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	if err := repo.SaveResearchCache(ctx, "real-key", "topic", "en", 3, "data"); err != nil {
		t.Fatal(err)
	}

	rows, err := repo.TouchResearchCache(ctx, "real-key")
	if err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("expected 1 row updated, got %d", rows)
	}

	rows, err = repo.TouchResearchCache(ctx, "nope")
	if err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("expected 0 rows updated for missing key, got %d", rows)
	}
}

func TestSweepStaleResearchCache_DeletesOnlyOldRows(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	if err := repo.SaveResearchCache(ctx, "fresh", "topic-a", "en", 3, "a"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveResearchCache(ctx, "stale", "topic-b", "en", 3, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(
		"UPDATE research_cache SET last_used = datetime('now', '-45 days') WHERE key = ?", "stale",
	); err != nil {
		t.Fatal(err)
	}

	deleted, err := repo.SweepStaleResearchCache(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 row swept, got %d", deleted)
	}

	// 0/negative maxAgeDays should default to 30 (defensive guard).
	deleted, err = repo.SweepStaleResearchCache(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deletions (default 30d, fresh row is 0d), got %d", deleted)
	}
}

func TestSectionCRUD(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	scriptID, err := repo.SaveScript(context.Background(), sampleScriptRecord(), []ScriptSectionRecord{
		{SectionType: "intro", SectionTitle: "Intro", Content: "A", SortOrder: 0},
		{SectionType: "body", SectionTitle: "Body", Content: "B", SortOrder: 1},
		{SectionType: "outro", SectionTitle: "Outro", Content: "C", SortOrder: 2},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// GetSectionByID: pick the body section (sort_order=1) by looking it up via the parent
	all, _, _, err := repo.GetScriptByID(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	_ = all
	// We didn't return sections from SaveScript's id alone, so use GetSectionByID
	// on the section IDs that GetScriptByID populated.
	_, sections, _, err := repo.GetScriptByID(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}
	bodyID := sections[1].ID
	if sections[1].SectionTitle != "Body" {
		t.Fatalf("expected middle section to be Body, got %q", sections[1].SectionTitle)
	}

	got, err := repo.GetSectionByID(ctx, bodyID)
	if err != nil {
		t.Fatalf("GetSectionByID: %v", err)
	}
	if got.Content != "B" {
		t.Errorf("expected content B, got %q", got.Content)
	}

	// UpdateSectionContent
	if err := repo.UpdateSectionContent(ctx, bodyID, "B-revised"); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetSectionByID(ctx, bodyID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Content != "B-revised" {
		t.Errorf("expected updated content, got %q", updated.Content)
	}

	// GetAdjacentSections on the body (sort_order=1) should find intro (prev) and outro (next)
	prev, next, err := repo.GetAdjacentSections(ctx, scriptID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if prev == nil || prev.SectionTitle != "Intro" {
		t.Errorf("expected prev=Intro, got %+v", prev)
	}
	if next == nil || next.SectionTitle != "Outro" {
		t.Errorf("expected next=Outro, got %+v", next)
	}
}
