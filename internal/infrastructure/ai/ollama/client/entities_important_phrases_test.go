package client

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/linguistics"
)

// bootstrapTestLexicon installs the repository lexicon exactly once for this
// package, mirroring TestSanitizeEntityExtractionResult_FiltersByLanguage so
// the phrase heuristics use the real per-language stop/function-word profiles.
func bootstrapTestLexicon(t *testing.T) {
	t.Helper()
	if linguistics.DefaultLexiconOrNil() != nil {
		return
	}
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	for d := dir; d != filepath.Dir(d); d = filepath.Dir(d) {
		root := filepath.Join(d, "config", "lexicons")
		if _, err := os.Stat(root); err == nil {
			registry, err := linguistics.NewLexiconRegistry(root)
			require.NoError(t, err)
			require.NoError(t, linguistics.SetDefaultLexicon(registry))
			return
		}
	}
	t.Fatal("config/lexicons not found")
}

// TestFallbackImportantPhrases_ShortSalientNotWholeParagraph is the Test 16
// certification: the important-phrase fallback must emit short salient verbatim
// fragments ("Cody Rhodes finally confirmed", "return at WrestleMania") and
// never the whole paragraph.
func TestFallbackImportantPhrases_ShortSalientNotWholeParagraph(t *testing.T) {
	bootstrapTestLexicon(t)
	profile := linguistics.DefaultLexicon().Resolve("en")

	segment := "After months of speculation, Cody Rhodes finally confirmed that he would return at WrestleMania."
	phrases := fallbackImportantPhrases(segment, 5, profile)

	require.NotEmpty(t, phrases, "important phrases must not be empty")
	lowerSegment := strings.ToLower(segment)
	wholeLower := strings.ToLower(strings.TrimSpace(segment))

	for _, p := range phrases {
		n := len(strings.Fields(p))
		require.GreaterOrEqual(t, n, minImportantPhraseWords, "phrase %q is a single word, not a phrase", p)
		require.LessOrEqual(t, n, maxImportantPhraseWords, "phrase %q is a whole sentence/paragraph", p)
		require.Contains(t, lowerSegment, strings.ToLower(p), "phrase %q is not verbatim in the segment", p)
		require.NotEqual(t, wholeLower, strings.ToLower(p), "whole paragraph surfaced as a phrase: %q", p)
	}

	// Both salient fragments (the subject and the event) must be surfaced.
	joined := strings.ToLower(strings.Join(phrases, "\n"))
	require.Contains(t, joined, "cody rhodes", "subject fragment missing from phrases: %v", phrases)
	require.Contains(t, joined, "wrestlemania", "event fragment missing from phrases: %v", phrases)
}

// TestFilterExactPhrases_RejectsWholeParagraphAndSingleWords certifies that the
// LLM-output filter drops a whole-paragraph echo and single-word fragments, and
// only keeps short verbatim salient spans.
func TestFilterExactPhrases_RejectsWholeParagraphAndSingleWords(t *testing.T) {
	bootstrapTestLexicon(t)
	profile := linguistics.DefaultLexicon().Resolve("en")

	segment := "Cody Rhodes finally confirmed that he would return at WrestleMania."
	got := filterExactPhrases(segment, []string{
		segment,  // whole-paragraph echo → must be dropped
		"return", // single word → must be dropped
		"Cody Rhodes finally confirmed",
		"return at WrestleMania",
	}, profile)

	require.NotContains(t, got, segment, "whole paragraph was not rejected")
	require.NotContains(t, got, "return", "single word was not rejected")
	require.Contains(t, got, "Cody Rhodes finally confirmed")
	require.Contains(t, got, "return at WrestleMania")
}
