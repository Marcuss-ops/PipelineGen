package defaults

// ScriptConfig is the canonical SSOT for script-generation defaults.
//
// Pre-fix scattered literals this SSOT replaces (June 2026, Step 4
// PR5 — DRIFT-DEFAULTS-SCRIPT):
//
//   - WordsPerMinute = 150
//     (the canonical active-path value used by
//     internal/application/scripts/adapters/generation_normalizer.go's
//     `applyConfigDefaults` since V2 unification, June 2026).
//   - DefaultDuration = 600
//     (internal/platform/ollama/types/defaults.go).
//   - DefaultLanguage = "it"
//     (same file).
//   - DefaultTemplate = "documentary"
//     (same file).
//   - DefaultTone = "documentary"
//     (same file).
//   - SafetyLanguage = "en"
//     (Step-3 safety floor — distinct from DefaultLanguage because
//     the V1 contract language for script generation is "en" while
//     the Step-2 config-default is "it"; per-locale overrides of
//     DefaultLanguage must not silently flip the safety floor).
//   - Literal `140` in
//     internal/application/lessons/service.go::estimateChapterDuration
//     (the formula `(words * 60) / 140`); and the typed constant
//     `internal/platform/ollama/types/constants.go::WordsPerMinute
//     (=140). Both are DEFERRED — see Blocco 2.C follow-up TODOs
//     registered against this commit; unifying them would cross
//     domain boundaries (lessons + ollama-internal infra) with
//     behavior-changing value flips.
//
// Every consumer MUST read from DefaultScriptConfig() rather than
// re-implementing these literals inline. A future "switch the
// default language from Italian to Spanish" or "tune WPM to 160
// for Italian speakers" change is then a one-line edit; pre-fix it
// required grep + reasoning about which call sites must agree
// (and historically missed the lessons/service.go copy, which kept
// the WPM=140 literal even after the ollama-side constant moved).
//
// Shape is intentionally tiny (6 leaf fields) to keep pkg/defaults
// leaf-only: zero imports from internal/, only consumed by callers
// crossing the infra→application seam.
type ScriptConfig struct {
	// WordsPerMinute is the canonical speech-rate assumption used
	// to estimate script duration from word count. Active-path
	// value: 150 (promoted from legacy 140 during V2 unification,
	// June 2026, to match
	// internal/application/scripts/adapters/generation_normalizer.go).
	// The SSOT is intentionally a single "base" value shared
	// across locales; per-language overrides are out of scope for
	// this SSOT and belong in a follow-on per-locale override
	// layer when one is implemented.
	WordsPerMinute int

	// DefaultDuration is the target script duration in seconds
	// when the caller leaves the field empty. Legacy inlined
	// value: 600 (= 10 min). PipelineGen's "single-script
	// endpoint" targets 10-min YouTube-style scripts by default;
	// the batch endpoint already uses 600.
	DefaultDuration int

	// DefaultLanguage is the BCP-47 code applied when the caller
	// does not specify a language. Legacy inlined value: "it".
	// Italian-first deployments can flip this SSOT to keep the
	// default without touching every call site.
	DefaultLanguage string

	// DefaultTemplate is the canonical script template name applied
	// when the caller does not specify one. Legacy inlined value:
	// "documentary".
	DefaultTemplate string

	// DefaultTone is the canonical script tone applied when
	// the caller does not specify one. Legacy inlined value:
	// "documentary". MUST match a known tone enum value (the
	// "documentary" / "narrative" / "explainer" / "listicle" set).
	DefaultTone string

	// SafetyLanguage is the BCP-47 code applied at the Step-3
	// safety floor (applySafetyDefaults in
	// internal/application/scripts/adapters/generation_normalizer.go)
	// when caller + preset + config are ALL unset. Semantically
	// distinct from DefaultLanguage: DefaultLanguage is the
	// Step-2 config-default fallback (today "it"); SafetyLanguage
	// is the V1 contract language (today "en") that the model
	// pipeline was originally validated against. Kept separate so
	// per-locale overrides don't accidentally override the safety
	// floor too. Out-of-scope: voiceover-side defaults (see
	// pkg/defaults/voiceover.go::DefaultLanguage).
	SafetyLanguage string
}

// DefaultScriptConfig returns the canonical DRIFT-DEFAULTS-SCRIPT
// SSOT. Treat the returned value as immutable per consumer site
// (no process-global mutation — copy and adjust locally if needed).
func DefaultScriptConfig() ScriptConfig {
	return ScriptConfig{
		WordsPerMinute:  150,
		DefaultDuration: 600,
		DefaultLanguage: "it",
		DefaultTemplate: "documentary",
		DefaultTone:     "documentary",
		SafetyLanguage:  "en",
	}
}
