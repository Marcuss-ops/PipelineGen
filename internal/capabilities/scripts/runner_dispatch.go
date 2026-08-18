package scriptgeneration

import "sort"

// runner_dispatch.go owns the deterministic dispatch priority for scene×
// language work units. Concurrency may be free — the worker pool can run units
// in any completion order — but the ORDER in which units are offered to the
// pool must be stable across runs. The canonical key is:
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
	if lang == source {
		return 0
	}
	for i, t := range targets {
		if t == lang {
			return i + 1
		}
	}
	return len(targets) + 2
}

// orderedSceneLanguages returns the scene's languages in deterministic
// dispatch order: source language first, then targets in caller order, then
// any undeclared languages alphabetically. It is the single owner of the
// language_priority half of the (scene_index, language_priority) key.
func orderedSceneLanguages(text map[Language]string, source Language, targets []Language) []Language {
	langs := make([]Language, 0, len(text))
	for lang := range text {
		langs = append(langs, lang)
	}
	sort.Slice(langs, func(a, b int) bool {
		pa := dispatchLanguagePriority(source, targets, langs[a])
		pb := dispatchLanguagePriority(source, targets, langs[b])
		if pa != pb {
			return pa < pb
		}
		return langs[a] < langs[b]
	})
	return langs
}
