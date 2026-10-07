package scriptgeneration

import (
	scriptRunner "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runner"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
func dispatchLanguagePriority(source Language, targets []Language, lang Language) int {
	return scriptRunner.LanguagePriority(string(source), languageStrings(targets), string(lang))
}

// orderedSceneLanguages returns the scene's languages in deterministic
// dispatch order: source language first, then targets in caller order, then
// any undeclared languages alphabetically. It is the single owner of the
// language_priority half of the (scene_index, language_priority) key.
func orderedSceneLanguages(text map[Language]string, source Language, targets []Language) []Language {
	ordered := scriptRunner.OrderedLanguages(languageTextStrings(text), string(source), languageStrings(targets))
	langs := make([]Language, len(ordered))
	for i := range ordered {
		langs[i] = Language(ordered[i])
	}
	return langs
}

// resolveArtifactRoutingContext resolves the canonical artifact routing
// context from the generation input. It is the single derivation point for
// Project / Language / folder routing; downstream phases consume the resolved
// value and never read req.Project or invent a namespace.
//
// The script documents destination is resolved through the single canonical
// resolver: explicit docs.folder_id > configured default (the runner's
// PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID) > fail closed when docs.enabled=true.
// A docs-enabled request with no resolvable folder returns an error so the
// runner fails the run BEFORE any Google Docs write.
func (req GenerateRequest) resolveArtifactRoutingContext(defaultDocsFolderID string) (kernelscript.ArtifactRoutingContext, error) {
	callerFolderID := req.Docs.FolderID
	enabled, _, _ := req.ResolveDocsConfig()
	docsFolderID, err := kernelscript.ResolveScriptDocsFolderID(enabled, callerFolderID, defaultDocsFolderID)
	if err != nil {
		return kernelscript.ArtifactRoutingContext{}, err
	}
	return kernelscript.ResolveArtifactRoutingContext(req.Project, string(req.SourceLanguage), req.VoiceoverFolderID, docsFolderID), nil
}

// ── Shared per-language work projection ─────────────────────────────

// sceneLanguageWork is the shared per-language projection used by streaming
// translation/TTS and batch voiceover selection. The source language never
// needs translation; target translations are selected only when text is empty.
type sceneLanguageWork struct {
	lang             Language
	text             string
	needsTranslation bool
}

func languageStrings(languages []Language) []string {
	if languages == nil {
		return nil
	}
	out := make([]string, len(languages))
	for i := range languages {
		out[i] = string(languages[i])
	}
	return out
}

func languageTextStrings(text map[Language]string) map[string]string {
	out := make(map[string]string, len(text))
	for language, value := range text {
		out[string(language)] = value
	}
	return out
}

func sceneLanguageWorks(work []scriptRunner.LanguageWork) []sceneLanguageWork {
	out := make([]sceneLanguageWork, len(work))
	for i := range work {
		out[i] = sceneLanguageWork{lang: Language(work[i].Language), text: work[i].Text, needsTranslation: work[i].NeedsTranslation}
	}
	return out
}

type voiceoverLanguageFilter struct {
	allowed  map[Language]struct{}
	explicit bool
}

func newVoiceoverLanguageFilter(languages []Language) voiceoverLanguageFilter {
	allowed := make(map[Language]struct{}, len(languages))
	for _, lang := range languages {
		allowed[lang] = struct{}{}
	}
	return voiceoverLanguageFilter{allowed: allowed, explicit: languages != nil}
}

func (f voiceoverLanguageFilter) allows(language Language) bool {
	if !f.explicit {
		return scriptRunner.VoiceoverLanguageAllowed(nil, string(language))
	}
	return scriptRunner.VoiceoverLanguageAllowed(languageStringsFromSet(f.allowed), string(language))
}

func languageStringsFromSet(languages map[Language]struct{}) []string {
	out := make([]string, 0, len(languages))
	for language := range languages {
		out = append(out, string(language))
	}
	return out
}

func requestedSceneLanguages(source Language, targets []Language) []Language {
	requested := scriptRunner.RequestedLanguages(string(source), languageStrings(targets))
	langs := make([]Language, len(requested))
	for i := range requested {
		langs[i] = Language(requested[i])
	}
	return langs
}

func buildSceneLanguageWork(langs []Language, text map[Language]string, source Language) []sceneLanguageWork {
	return sceneLanguageWorks(scriptRunner.BuildLanguageWork(languageStrings(langs), languageTextStrings(text), string(source)))
}

func buildVoiceoverLanguageWork(text map[Language]string, source Language, targets, voiceoverLanguages []Language) []sceneLanguageWork {
	work := scriptRunner.BuildVoiceoverLanguageWork(languageTextStrings(text), string(source), languageStrings(targets), languageStrings(voiceoverLanguages))
	return sceneLanguageWorks(work)
}

func requestedVoiceoverLanguages(text map[Language]string, source Language, targets, voiceoverLanguages []Language) []Language {
	work := buildVoiceoverLanguageWork(text, source, targets, voiceoverLanguages)
	langs := make([]Language, 0, len(work))
	for _, item := range work {
		langs = append(langs, item.lang)
	}
	return langs
}

func voiceoverLanguageRequested(req GenerateRequest, language Language) bool {
	return scriptRunner.VoiceoverLanguageAllowed(languageStrings(req.VoiceoverLanguages), string(language))
}
