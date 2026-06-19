// Deprecated: use pkg/textutil instead.
// This file delegates to the canonical implementation in pkg/textutil/.
package platform

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Deprecated: use pkg/textutil.Slugify.
func Slugify(s string) string { return textutil.Slugify(s) }

// Deprecated: use pkg/textutil.SlugifyWithMax.
func SlugifyWithMax(s string, maxLen int) string { return textutil.SlugifyWithMax(s, maxLen) }

// Deprecated: use pkg/textutil.SafeName.
func SafeName(name string) string { return textutil.SafeName(name) }

// Deprecated: use pkg/textutil.SanitizeFilename.
func SanitizeFilename(name string) string { return textutil.SanitizeFilename(name) }

// Deprecated: use pkg/textutil.Truncate.
func Truncate(s string, n int) string { return textutil.Truncate(s, n) }

// Deprecated: use pkg/textutil.CountWords.
func CountWords(text string) int { return textutil.CountWords(text) }

// Deprecated: use pkg/textutil.FirstNonEmpty.
func FirstNonEmpty(values ...string) string { return textutil.FirstNonEmpty(values...) }

// Deprecated: use pkg/textutil.ContainsCI.
func ContainsCI(s, substr string) bool { return textutil.ContainsCI(s, substr) }

// Deprecated: use pkg/textutil.SplitCSV.
func SplitCSV(text string) []string { return textutil.SplitCSV(text) }

// Deprecated: use pkg/textutil.NormalizeStringSlice.
func NormalizeStringSlice(tags []string) []string { return textutil.NormalizeStringSlice(tags) }

// Deprecated: use pkg/textutil.Tokenize.
func Tokenize(text string) []string { return textutil.Tokenize(text) }

// Deprecated: use pkg/textutil.ParseVTTTimestamp.
func ParseVTTTimestamp(ts string) float64 { return textutil.ParseVTTTimestamp(ts) }

// Deprecated: use pkg/textutil.FormatSecondsToTimestamp.
func FormatSecondsToTimestamp(seconds int) string { return textutil.FormatSecondsToTimestamp(seconds) }

// Deprecated: use pkg/textutil.CleanSubtitleText.
func CleanSubtitleText(text string) string { return textutil.CleanSubtitleText(text) }

// Deprecated: use pkg/textutil.ParseTimestamp.
func ParseTimestamp(ts string) (int, error) { return textutil.ParseTimestamp(ts) }

// ── Stopwords (file I/O — not suitable for leaf pkg/) ──────────────────

var (
	stopwordsMap  map[string]bool
	stopwordsOnce sync.Once
)

func ensureStopwords() {
	stopwordsOnce.Do(func() {
		stopwordsMap = make(map[string]bool)
		loadStopwordsFromDir("config/stopwords")
	})
}

func loadStopwordsFromDir(dir string) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".txt" {
			continue
		}
		path := filepath.Join(dir, file.Name())
		loadStopwordsFile(path)
	}
}

func loadStopwordsFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		word := strings.TrimSpace(scanner.Text())
		if word != "" && !strings.HasPrefix(word, "#") {
			stopwordsMap[strings.ToLower(word)] = true
		}
	}
}

// IsStopWord checks if a term is a common stop word loaded from config files.
func IsStopWord(term string) bool {
	ensureStopwords()
	return stopwordsMap[strings.ToLower(term)]
}

// TokenizeWithStopWords removes stop words from tokenization.
func TokenizeWithStopWords(text string) []string {
	tokens := textutil.Tokenize(text)
	result := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if len(tok) >= 3 && !IsStopWord(tok) {
			result = append(result, tok)
		}
	}
	return result
}

// Deprecated: use pkg/textutil.CleanForVoiceover.
func CleanForVoiceover(text string) string { return textutil.CleanForVoiceover(text) }

// Deprecated: use pkg/textutil.SplitScriptSentences.
func SplitScriptSentences(text string) []string { return textutil.SplitScriptSentences(text) }

// Deprecated: use pkg/textutil.BuildSceneQuery.
func BuildSceneQuery(sentence, topic, style, language string) string {
	return textutil.BuildSceneQuery(sentence, topic, style, language)
}

// Deprecated: use pkg/textutil.ExtractJSONArray.
func ExtractJSONArray(s string) string { return textutil.ExtractJSONArray(s) }

// Deprecated: use pkg/textutil.Float64To32.
func Float64To32(in []float64) []float32 { return textutil.Float64To32(in) }

// UniqueStringsVar returns a deduplicated slice preserving first-occurrence order.
func UniqueStringsVar(items ...string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

// LangFullName returns the full language name for a language code.
func LangFullName(code string) string {
	names := map[string]string{
		"it": "Italian", "es": "Spanish", "fr": "French", "de": "German",
		"pt": "Portuguese", "nl": "Dutch", "pl": "Polish", "ru": "Russian",
		"ja": "Japanese", "zh": "Chinese", "ko": "Korean", "ar": "Arabic",
		"hi": "Hindi", "tr": "Turkish", "sv": "Swedish", "da": "Danish",
		"fi": "Finnish", "no": "Norwegian", "cs": "Czech", "ro": "Romanian",
		"hu": "Hungarian", "el": "Greek", "he": "Hebrew", "th": "Thai",
		"vi": "Vietnamese", "id": "Indonesian", "ms": "Malay",
	}
	if name, ok := names[code]; ok {
		return name
	}
	return code
}
