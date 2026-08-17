package prompts

import (
	"strings"
	"testing"
)

// TestEntityExtractionPrompt_ConsolidatesEntitiesPhrasesWordsIntoSinglePass
// certifies the SceneAnalysis consolidation contract: a single extraction
// prompt requests named entities, important phrases, and important words (plus
// visual subjects and Artlist phrases) in ONE model call. There is no separate
// prompt for entities, phrases, or words — splitting them would be a regression
// to three independent LLM requests for the same scene text.
func TestEntityExtractionPrompt_ConsolidatesEntitiesPhrasesWordsIntoSinglePass(t *testing.T) {
	const text = "NASA launched the Genesis mission to collect solar wind samples."
	prompt := BuildEntityExtractionPrompt(text, 5)

	for _, section := range []string{
		"## frasi_importanti",  // important phrases
		"## nomi_speciali",     // named entities
		"## parole_importanti", // important words
		"## entity_senza_testo",
		"## artlist_phrases",
	} {
		if !strings.Contains(prompt, section) {
			t.Fatalf("single-pass extraction prompt must request %q (got %d-byte prompt)", section, len(prompt))
		}
	}
}

// TestEntityExtractionBatchPrompt_ConsolidatesPerSceneSections certifies the
// bounded multi-scene variant keeps the same single-pass contract: one call
// serves several scenes, each carrying the full entity+phrase+word section set.
func TestEntityExtractionBatchPrompt_ConsolidatesPerSceneSections(t *testing.T) {
	prompt := BuildEntityExtractionBatchPrompt([]string{"scene zero", "scene one"}, 5)

	for _, section := range []string{
		"## frasi_importanti",
		"## nomi_speciali",
		"## parole_importanti",
		"## entity_senza_testo",
		"## artlist_phrases",
	} {
		if !strings.Contains(prompt, section) {
			t.Fatalf("batch extraction prompt must request %q once per segment", section)
		}
	}
	if strings.Count(prompt, "SEGMENT_INPUT_") != 2 {
		t.Fatalf("batch prompt must address both segments, got:\n%s", prompt)
	}
}
