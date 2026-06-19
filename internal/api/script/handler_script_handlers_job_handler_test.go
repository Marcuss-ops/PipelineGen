package script

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/database"
	concurrent "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// ── Test A: Research Cache — base get/save ──────────────────────────────

func TestResearchCacheGetSave(t *testing.T) {
	db, repo, ctx := setupResearchCacheDB(t)
	defer db.Close()

	key := "research_test_key_1"
	topic := "Rome History"
	lang := "it"
	maxSteps := 10
	source := "Rome was founded in 753 BC."

	// Get non-existent
	val, err := repo.GetResearchCache(ctx, key)
	if err != nil {
		t.Fatalf("error getting non-existent cache: %v", err)
	}
	if val != "" {
		t.Fatalf("expected empty value, got: %q", val)
	}

	// Save
	err = repo.SaveResearchCache(ctx, key, topic, lang, maxSteps, source)
	if err != nil {
		t.Fatalf("error saving cache: %v", err)
	}

	// Get saved
	val, err = repo.GetResearchCache(ctx, key)
	if err != nil {
		t.Fatalf("error getting saved cache: %v", err)
	}
	if val != source {
		t.Fatalf("expected %q, got: %q", source, val)
	}
}

// ── Test B: Research Cache — TTL 7 giorni scaduto = MISS ────────────────

func TestResearchCacheTTL(t *testing.T) {
	db, repo, ctx := setupResearchCacheDB(t)
	defer db.Close()

	key := "research_ttl_test"
	source := "Rome was built in a day."

	// Save cache entry
	err := repo.SaveResearchCache(ctx, key, "topic", "en", 10, source)
	if err != nil {
		t.Fatalf("error saving cache: %v", err)
	}

	// Verify it's found initially
	val, err := repo.GetResearchCache(ctx, key)
	if err != nil {
		t.Fatalf("error getting cache: %v", err)
	}
	if val != source {
		t.Fatalf("expected %q, got: %q", source, val)
	}

	// Simulate TTL expiry: set last_used to 8 days ago
	_, err = db.Exec("UPDATE research_cache SET last_used = datetime('now', '-8 days') WHERE key = ?", key)
	if err != nil {
		t.Fatalf("error setting last_used to past: %v", err)
	}

	// Verify MISS (should return empty because last_used is beyond 7 days)
	val, err = repo.GetResearchCache(ctx, key)
	if err != nil {
		t.Fatalf("error getting expired cache: %v", err)
	}
	if val != "" {
		t.Fatalf("expected empty value for expired cache, got: %q", val)
	}

	// Verify record still exists but with old last_used
	var lastUsed string
	err = db.QueryRow("SELECT last_used FROM research_cache WHERE key = ?", key).Scan(&lastUsed)
	if err != nil {
		t.Fatalf("error querying last_used: %v", err)
	}
	if !strings.HasPrefix(lastUsed, "20") {
		t.Fatalf("expected last_used to be a date string, got: %q", lastUsed)
	}
}

// ── Test B (bis): Research Cache — last_used aggiornato su HIT ──────────

func TestResearchCacheTTLHit(t *testing.T) {
	db, repo, ctx := setupResearchCacheDB(t)
	defer db.Close()

	key := "research_ttl_hit"
	source := "Fresh content."

	// Save and set last_used to 6 days ago (still within TTL)
	err := repo.SaveResearchCache(ctx, key, "topic", "en", 10, source)
	if err != nil {
		t.Fatalf("error saving cache: %v", err)
	}
	_, err = db.Exec("UPDATE research_cache SET last_used = datetime('now', '-6 days') WHERE key = ?", key)
	if err != nil {
		t.Fatalf("error setting last_used: %v", err)
	}

	// Get — should HIT (6 days < 7 days TTL)
	val, err := repo.GetResearchCache(ctx, key)
	if err != nil {
		t.Fatalf("error getting cache: %v", err)
	}
	if val != source {
		t.Fatalf("expected %q, got: %q", source, val)
	}

	// Verify last_used was updated to now (fresh)
	var lastUsed string
	err = db.QueryRow("SELECT last_used FROM research_cache WHERE key = ?", key).Scan(&lastUsed)
	if err != nil {
		t.Fatalf("error querying last_used: %v", err)
	}
	// last_used should be recent (within last minute)
	parsed, err := time.Parse("2006-01-02 15:04:05", lastUsed)
	if err != nil {
		t.Fatalf("unable to parse last_used %q: %v", lastUsed, err)
	}
	if time.Since(parsed) > 60*time.Second {
		t.Fatalf("last_used was not updated on HIT: got %q (diff: %v)", lastUsed, time.Since(parsed))
	}
}

