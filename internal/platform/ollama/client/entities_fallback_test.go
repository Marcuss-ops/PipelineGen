package client

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

func TestFallbackSpecialNames_RecoversSingleWordProperNouns(t *testing.T) {
	// "Tikal" and "Palenque" are capitalized single words that appear
	// mid-sentence and never lowercase — the heuristic must recover them.
	text := "La civiltà Maya si sviluppò in Mesoamerica. Le città di Tikal e Palenque sorsero nella penisola dello Yucatán, con Chichén Itzá nel nord."
	names := fallbackSpecialNames(text, 10, "")

	require.Contains(t, names, "Tikal")
	require.Contains(t, names, "Palenque")
	require.Contains(t, names, "Mesoamerica")
	require.Contains(t, names, "Yucatán")
	require.Contains(t, names, "Maya")

	// Sentence-initial articles are not names (and are below the length
	// floor), so they must never be emitted.
	require.NotContains(t, names, "La")
	require.NotContains(t, names, "Le")
}

func TestFallbackSpecialNames_ExcludesSentenceInitialAndLowercaseWords(t *testing.T) {
	// A capitalized word that only starts a sentence is a common noun, not a
	// proper noun.
	sentenceInitial := "Questo concetto richiama Tikal e Palenque."
	names := fallbackSpecialNames(sentenceInitial, 10, "")
	require.Contains(t, names, "Tikal")
	require.Contains(t, names, "Palenque")
	require.NotContains(t, names, "Questo")

	// A word that also appears lowercase elsewhere is a common noun even when
	// it is capitalized in one position.
	lowercaseElsewhere := "Il nome maya compare spesso. Maya è una civiltà."
	names = fallbackSpecialNames(lowercaseElsewhere, 10, "")
	require.NotContains(t, names, "Maya")
}

func TestFallbackSpecialNames_ExcludesAllCapsAndRomanNumerals(t *testing.T) {
	// Acronyms and Roman numerals are all-caps: they are not title-case proper
	// nouns and must not be recovered as names.
	text := "Tra il III e il IX secolo Tikal dominò la regione, mentre la NASA studia le stelle."
	names := fallbackSpecialNames(text, 10, "")
	require.Contains(t, names, "Tikal")
	require.NotContains(t, names, "III")
	require.NotContains(t, names, "IX")
	require.NotContains(t, names, "NASA")
}

func TestIsSentenceStart(t *testing.T) {
	require.True(t, isSentenceStart("Tikal domina.", 0))
	require.False(t, isSentenceStart("città di Tikal.", len("città di ")))
	require.True(t, isSentenceStart("Fine. Tikal domina.", len("Fine. ")))
}

// TestSanitizeEntityExtractionResult_FiltersByLanguage installs the repository
// lexicon (a process-global) and therefore must run after the lexicon-agnostic
// tests above; it is also ordered after the client_* test files by filename.
func TestSanitizeEntityExtractionResult_FiltersByLanguage(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	for d := dir; d != filepath.Dir(d); d = filepath.Dir(d) {
		root := filepath.Join(d, "config", "lexicons")
		if _, err := os.Stat(root); err == nil {
			registry, err := linguistics.NewLexiconRegistry(root)
			require.NoError(t, err)
			require.NoError(t, linguistics.SetDefaultLexicon(registry))
			break
		}
	}

	segment := "L'astronomia Maya e la scrittura che studiamo."
	result := &detail.EntityExtractionResult{
		SegmentIndex:     0,
		FrasiImportanti:  []string{segment},
		EntitaSenzaTesto: map[string]string{},
		NomiSpeciali:     []string{},
		ParoleImportanti: []string{"astronomia", "che", "scrittura"},
		ArtlistPhrases:   []string{},
	}
	out := sanitizeEntityExtractionResult(segment, result, 5, "it")

	// "che" is an Italian stopword and must be dropped; the concrete nouns
	// survive because they occur in the segment and are not function words.
	require.NotContains(t, out.ParoleImportanti, "che")
	require.Contains(t, out.ParoleImportanti, "astronomia")
	require.Contains(t, out.ParoleImportanti, "scrittura")

	// The empty nomi_speciali list must be recovered from the capitalized word
	// "Maya" that appears mid-sentence.
	require.Contains(t, out.NomiSpeciali, "Maya")
}
