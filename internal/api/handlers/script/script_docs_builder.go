package script

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/clips"
	artlistSvc "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/association"
	imgservice "velox/go-master/internal/service/images"
)

// BuildScriptDocument generates the modular script document using Ollama and the local catalogs.
func BuildScriptDocument(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, dataDir, pythonScriptsDir, nodeScraperDir string, StockDriveRepo, ArtlistRepo, ClipsRepo *clips.Repository, artlistService *artlistSvc.Service, imgService *imgservice.Service, assocService *association.Service) (*ScriptDocument, error) {
	req.Normalize()

	if gen == nil || gen.GetClient() == nil {
		return nil, fmt.Errorf("ollama generator not initialized")
	}

	sourceText := strings.TrimSpace(req.SourceText)
	if sourceText == "" {
		sourceText = req.Topic
	}

	generated, err := gen.GenerateScript(ctx, types.TextGenerationRequest{
		Title:      req.Topic,
		SourceText: sourceText,
		Language:   req.Language,
		Duration:   req.Duration,
		Tone:       req.Template,
		Model:      "",
		Options:    map[string]interface{}{},
	})
	if err != nil {
		return nil, fmt.Errorf("ollama script generation failed: %w", err)
	}

	narrative := strings.TrimSpace(generated.Script)
	if narrative == "" {
		narrative = strings.TrimSpace(sourceText)
	}
	if narrative == "" {
		narrative = req.Topic
	}
	// cleanNarrativeBody now relies on the general CleanScript logic but we can add document-specific cleaning here
	narrative = types.CleanScript(narrative)

	timeline, _ := BuildTimelinePlan(ctx, gen, req, dataDir, nodeScraperDir, sourceText, narrative, StockDriveRepo, ArtlistRepo, ClipsRepo, artlistService, assocService)

	// Build image section (always include, use default if service unavailable)
	var imageSection ScriptSection
	if imgService != nil {
		imageSection = buildImagePlanningSection(req, narrative, nil, ScriptSection{}, ScriptSection{}, ScriptSection{}, pythonScriptsDir, imgService)
	} else {
		imageSection = ScriptSection{
			Title: "📸 Entità con Immagine",
			Body:  "Servizio immagini non disponibile.",
		}
	}

	// Extract end sections
	phrases := extractImportantPhrases(narrative)
	specialNames := extractSpecialNames(narrative)
	importantWords := extractImportantWords(narrative, 10)

	importantPhrasesSection := ScriptSection{
		Title: "📢 IMPORTANT PHRASES",
		Body:  renderImportantPhrases(phrases),
	}
	specialNamesSection := ScriptSection{
		Title: "⭐ SPECIAL NAMES",
		Body:  renderSpecialNames(specialNames),
	}
	importantWordsSection := ScriptSection{
		Title: "🗝️ IMPORTANT WORDS",
		Body:  renderImportantWords(importantWords),
	}

	sections := []ScriptSection{
		{Title: "🧾 Metadata", Body: renderMetadata(req)},
		{Title: types.MarkerNarrator, Body: narrative},
		{Title: types.MarkerTimeline, Body: RenderTimeline(timeline)},
		imageSection,
		importantPhrasesSection,
		specialNamesSection,
		importantWordsSection,
	}

	content := renderScriptDocument(req.Topic, sections)
	return &ScriptDocument{
		Title:    req.Topic,
		Content:  content,
		Sections: sections,
		Timeline: timeline,
	}, nil
}

func renderMetadata(req ScriptDocsRequest) string {
	var b strings.Builder
	b.WriteString("Topic: ")
	b.WriteString(req.Topic)
	b.WriteString("\nDuration: ")
	b.WriteString(fmt.Sprintf("%d seconds", req.Duration))
	b.WriteString("\nLanguage: ")
	b.WriteString(req.Language)
	b.WriteString("\nTemplate: ")
	b.WriteString(req.Template)
	b.WriteString("\nMode: modular")
	return b.String()
}

// extractImportantPhrases splits narrative into sentences, returns up to 10 unique phrases
func extractImportantPhrases(narrative string) []string {
	sentences := strings.FieldsFunc(narrative, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	})
	var phrases []string
	seen := make(map[string]struct{})
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		s = strings.TrimRight(s, ".!?")
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		phrases = append(phrases, s)
		if len(phrases) >= 10 {
			break
		}
	}
	return phrases
}

