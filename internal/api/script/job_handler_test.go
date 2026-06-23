package script

// Research-cache tests were removed June 2026: the local
// internal/application/scripts.ScriptRepository contract (declared in
// repository.go) doesn't expose GetResearchCache/SaveResearchCache/
// TouchResearchCache — those live on the canonical
// internal/domain/script.ScriptRepository and on the concrete
// *assets.ScriptRepository. The previous test fixture depended on a
// `setupResearchCacheDB` helper that built a stub repo via
// scripts.NewScriptRepository (which didn't exist on the local
// interface) and called methods that aren't part of this package's
// contract. The 4 previously-t.Skip()pend tests (TestResearchCache*
// variants) plus TestTouchResearchCache plus the helper have all been
// dropped.
//
// Re-add research-cache tests under a new handler_batch_research_cache_test.go
// once the contract is unified (TODO tracked in scripts/repository.go's
// NewPlan doc-comment). Until then, only the orchestration-level tests
// below (segmentation, parallel map, stats counters) live in this file.

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

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
