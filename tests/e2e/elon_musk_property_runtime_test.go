package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLiveElonMuskPropertyRuntime deliberately has no expected phrase list.
// The payload contains only editorial input; every phrase printed below is
// produced by the runtime extraction path and checked by output properties.
func TestLiveElonMuskPropertyRuntime(t *testing.T) {
	if os.Getenv("PIPELINEGEN_ELON_MUSK_LIVE") != "1" {
		t.Skip("set PIPELINEGEN_ELON_MUSK_LIVE=1 to run the live Elon Musk property runtime test")
	}
	token := liveAdminToken()
	if token == "" {
		t.Skip("VELOX_ADMIN_TOKEN or TOKEN_FILE is required for the live Elon Musk runtime test")
	}
	body := readElonMuskRuntimeFixture(t)
	assertNoPhraseOracle(t, body)

	baseURL := strings.TrimRight(getenv("VELOX_API_BASE_URL", "http://127.0.0.1:8000"), "/")
	client := &http.Client{}
	jobID := submitMikeTysonRuntime(t, client, baseURL, token, body)
	full := waitForMikeTysonRuntime(t, client, baseURL, token, jobID)
	result := generationResult(full)
	if result == nil {
		t.Fatalf("job %s completed without job.result.result", jobID)
	}
	verifyElonMuskPropertyResult(t, result)
	t.Logf("runtime PASS: job=%s word_count=%d", jobID, integerAt(mapAt(result, "output"), "word_count"))
}

func readElonMuskRuntimeFixture(t *testing.T) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while resolving Elon Musk fixture")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "ops", "jobs", "elon_musk_1000w_property.generate.json"))
	if err != nil {
		t.Fatalf("read Elon Musk runtime fixture: %v", err)
	}
	if !json.Valid(body) {
		t.Fatal("Elon Musk runtime fixture is not valid JSON")
	}
	return body
}

func assertNoPhraseOracle(t *testing.T, body []byte) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode Elon Musk fixture: %v", err)
	}
	if containsJSONKey(payload, "important_phrases") {
		t.Fatal("Elon Musk runtime fixture contains important_phrases input; this test must not provide a phrase oracle")
	}
}

func containsJSONKey(value any, key string) bool {
	switch typed := value.(type) {
	case map[string]any:
		if _, exists := typed[key]; exists {
			return true
		}
		for _, child := range typed {
			if containsJSONKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsJSONKey(child, key) {
				return true
			}
		}
	}
	return false
}

func verifyElonMuskPropertyResult(t *testing.T, result map[string]any) {
	t.Helper()
	wordCount := integerAt(mapAt(result, "output"), "word_count")
	if wordCount < 850 || wordCount > 1200 {
		t.Fatalf("generated word_count=%d, want approximately 1000", wordCount)
	}
	scenes := mapsAt(result, "scenes")
	if len(scenes) != 5 {
		t.Fatalf("generated scenes=%d, want five", len(scenes))
	}
	languages := []string{"en", "it", "de", "fr", "es", "pt-BR", "pl", "ru", "tr", "id"}
	counts := make(map[string]int, len(languages))
	for _, language := range languages {
		for _, scene := range scenes {
			text := stringAt(mapAt(scene, "text"), language)
			if text == "" {
				t.Fatalf("scene %q has no %s text", stringAt(scene, "id"), language)
			}
			annotations := mapAt(mapAt(scene, "annotations_by_language"), language)
			if annotations == nil && language == "en" {
				annotations = mapAt(scene, "annotations")
			}
			if annotations == nil {
				continue
			}
			entities := append(stringValues(valueAt(annotations, "primary_entities"), "text"), stringValues(valueAt(annotations, "secondary_entities"), "text")...)
			for _, phrase := range stringValues(valueAt(annotations, "important_phrases"), "text") {
				if !strings.Contains(text, phrase) {
					t.Fatalf("%s/%s phrase %q is not grounded in its scene text", language, stringAt(scene, "id"), phrase)
				}
				if words := len(strings.Fields(phrase)); words < 2 || words > 4 {
					t.Fatalf("%s/%s phrase %q has %d words, want 2..4", language, stringAt(scene, "id"), phrase, words)
				}
				for _, entity := range entities {
					if phraseOverlapsSurface(text, phrase, entity) {
						t.Fatalf("%s/%s phrase %q overlaps entity %q", language, stringAt(scene, "id"), phrase, entity)
					}
				}
				counts[language]++
				t.Logf("extracted language=%s scene=%s phrase=%q", language, stringAt(scene, "id"), phrase)
			}
		}
		if counts[language] == 0 {
			t.Fatalf("language %s produced zero phrases", language)
		}
	}
	t.Logf("extracted phrase counts by language: %v", counts)
	documents := mapAt(result, "documents")
	if len(documents) != len(languages) {
		t.Fatalf("localized documents=%d, want %d", len(documents), len(languages))
	}
	for _, language := range languages {
		link := stringAt(mapAt(documents, language), "link")
		if link == "" {
			t.Fatalf("%s Google Doc link is missing", language)
		}
		t.Logf("google doc language=%s link=%s", language, link)
	}
}
