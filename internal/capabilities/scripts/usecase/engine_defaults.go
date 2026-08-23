package usecase

// ConfigureScriptDefaults injects the already-resolved script defaults from
// the composition root. Engine.Generate does not consult YAML, environment,
// or package-local default registries after this point.
func (e *Engine) ConfigureScriptDefaults(language, tone string, wordsPerMinute int) {
	if e == nil {
		return
	}
	e.defaultLanguage = language
	e.defaultTone = tone
	e.wordsPerMinute = wordsPerMinute
}
