// Package translation — providers.go: canonical provider identifiers and
// Argos Translate fingerprint tokens (PR-ARGOS-TRANSLATION, Aug 2026).
//
// godlike/06 SSOT: the provider identifiers stamped on
// TranslationResult.UsedProvider and the Argos request-fingerprint tokens
// live here so the materializer (application/assets/texttracks) and the
// composition root (app/wiring) share the SAME literals instead of
// drifting on inline strings.
package translation

const (
	// ProviderArgos is the canonical provider identifier for the Argos
	// Translate (OpenNMT, offline, deterministic) adapter.
	ProviderArgos = "argos"
	// ProviderOllama is the canonical provider identifier for the Ollama
	// (LLM) adapter, used both standalone and as the Argos fallback.
	ProviderOllama = "ollama"
)

// Argos Translate request-fingerprint tokens. ArgosTranslationModel MUST
// differ from every Ollama model name so that switching the provider stack
// invalidates prior translations (godlike/06 TranslationKey SSOT: the
// operator bumps the model token to change the request fingerprint).
//
// ArgosTranslationModelVersion is bumped to v2 to invalidate the v1 rows
// that were stamped with the Argos fingerprint while Ollama actually
// produced the text (PR-ARGOS-TRANSLATION provenance fix): those rows are
// unreadable as Argos provenance and must be re-translated by Argos on the
// next materialize run.
const (
	ArgosTranslationModel        = "argos-translate"
	ArgosTranslationModelVersion = "argos-translate/v2"
)
