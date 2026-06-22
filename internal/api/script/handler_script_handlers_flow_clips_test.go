package script

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/\1"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	translations "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	_ "github.com/mattn/go-sqlite3"
)

// ── topicRelevant ─────────────────────────────────────────────────────────

func TestTopicRelevant_ExactSubstringMatch(t *testing.T) {
	// keyword "rome" appears literally in asset name "roman forum"
	if !topicRelevant("Roman Forum", "rome") {
		t.Fatal("expected true: 'rome' is substring of 'roman forum'")
	}
}

func TestTopicRelevant_MorphologicalPrefixMatch(t *testing.T) {
	// "pompeii" and "Pompei" share prefix "pom" (3 chars)
	if !topicRelevant("Pompei excavations", "pompeii") {
		t.Fatal("expected true: 'pompeii' and 'Pompei' share prefix 'pom'")
	}
}

func TestTopicRelevant_ItalianClipEnglishTopic(t *testing.T) {
	// Italian clip name "Colata lavica" has genuinely no overlap with
	// English topic "history pompeii" — no substring or prefix match.
	if topicRelevant("Colata lavica", "history pompeii") {
		t.Fatal("expected false: 'colata lavica' has no overlap with 'history pompeii'")
	}
}

func TestTopicRelevant_EmptyTopicKeywords(t *testing.T) {
	// Empty topic keywords → accept all
	if !topicRelevant("anything", "") {
		t.Fatal("expected true: empty topic keywords = accept all")
	}
}

func TestTopicRelevant_ShortKeywordSkipped(t *testing.T) {
	// Words < 4 chars are skipped (stop word safety net)
	if topicRelevant("something", "the end") {
		t.Fatal("expected false: 'the' and 'end' are < 4 chars, skipped")
	}
}

func TestTopicRelevant_ItalianClipWithVesuvio(t *testing.T) {
	// Clip name "Vesuvio eruzione" - keyword "vesuvio" (from title) matches
	// via exact substring (case-insensitive)
	if !topicRelevant("Vesuvio eruzione", "vesuvio pompeii") {
		t.Fatal("expected true: 'Vesuvio' matches 'vesuvio' exactly")
	}
}

func TestTopicRelevant_EnglishClipWithItalianTopic(t *testing.T) {
	// English clip name with Italian topic - morphological match via prefix
	// "archaeological" and "archeologico" → prefix "arc" (3 chars)
	if !topicRelevant("Archaeological dig", "archeologico romano") {
		t.Fatal("expected true: 'archaeological' and 'archeologico' share prefix 'arc'")
	}
}

func TestTopicRelevant_NoOverlap(t *testing.T) {
	// Completely unrelated
	if topicRelevant("Coffee grinding machine", "ancient rome pompeii") {
		t.Fatal("expected false: no overlap between coffee and ancient rome")
	}
}

// ── extractTopicKeywords ───────────────────────────────────────────────────

func TestExtractTopicKeywords_StopsStopWords(t *testing.T) {
	result := extractTopicKeywords("The history of Ancient Rome")
	words := splitFields(result)
	if !contains(words, "history") || !contains(words, "ancient") || !contains(words, "rome") {
		t.Fatalf("expected 'history', 'ancient', 'rome' in result, got: %q", result)
	}
}

func TestExtractTopicKeywords_ShortWordsDropped(t *testing.T) {
	result := extractTopicKeywords("A big cat and a dog")
	words := splitFields(result)
	if contains(words, "a") || contains(words, "A") {
		t.Fatalf("expected single-char 'a' removed, got: %q", result)
	}
	if len(words) > 4 {
		t.Fatalf("expected 0-4 keywords, got %d: %q", len(words), result)
	}
}

func TestExtractTopicKeywords_LongTitleTruncated(t *testing.T) {
	title := "one two three four five six seven eight nine ten"
	result := extractTopicKeywords(title)
	words := splitFields(result)
	if len(words) > 7 {
		t.Fatalf("expected max 7 keywords, got %d: %q", len(words), result)
	}
}

func TestExtractTopicKeywords_ItalianTitle(t *testing.T) {
	result := extractTopicKeywords("La storia di Pompei")
	words := splitFields(result)
	if !contains(words, "storia") || !contains(words, "pompei") {
		t.Fatalf("expected 'storia', 'pompei', got: %q", result)
	}
	if contains(words, "la") || contains(words, "di") {
		t.Fatalf("Italian stop words should be removed, got: %q", result)
	}
}