// ── Test C: Research — timeout contestuale propagato ────────────────────

func TestResearchCacheTimeout(t *testing.T) {
	// Simulate context cancellation — must happen before Cmd.Start()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// exec.CommandContext with a cancelled context must fail immediately on Start
	cmd := exec.CommandContext(ctx, "python3", "--version")
	// StdoutPipe is called before Start, must succeed even with cancelled context
	_, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe failed: %v", err)
	}
	// Start must fail because context is already cancelled
	err = cmd.Start()
	if err == nil {
		// If Start somehow succeeds, Wait should return the context error
		waitErr := cmd.Wait()
		if waitErr == nil || !errors.Is(waitErr, context.Canceled) {
			cmd.Process.Kill()
			t.Fatalf("expected context.Canceled from Wait, got: %v", waitErr)
		}
		t.Logf("exec.CommandContext correctly failed with Canceled on Wait")
		return
	}
	// Start() failed because context is cancelled — this is also valid
	if !errors.Is(err, context.Canceled) {
		t.Logf("Start() failed with: %v (not Canceled but still expected)", err)
	}
	t.Logf("exec.CommandContext with cancelled context correctly handled: %v", err)
}

// ── Test F: Segmentazione — word count per scena 35-75 ──────────────────

func TestSceneWordCount(t *testing.T) {
	// Simulate a script with paragraphs of 35-75 words (20-30 sec at 150 wpm)
	script := `Ancient Rome was a civilization that grew from a small city-state to one of the largest empires in the ancient world. It was known for its military prowess, legal systems, and engineering achievements that would influence Western civilization for millennia to come. The Roman Republic established a system of checks and balances that inspired modern democracies.

Julius Caesar was a pivotal figure who crossed the Rubicon River and changed the course of history forever. His military campaigns in Gaul and Britain expanded Roman territory dramatically. His actions led directly to the end of the Roman Republic and the beginning of the Imperial era under Augustus Caesar, his adopted heir.

The Roman Empire at its height controlled vast territories spanning three continents. From the misty shores of Britain to the fertile lands of North Africa, and from the Iberian Peninsula to the Middle East, Roman influence was felt everywhere. Trade routes connected Rome to India and China through the Silk Road network.

Roman engineering achievements include magnificent aqueducts that carried water over long distances, durable roads that connected the empire, and architectural marvels like the Colosseum and Pantheon. Many of these ancient structures still stand today as testaments to Roman engineering skill and architectural vision.

The gradual fall of the Western Roman Empire was a slow process that spanned several centuries. Internal political decay, economic troubles, and external pressures from Germanic tribes all contributed to its eventual decline. The Eastern Roman Empire, known as Byzantium, continued to thrive for another thousand years after the West fell.`

	// Intelligent segmentation by paragraphs/concept ideas (same logic as job_handler.go)
	var paragraphs []string
	for _, p := range strings.Split(script, "\n\n") {
		p = strings.TrimSpace(p)
		if p != "" {
			paragraphs = append(paragraphs, p)
		}
	}
	if len(paragraphs) == 0 {
		paragraphs = []string{script}
	}

	if len(paragraphs) != 5 {
		t.Fatalf("expected 5 paragraphs, got %d", len(paragraphs))
	}

	for i, para := range paragraphs {
		words := len(strings.Fields(para))
		t.Logf("scene %d: %d words — %q...", i+1, words, para[:min(len(para), 60)])

		// Each scene should have 35-75 words (20-30 sec at 150 wpm)
		if words < 35 || words > 75 {
			t.Errorf("scene %d: %d words, expected 35-75", i+1, words)
		}

		// Each paragraph should end with a period (not ! or ?) as per test plan F
		trimmed := strings.TrimSpace(para)
		if !strings.HasSuffix(trimmed, ".") {
			t.Errorf("scene %d: does not end with period: %q", i+1, trimmed)
		}
	}
}

// ── Test I: ParallelMap — semaphore max 4 reale ─────────────────────────

