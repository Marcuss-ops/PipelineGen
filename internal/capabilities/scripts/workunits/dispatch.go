package workunits

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptRunner "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runner"
)

// runner_dispatch.go owns the deterministic dispatch priority for scene×
// language work units. It also holds the shared (scene, language) projection
// consumed by streaming translation/TTS and batch voiceover selection.
// Concurrency may be free — the worker pool can run units in any completion
// order — but the ORDER in which units are offered to the pool must be stable
// across runs. The canonical key is:
//
//	(scene_index, language_priority)
//
// scene_index is the scene's zero-based canonical position; language_priority
// is the source language first (0), then each target language in the caller's
// req.Languages order (1, 2, …). An undeclared language falls after every
// declared one and ties-breaks alphabetically, so the total order never
// depends on map iteration order.

// dispatchLanguagePriority returns the deterministic within-scene dispatch
// priority of lang: 0 for the source language, 1..len(targets) for a target
// in req.Languages order, and len(targets)+2 for any undeclared language
// (callers tie-break those alphabetically).
func DispatchLanguagePriority(source contract.Language, targets []contract.Language, lang contract.Language) int {
	return scriptRunner.LanguagePriority(string(source), LanguageStrings(targets), string(lang))
}

// orderedSceneLanguages returns the scene's languages in deterministic
// dispatch order: source language first, then targets in caller order, then
// any undeclared languages alphabetically. It is the single owner of the
// language_priority half of the (scene_index, language_priority) key.
func OrderedSceneLanguages(text map[contract.Language]string, source contract.Language, targets []contract.Language) []contract.Language {
	ordered := scriptRunner.OrderedLanguages(languageTextStrings(text), string(source), LanguageStrings(targets))
	langs := make([]contract.Language, len(ordered))
	for i := range ordered {
		langs[i] = contract.Language(ordered[i])
	}
	return langs
}

// ── Shared per-language work projection ─────────────────────────────

// SceneLanguageWork is the shared per-language projection used by streaming
// translation/TTS and batch voiceover selection. The source language never
// needs translation; target translations are selected only when text is empty.
type SceneLanguageWork struct {
	Lang             contract.Language
	Text             string
	NeedsTranslation bool
}

func LanguageStrings(languages []contract.Language) []string {
	if languages == nil {
		return nil
	}
	out := make([]string, len(languages))
	for i := range languages {
		out[i] = string(languages[i])
	}
	return out
}

func languageTextStrings(text map[contract.Language]string) map[string]string {
	out := make(map[string]string, len(text))
	for language, value := range text {
		out[string(language)] = value
	}
	return out
}

func sceneLanguageWorks(work []scriptRunner.LanguageWork) []SceneLanguageWork {
	out := make([]SceneLanguageWork, len(work))
	for i := range work {
		out[i] = SceneLanguageWork{Lang: contract.Language(work[i].Language), Text: work[i].Text, NeedsTranslation: work[i].NeedsTranslation}
	}
	return out
}

type voiceoverLanguageFilter struct {
	allowed  map[contract.Language]struct{}
	explicit bool
}

func NewVoiceoverLanguageFilter(languages []contract.Language) voiceoverLanguageFilter {
	allowed := make(map[contract.Language]struct{}, len(languages))
	for _, lang := range languages {
		allowed[lang] = struct{}{}
	}
	return voiceoverLanguageFilter{allowed: allowed, explicit: languages != nil}
}

func (f voiceoverLanguageFilter) Allows(language contract.Language) bool {
	if !f.explicit {
		return scriptRunner.VoiceoverLanguageAllowed(nil, string(language))
	}
	return scriptRunner.VoiceoverLanguageAllowed(languageStringsFromSet(f.allowed), string(language))
}

func languageStringsFromSet(languages map[contract.Language]struct{}) []string {
	out := make([]string, 0, len(languages))
	for language := range languages {
		out = append(out, string(language))
	}
	return out
}

func RequestedSceneLanguages(source contract.Language, targets []contract.Language) []contract.Language {
	requested := scriptRunner.RequestedLanguages(string(source), LanguageStrings(targets))
	langs := make([]contract.Language, len(requested))
	for i := range requested {
		langs[i] = contract.Language(requested[i])
	}
	return langs
}

func BuildSceneLanguageWork(langs []contract.Language, text map[contract.Language]string, source contract.Language) []SceneLanguageWork {
	return sceneLanguageWorks(scriptRunner.BuildLanguageWork(LanguageStrings(langs), languageTextStrings(text), string(source)))
}

func BuildVoiceoverLanguageWork(text map[contract.Language]string, source contract.Language, targets, voiceoverLanguages []contract.Language) []SceneLanguageWork {
	work := scriptRunner.BuildVoiceoverLanguageWork(languageTextStrings(text), string(source), LanguageStrings(targets), LanguageStrings(voiceoverLanguages))
	return sceneLanguageWorks(work)
}