func TestExtractTopicKeywords_Empty(t *testing.T) {
	if result := extractTopicKeywords(""); result != "" {
		t.Fatalf("expected empty for empty input, got: %q", result)
	}
}

// ── contextualQuery ───────────────────────────────────────────────────────

func TestContextualQuery(t *testing.T) {
	result := contextualQuery("The history of Pompeii", "ancient lava flows")
	if !contains(splitFields(result), "history") ||
		!contains(splitFields(result), "pompeii") ||
		!contains(splitFields(result), "ancient") ||
		!contains(splitFields(result), "lava") ||
		!contains(splitFields(result), "flows") {
		t.Fatalf("expected topic+phrase combined, got: %q", result)
	}
}

func TestContextualQuery_EmptyTitle(t *testing.T) {
	result := contextualQuery("", "ancient lava flows")
	if result != "ancient lava flows" {
		t.Fatalf("expected phrase only when title empty, got: %q", result)
	}
}

func TestContextualQuery_EnglishTitleItalianPhrase(t *testing.T) {
	result := contextualQuery("The history of Pompeii", "antiche colate laviche")
	if !contains(splitFields(result), "history") ||
		!contains(splitFields(result), "pompeii") ||
		!contains(splitFields(result), "antiche") ||
		!contains(splitFields(result), "colate") {
		t.Fatalf("expected English topic + Italian phrase, got: %q", result)
	}
}

// ── extractSearchKeywords ─────────────────────────────────────────────────

func TestExtractSearchKeywords_SpecialNamesInPhrase(t *testing.T) {
	result := extractSearchKeywords("Vesuvio eruption ancient", "Pompeii", []string{"Vesuvio", "Pompei"})
	if result == "" {
		t.Fatalf("expected non-empty result")
	}
	words := splitFields(result)
	if !contains(words, "vesuvio") || !contains(words, "eruption") || !contains(words, "ancient") {
		t.Fatalf("expected 'Vesuvio', 'eruption', 'ancient' in result, got: %q", result)
	}
}

func TestExtractSearchKeywords_FallbackToPhraseWords(t *testing.T) {
	result := extractSearchKeywords("ancient lava eruption", "", nil)
	words := splitFields(result)
	if !contains(words, "ancient") || !contains(words, "lava") {
		t.Fatalf("expected content words from phrase, got: %q", result)
	}
}

func TestExtractSearchKeywords_LimitedToFour(t *testing.T) {
	result := extractSearchKeywords("one two three four five six", "", nil)
	words := splitFields(result)
	if len(words) > 4 {
		t.Fatalf("expected max 4 keywords, got %d: %q", len(words), result)
	}
}

// ── artlistSearchPhrase ────────────────────────────────────────────────────

// TestArtlistSearchPhrase_NilTranslator verifica che artlistSearchPhrase
// restituisca la frase originale quando il Translator è nil.
func TestArtlistSearchPhrase_NilTranslator(t *testing.T) {
	svc := ClipServices{Translator: nil}
	result := artlistSearchPhrase(context.Background(), svc, "antiche colate laviche")
	if result != "antiche colate laviche" {
		t.Fatalf("expected original phrase when translator nil, got: %q", result)
	}
}

// TestArtlistSearchPhrase_EmptyPhrase verifica che artlistSearchPhrase
// restituisca stringa vuota per input vuoto.
func TestArtlistSearchPhrase_EmptyPhrase(t *testing.T) {
	svc := ClipServices{Translator: nil}
	result := artlistSearchPhrase(context.Background(), svc, "")
	if result != "" {
		t.Fatalf("expected empty for empty input, got: %q", result)
	}
}