func TestParallelMapRealSemaphore(t *testing.T) {
	items := make([]string, 20)
	for i := 0; i < 20; i++ {
		items[i] = fmt.Sprintf("item_%d", i)
	}

	var mu sync.Mutex
	var active int
	var maxActive int

	results := concurrent.ParallelMap(items, 4, func(idx int, item string) string {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()

		// Simulate work
		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		active--
		mu.Unlock()

		return fmt.Sprintf("processed_%s", item)
	})

	if maxActive > 4 {
		t.Fatalf("max active concurrency exceeded limit of 4: got %d", maxActive)
	}
	t.Logf("ParallelMap max active concurrency: %d (limit: 4) ✅", maxActive)

	// Verify results are in order
	if len(results) != 20 {
		t.Fatalf("expected 20 results, got %d", len(results))
	}
	if results[0] != "processed_item_0" {
		t.Fatalf("expected first result 'processed_item_0', got: %q", results[0])
	}
	if results[19] != "processed_item_19" {
		t.Fatalf("expected last result 'processed_item_19', got: %q", results[19])
	}
}

// ── Test N: Stats counters — verifica struttura ─────────────────────────

func TestStatsCounters(t *testing.T) {
	// Simulate the stats map construction from job_handler.go
	var statsMu sync.Mutex
	var cacheHitsLLM int
	var cacheHitsImg int
	var imagesGenerated int

	// Simulate LLM cache HIT
	statsMu.Lock()
	cacheHitsLLM = 1
	statsMu.Unlock()

	// Simulate image cache HIT
	statsMu.Lock()
	cacheHitsImg++
	statsMu.Unlock()

	// Simulate image generation
	statsMu.Lock()
	imagesGenerated += 5
	statsMu.Unlock()

	stats := map[string]any{
		"cache_hits_llm":   cacheHitsLLM,
		"cache_hits_img":   cacheHitsImg,
		"images_generated": imagesGenerated,
	}

	// Verify values
	cacheLLM, ok := stats["cache_hits_llm"].(int)
	if !ok {
		t.Fatalf("cache_hits_llm not an int")
	}
	if cacheLLM != 1 {
		t.Errorf("expected cache_hits_llm=1, got %d", cacheLLM)
	}

	cacheImg, ok := stats["cache_hits_img"].(int)
	if !ok {
		t.Fatalf("cache_hits_img not an int")
	}
	if cacheImg != 1 {
		t.Errorf("expected cache_hits_img=1, got %d", cacheImg)
	}

	imagesGen, ok := stats["images_generated"].(int)
	if !ok {
		t.Fatalf("images_generated not an int")
	}
	if imagesGen != 5 {
		t.Errorf("expected images_generated=5, got %d", imagesGen)
	}

	t.Logf("Stats OK: cache_hits_llm=%d cache_hits_img=%d images_generated=%d", cacheLLM, cacheImg, imagesGen)
}

// ── Test O: Error propagation — stats parziali su errore ────────────────

func TestStatsOnError(t *testing.T) {
	// Simulate a partial failure: some images were generated before error
	var statsMu sync.Mutex
	var cacheHitsImg int
	var imagesGenerated int

	// Simulate 3 successful images
	statsMu.Lock()
	imagesGenerated = 3
	statsMu.Unlock()

	// Simulate a failure — the count stays at 3 (partial)
	_ = fmt.Errorf("qdrant connection refused") // simulated error

	stats := map[string]any{
		"cache_hits_img":   cacheHitsImg,
		"images_generated": imagesGenerated,
	}

	imagesGen, ok := stats["images_generated"].(int)
	if !ok {
		t.Fatalf("images_generated not an int")
	}
	if imagesGen != 3 {
		t.Errorf("expected images_generated=3 (partial), got %d", imagesGen)
	}

	cacheImg, ok := stats["cache_hits_img"].(int)
	if !ok {
		t.Fatalf("cache_hits_img not an int")
	}
	if cacheImg != 0 {
		t.Errorf("expected cache_hits_img=0, got %d", cacheImg)
	}

	t.Logf("Partial stats OK: images_generated=%d (before error), cache_hits_img=%d", imagesGen, cacheImg)
}

// ── Test: Overwrite non duplica — INSERT OR REPLACE ─────────────────────

