// Package runner contains reusable, side-effect-free policies for script runs.
package runner

import "sort"

// LanguageWork is the text and translation requirement for one requested
// language on a scene.
type LanguageWork struct {
	Language         string
	Text             string
	NeedsTranslation bool
}

// LanguagePriority assigns source first, requested targets in caller order,
// then undeclared languages after every target.
func LanguagePriority(source string, targets []string, language string) int {
	if language == source {
		return 0
	}
	for i, target := range targets {
		if target == language {
			return i + 1
		}
	}
	return len(targets) + 2
}

// OrderedLanguages returns map keys in source/target priority order, with
// undeclared languages alphabetized to make the order total and deterministic.
func OrderedLanguages(text map[string]string, source string, targets []string) []string {
	languages := make([]string, 0, len(text))
	for language := range text {
		languages = append(languages, language)
	}
	sort.Slice(languages, func(i, j int) bool {
		left := LanguagePriority(source, targets, languages[i])
		right := LanguagePriority(source, targets, languages[j])
		if left != right {
			return left < right
		}
		return languages[i] < languages[j]
	})
	return languages
}

// RequestedLanguages deduplicates source/targets while preserving caller order
// and omitting empty language IDs.
func RequestedLanguages(source string, targets []string) []string {
	languages := make([]string, 0, len(targets)+1)
	seen := make(map[string]struct{}, len(targets)+1)
	for _, language := range append([]string{source}, targets...) {
		if language == "" {
			continue
		}
		if _, exists := seen[language]; exists {
			continue
		}
		seen[language] = struct{}{}
		languages = append(languages, language)
	}
	return languages
}

// BuildLanguageWork preserves the supplied language order. Existing target
// text does not need translation; the source language is never translated.
func BuildLanguageWork(languages []string, text map[string]string, source string) []LanguageWork {
	work := make([]LanguageWork, 0, len(languages))
	for _, language := range languages {
		value := text[language]
		work = append(work, LanguageWork{
			Language: language, Text: value,
			NeedsTranslation: language != source && value == "",
		})
	}
	return work
}

// VoiceoverLanguageAllowed distinguishes an omitted filter (all languages)
// from an explicitly empty filter (no languages).
func VoiceoverLanguageAllowed(languages []string, language string) bool {
	if languages == nil {
		return true
	}
	for _, allowed := range languages {
		if allowed == language {
			return true
		}
	}
	return false
}

// BuildVoiceoverLanguageWork selects non-empty text in canonical dispatch
// order, optionally restricted by the caller's explicit voiceover list.
func BuildVoiceoverLanguageWork(text map[string]string, source string, targets, voiceoverLanguages []string) []LanguageWork {
	languages := OrderedLanguages(text, source, targets)
	work := BuildLanguageWork(languages, text, source)
	out := work[:0]
	for _, item := range work {
		if VoiceoverLanguageAllowed(voiceoverLanguages, item.Language) && item.Text != "" {
			out = append(out, item)
		}
	}
	return out
}

// RequestedVoiceoverLanguages projects the selected voiceover language IDs.
func RequestedVoiceoverLanguages(text map[string]string, source string, targets, voiceoverLanguages []string) []string {
	work := BuildVoiceoverLanguageWork(text, source, targets, voiceoverLanguages)
	languages := make([]string, 0, len(work))
	for _, item := range work {
		languages = append(languages, item.Language)
	}
	return languages
}

// SortBySceneAndLanguage stably orders work by scene position then canonical
// language priority; original order is preserved for equal keys.
func SortBySceneAndLanguage[T any](work []T, sceneIndex func(T) int, language func(T) string, source string, targets []string) {
	sort.SliceStable(work, func(i, j int) bool {
		leftScene, rightScene := sceneIndex(work[i]), sceneIndex(work[j])
		if leftScene != rightScene {
			return leftScene < rightScene
		}
		return LanguagePriority(source, targets, language(work[i])) < LanguagePriority(source, targets, language(work[j]))
	})
}