// TestArtlistSearchPhrase_CacheHit verifica che artlistSearchPhrase usi la
// traduzione dalla cache quando disponibile, senza chiamare l'LLM.
//
// Usa cache.Set() per popolare correttamente la cache (L1 + L2) attraverso
// la stessa API che il production code usa.
func TestArtlistSearchPhrase_CacheHit(t *testing.T) {
	db := newTranslationCacheDB(t)
	defer db.Close()

	cache := translations.NewCache(db)
	defer cache.Close()

	ctx := context.Background()
	italianPhrase := "antiche colate laviche"
	expectedEnglish := "ancient lava flows"

	// Popola la cache attraverso cache.Set() che usa lo stesso cacheKey
	// interno del production code (sha256 lowercase + trim).
	if err := cache.Set(ctx, italianPhrase, "english", expectedEnglish); err != nil {
		t.Fatalf("failed to pre-populate cache: %v", err)
	}

	// Crea un Client (non verrà chiamato perché la cache hit ritorna prima)
	client := ollamaclient.NewClient("http://127.0.0.1:11434", "gemma4:e4b", 30)
	gen := ollama.NewGenerator(client)
	gen.SetTranslationCache(cache)

	svc := ClipServices{
		Translator:    gen,
		MetadataModel: "gemma4:e4b",
	}

	result := artlistSearchPhrase(ctx, svc, italianPhrase)
	if result != expectedEnglish {
		t.Fatalf("expected translation %q from cache, got: %q", expectedEnglish, result)
	}
}

// TestArtlistSearchPhrase_CacheMiss verifica che artlistSearchPhrase
// restituisca la frase originale quando la cache non ha la traduzione
// e il client non è raggiungibile (TranslateTextWithModel fallisce).
func TestArtlistSearchPhrase_CacheMiss(t *testing.T) {
	db := newTranslationCacheDB(t)
	defer db.Close()

	cache := translations.NewCache(db)
	defer cache.Close()

	// Client che punta a una porta inascolto → TranslateTextWithModel fallirà
	// con errore di connessione dopo tutte le retry.
	client := ollamaclient.NewClient("http://127.0.0.1:19999", "gemma4:e4b", 1)
	gen := ollama.NewGenerator(client)
	gen.SetTranslationCache(cache)

	svc := ClipServices{
		Translator:    gen,
		MetadataModel: "gemma4:e4b",
	}

	// Cache miss + LLM failure → fallback alla frase originale
	result := artlistSearchPhrase(context.Background(), svc, "rovine romane antiche")
	if result != "rovine romane antiche" {
		t.Fatalf("expected original phrase on cache miss + LLM failure, got: %q", result)
	}
}

// ── filterSearchAssets ────────────────────────────────────────────────────

func TestFilterSearchAssets_ArtlistExemptFromTopicFilter(t *testing.T) {
	// Artlist clip con nome italiano SENZA overlap con topic keywords
	// → DEVE passare il filter (Artlist exempt)
	assets := []realtime.MatchAsset{
		{ID: "art1", Name: "Colata lavica Vesuvio", Source: "artlist", Score: 0.85, DriveLink: "https://drive.google.com/file/d/abc"},
	}
	seen := make(map[string]struct{})
	results := filterSearchAssets(assets, "history pompeii", seen, 5)
	if len(results) != 1 {
		t.Fatalf("expected 1 Artlist clip to pass filter (exempt), got %d", len(results))
	}
	if results[0].ID != "art1" {
		t.Fatalf("expected art1, got %q", results[0].ID)
	}
	if results[0].DriveLink != "https://drive.google.com/file/d/abc" {
		t.Fatalf("expected DriveLink to be preserved, got %q", results[0].DriveLink)
	}
}

func TestFilterSearchAssets_NonArtlistMatchingPasses(t *testing.T) {
	// YouTube clip con nome inglese che matcha il topic "history pompeii"
	// "history" è parola di topic contenuta in "Pompeii history documentary" → topicRelevant = true
	assets := []realtime.MatchAsset{
		{ID: "yt1", Name: "Pompeii history documentary", Source: "youtube", Score: 0.92, DriveLink: "https://drive.google.com/file/d/def"},
	}
	seen := make(map[string]struct{})
	results := filterSearchAssets(assets, "history pompeii", seen, 5)
	if len(results) != 1 {
		t.Fatalf("expected 1 matching YouTube clip to pass, got %d", len(results))
	}
}