// renderImportantPhrases formats phrases with ✨ prefix
func renderImportantPhrases(phrases []string) string {
	if len(phrases) == 0 {
		return "Nessuna frase importante rilevata."
	}
	var b strings.Builder
	for _, p := range phrases {
		b.WriteString("   ✨ \"")
		b.WriteString(p)
		b.WriteString("\"\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// extractSpecialNames finds proper nouns (start with uppercase) in narrative
// Filters out sentence-start words that are common words, not names
func extractSpecialNames(narrative string) []string {
	// Common sentence starters that are not proper nouns
	sentenceStarters := map[string]struct{}{
		"the": {}, "she's": {}, "he": {}, "her": {}, "his": {},
		"this": {}, "that": {}, "these": {}, "those": {},
		"it": {}, "its": {}, "today": {}, "tomorrow": {},
		"yesterday": {}, "now": {}, "then": {}, "soon": {},
	}
	var names []string
	seen := make(map[string]struct{})
	words := strings.Fields(narrative)

	for i, w := range words {
		if len(w) == 0 {
			continue
		}
		firstChar := []rune(w)[0]
		if !unicode.IsUpper(firstChar) {
			continue
		}
		// Clean punctuation
		cleanWord := strings.TrimRight(w, ",.!?;:\"'")
		if cleanWord == "" {
			continue
		}
		// Skip if it's just a sentence starter (and at sentence start position)
		key := strings.ToLower(cleanWord)
		if _, isStarter := sentenceStarters[key]; isStarter {
			// Check if this is likely a sentence start (previous word ends with .!? or it's first word)
			if i == 0 || isEndOfSentence(words, i-1) {
				continue
			}
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, cleanWord)
		if len(names) >= 10 {
			break
		}
	}
	return names
}

// isEndOfSentence checks if the word at index is at the end of a sentence
func isEndOfSentence(words []string, idx int) bool {
	if idx < 0 || idx >= len(words) {
		return false
	}
	word := words[idx]
	// Check if word ends with sentence-ending punctuation
	lastChar := []rune(word)[len([]rune(word))-1]
	return lastChar == '.' || lastChar == '!' || lastChar == '?'
}

// renderSpecialNames formats names with 🆔 prefix
func renderSpecialNames(names []string) string {
	if len(names) == 0 {
		return "Nessun nome speciale rilevato."
	}
	var b strings.Builder
	for _, n := range names {
		b.WriteString("   🆔 ")
		b.WriteString(n)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// extractImportantWords returns top N frequent non-stop words
func extractImportantWords(narrative string, max int) []string {
	stopWords := map[string]struct{}{
		"the": {}, "a": {}, "an": {}, "and": {}, "or": {}, "but": {}, "in": {},
		"on": {}, "at": {}, "to": {}, "for": {}, "of": {}, "with": {}, "by": {},
		"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
		"this": {}, "that": {}, "these": {}, "those": {}, "it": {}, "its": {},
	}
	words := strings.Fields(strings.ToLower(narrative))
	freq := make(map[string]int)
	for _, w := range words {
		w = strings.TrimRight(w, ",.!?;:\"'")
		if w == "" {
			continue
		}
		if _, ok := stopWords[w]; ok {
			continue
		}
		if len([]rune(w)) < 3 {
			continue
		}
		freq[w]++
	}
	type wordFreq struct {
		word  string
		count int
	}
	var wf []wordFreq
	for w, c := range freq {
		wf = append(wf, wordFreq{w, c})
	}
	sort.Slice(wf, func(i, j int) bool {
		return wf[i].count > wf[j].count
	})
	var result []string
	for i, w := range wf {
		if i >= max {
			break
		}
		result = append(result, w.word)
	}
	return result
}

// renderImportantWords formats words with 🔹 prefix
func renderImportantWords(words []string) string {
	if len(words) == 0 {
		return "Nessuna parola importante rilevata."
	}
	var b strings.Builder
	for _, w := range words {
		b.WriteString("   🔹 ")
		b.WriteString(w)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