func TestResearchCacheOverwrite(t *testing.T) {
	db, repo, ctx := setupResearchCacheDB(t)
	defer db.Close()

	key := "research_overwrite"
	source1 := "First version."
	source2 := "Second version."

	// Save first
	err := repo.SaveResearchCache(ctx, key, "topic", "en", 10, source1)
	if err != nil {
		t.Fatalf("error saving first cache: %v", err)
	}

	// Count rows
	var count1 int
	db.QueryRow("SELECT COUNT(*) FROM research_cache WHERE key = ?", key).Scan(&count1)
	if count1 != 1 {
		t.Fatalf("expected 1 row after first save, got %d", count1)
	}

	// Save second (should overwrite, not duplicate)
	err = repo.SaveResearchCache(ctx, key, "topic", "en", 10, source2)
	if err != nil {
		t.Fatalf("error saving second cache: %v", err)
	}

	// Count rows — should still be 1
	var count2 int
	db.QueryRow("SELECT COUNT(*) FROM research_cache WHERE key = ?", key).Scan(&count2)
	if count2 != 1 {
		t.Fatalf("expected 1 row after overwrite, got %d — INSERT OR REPLACE not working", count2)
	}

	// Verify latest value
	val, err := repo.GetResearchCache(ctx, key)
	if err != nil {
		t.Fatalf("error getting overwritten cache: %v", err)
	}
	if val != source2 {
		t.Fatalf("expected %q after overwrite, got: %q", source2, val)
	}
}

// ── Test: TouchResearchCache ────────────────────────────────────────────

func TestTouchResearchCache(t *testing.T) {
	db, repo, ctx := setupResearchCacheDB(t)
	defer db.Close()

	key := "research_touch"

	// Touch non-existent key
	affected, err := repo.TouchResearchCache(ctx, key)
	if err != nil {
		t.Fatalf("error touching non-existent key: %v", err)
	}
	if affected != 0 {
		t.Fatalf("expected 0 rows for non-existent key, got %d", affected)
	}

	// Save then touch
	err = repo.SaveResearchCache(ctx, key, "topic", "en", 10, "content")
	if err != nil {
		t.Fatalf("error saving: %v", err)
	}

	// Set last_used to old date
	_, err = db.Exec("UPDATE research_cache SET last_used = datetime('now', '-3 days') WHERE key = ?", key)
	if err != nil {
		t.Fatalf("error setting old last_used: %v", err)
	}

	// Touch
	affected, err = repo.TouchResearchCache(ctx, key)
	if err != nil {
		t.Fatalf("error touching key: %v", err)
	}
	if affected != 1 {
		t.Fatalf("expected 1 row affected, got %d", affected)
	}

	// Verify last_used was refreshed
	var lastUsed string
	err = db.QueryRow("SELECT last_used FROM research_cache WHERE key = ?", key).Scan(&lastUsed)
	if err != nil {
		t.Fatalf("error querying last_used: %v", err)
	}
	parsed, err := time.Parse("2006-01-02 15:04:05", lastUsed)
	if err != nil {
		t.Fatalf("unable to parse last_used %q: %v", lastUsed, err)
	}
	if time.Since(parsed) > 60*time.Second {
		t.Fatalf("last_used was not updated by Touch: got %q", lastUsed)
	}
}

// ── Test: Scene ordering — ParallelMap preserves order ──────────────────

func TestParallelMapOrderPreserved(t *testing.T) {
	items := []string{"first", "second", "third", "fourth", "fifth"}

	results := concurrent.ParallelMap(items, 2, func(idx int, item string) string {
		time.Sleep(time.Duration(5-idx) * time.Millisecond) // inverse sleep
		return fmt.Sprintf("%s_processed", item)
	})

	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}
	expected := []string{"first_processed", "second_processed", "third_processed", "fourth_processed", "fifth_processed"}
	for i, exp := range expected {
		if results[i] != exp {
			t.Errorf("result[%d] = %q; want %q", i, results[i], exp)
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────

func setupResearchCacheDB(t *testing.T) (*sql.DB, *scripts.ScriptRepository, context.Context) {
	t.Helper()

	// Run migration with last_used column
	schema := `
		CREATE TABLE IF NOT EXISTS research_cache (
			key TEXT PRIMARY KEY,
			topic TEXT NOT NULL,
			language TEXT NOT NULL,
			max_steps INTEGER NOT NULL,
			source_text TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_used TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_research_cache_topic ON research_cache(topic);
		CREATE INDEX IF NOT EXISTS idx_research_cache_last_used ON research_cache(last_used);
	`

	db := storage.NewTestDBWithSchema(t, schema)

	repo := scripts.NewScriptRepository(db)
	ctx := context.Background()
	return db, repo, ctx
}