func TestFilterSearchAssets_NonArtlistNonMatchingFiltered(t *testing.T) {
	// YouTube clip con nome italiano SENZA overlap con topic inglese
	// → DEVE essere filtrato
	assets := []realtime.MatchAsset{
		{ID: "yt1", Name: "Macchina da caffè espresso", Source: "youtube", Score: 0.95, DriveLink: "https://drive.google.com/file/d/ghi"},
	}
	seen := make(map[string]struct{})
	results := filterSearchAssets(assets, "history pompeii", seen, 5)
	if len(results) != 0 {
		t.Fatalf("expected non-matching YouTube clip to be filtered, got %d", len(results))
	}
}

func TestFilterSearchAssets_Deduplication(t *testing.T) {
	// Stessa clip Artlist appare due volte → solo una deve passare
	assets := []realtime.MatchAsset{
		{ID: "art1", Name: "Vesuvio eruzione", Source: "artlist", Score: 0.90, DriveLink: "https://drive.google.com/file/d/a"},
		{ID: "art1", Name: "Vesuvio eruzione", Source: "artlist", Score: 0.90, DriveLink: "https://drive.google.com/file/d/a"},
	}
	seen := make(map[string]struct{})
	results := filterSearchAssets(assets, "history pompeii", seen, 5)
	if len(results) != 1 {
		t.Fatalf("expected 1 deduplicated result, got %d", len(results))
	}
}

func TestFilterSearchAssets_LimitRespected(t *testing.T) {
	assets := []realtime.MatchAsset{
		{ID: "art1", Name: "Rovine romane", Source: "artlist", Score: 0.90, DriveLink: "drive.google.com/a"},
		{ID: "art2", Name: "Foro romano", Source: "artlist", Score: 0.85, DriveLink: "drive.google.com/b"},
		{ID: "art3", Name: "Colosseo", Source: "artlist", Score: 0.80, DriveLink: "drive.google.com/c"},
	}
	seen := make(map[string]struct{})
	results := filterSearchAssets(assets, "history pompeii", seen, 2)
	if len(results) != 2 {
		t.Fatalf("expected only 2 results (limit), got %d", len(results))
	}
}

func TestFilterSearchAssets_EmptyTopicKeywords(t *testing.T) {
	// Topic keywords vuoti → tutti gli asset passano (topicRelevant torna true)
	assets := []realtime.MatchAsset{
		{ID: "yt1", Name: "Coffee machine", Source: "youtube", Score: 0.70, DriveLink: ""},
		{ID: "art1", Name: "Caffè macinato", Source: "artlist", Score: 0.70, DriveLink: ""},
	}
	seen := make(map[string]struct{})
	results := filterSearchAssets(assets, "", seen, 5)
	if len(results) != 2 {
		t.Fatalf("expected all 2 assets to pass with empty topic keywords, got %d", len(results))
	}
}

func TestFilterSearchAssets_EmptyAssets(t *testing.T) {
	seen := make(map[string]struct{})
	results := filterSearchAssets(nil, "history pompeii", seen, 5)
	if len(results) != 0 {
		t.Fatalf("expected 0 results for nil assets, got %d", len(results))
	}
}

func TestFilterSearchAssets_SeenPreventsDuplicates(t *testing.T) {
	// seen map pre-popolata con ID art1 → quel asset deve essere saltato
	seen := map[string]struct{}{"art1": {}}
	assets := []realtime.MatchAsset{
		{ID: "art1", Name: "Già visto", Source: "artlist", Score: 0.90, DriveLink: ""},
		{ID: "art2", Name: "Nuovo clip", Source: "artlist", Score: 0.85, DriveLink: ""},
	}
	results := filterSearchAssets(assets, "history pompeii", seen, 5)
	if len(results) != 1 || results[0].ID != "art2" {
		t.Fatalf("expected only art2 (new), got %d results: %+v", len(results), results)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────

// newTranslationCacheDB crea un database SQLite in-memory con la tabella
// translation_cache già pronta.
func newTranslationCacheDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS translation_cache (
			cache_key TEXT PRIMARY KEY,
			source_text_hash TEXT NOT NULL,
			target_language TEXT NOT NULL,
			translated_text TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_used TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("failed to create translation_cache table: %v", err)
	}
	return db
}

func splitFields(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

func contains(words []string, target string) bool {
	for _, w := range words {
		if len(w) == len(target) {
			same := true
			for i := 0; i < len(w); i++ {
				if (w[i] | 32) != (target[i] | 32) {
					same = false
					break
				}
			}
			if same {
				return true
			}
		}
	}
	return false
}
