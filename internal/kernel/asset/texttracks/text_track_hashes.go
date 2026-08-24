package texttracks

// text_track_hashes.go defines the CANONICAL hash factories for
// asset_text_tracks rows (PR-PY-CLIPS-CORRETTE-TRADOTTE, Fase 1.a,
// July 2026).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The SHA-256 input formula for TextHash    → THIS file
//   - The SHA-256 input formula for SourceVersion → THIS file
//   - The NormalizeForHash() canonicalisation for both hashes → THIS file
//
// NB: this file's `NormalizeForHash` is intentionally distinct from
// the BCP-47 `Normalize(code string) (string, error)` in bcp47.go —
// they normalise different things (free-form text vs BCP-47 locale
// tags) and have different signatures. Callers MUST NOT collapse the
// two: a text payload that happens to be a locale code goes through
// NormalizeForHash for hashing, not through Normalize.
//
// godlike/07 honest lock: the SQLite repository accepts TextHash and
// SourceVersion VERBATIM (it does not compute them — see
// internal/platform/sqlite/assets/text_track_repository.go
// and the schema DDL in migration 137). If a caller recomputes these
// values inline they will silently drift from the canonical formula
// here → split-brain idempotency keys → unbounded re-translation.
// Callers MUST call the helpers below and pass the results through.
//
// The reasoning: TextHash keys idempotent re-acquisitions ("have I
// already seen this text under this language+kind?"). SourceVersion
// keys idempotent re-translations ("have I already translated THIS
// text with THIS model to THIS language?"). A drift between two
// replicas of the same formula is a silent rewrite of the
// idempotency namespace.

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// Normalize is the canonical hash-invariant text representation.
// Whitespace is collapsed to single spaces; leading/trailing
// whitespace stripped; Unicode-case-folded for stability so
// "Hello" and "hello" hash identically (a derived translation
// MUST invalidate on real content changes, not on incidental
// casing drift from the upstream provider).
//
// godlike/06 SSOT: callers MUST call NormalizeForHash before passing a
// text payload to TextHash. Inline `strings.ToLower(TrimSpace(s))`
// duplicates must NOT exist elsewhere.
func NormalizeForHash(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
}

// TextHash is the canonical SHA-256 fingerprint of a
// (text, language_code, text_kind) triple. It detects when source
// content has changed so that downstream translations can be
// invalidated via SourceVersion drift.
//
//	text_hash = sha256(Normalize(text) + "|" +
//	                   language_code   + "|" +
//	                   text_kind)
//
// The pipe `|` separator is reserved (must not appear in language
// codes, text_kind enum values, or normalised transcript text —
// see bcp47.Normalize + TextTrackKind enum for the canonical whitelist).
//
// godlike/06 SSOT: this is the SOLE canonical formula. Mirror
// implementations MUST call this helper, not reimplement the
// SHA-256 chain. The canonical input triple is asserted in
// text_track_hashes_test.go via TestTextHash_Deterministic.
func TextHash(text, language string, kind TextTrackKind) string {
	if language == "" {
		language = "und" // BCP-47 "undetermined" — never silently default to "en"
	}
	payload := strings.Join([]string{
		NormalizeForHash(text),
		language,
		string(kind),
	}, "|")
	return digest.SHA256Bytes([]byte(payload))
}

// SourceVersion is the canonical SHA-256 fingerprint for a derived
// (translation) row that depends on (source text hash, source
// language, target language, provider, model, model version,
// prompt version). When ANY of these inputs change, SourceVersion
// changes and dependent downstream caches / Qdrant reindex
// outbox-events invalidate.
//
//	source_version = sha256(text_hash            + "|" +
//	                       source_language_code  + "|" +
//	                       target_language_code  + "|" +
//	                       provider              + "|" +
//	                       model_name            + "|" +
//	                       model_version         + "|" +
//	                       prompt_version)
//
// godlike/06 SSOT: this is the SOLE canonical formula. The
// canonical input heptuple is asserted in text_track_hashes_test.go
// via TestSourceVersion_Deterministic.
//
// Notes:
//   - model_name / model_version / prompt_version are EMPTY
//     strings when the provider doesn't expose a model taxonomy;
//     the SHA-256 chain remains stable across providers that just
//     stamp "".
//   - textHash is passed verbatim (not re-normalised here) — the
//     caller already invoked TextHash on the source text; passing
//     the double-Normalize result is a caller bug, NOT a formula
//     bug.
func SourceVersion(
	textHash string,
	sourceLanguage string,
	targetLanguage string,
	provider string,
	modelName string,
	modelVersion string,
	promptVersion string,
) string {
	payload := strings.Join([]string{
		textHash,
		sourceLanguage,
		targetLanguage,
		provider,
		modelName,
		modelVersion,
		promptVersion,
	}, "|")
	return digest.SHA256Bytes([]byte(payload))
}

// TranslationKey is the canonical SHA-256 fingerprint of a
// translation REQUEST context — the look-up key the Materializer
// uses BEFORE calling the LLM to decide "have I already produced
// a translation under this exact (source, model, prompt) tuple
// into this language?".
//
// PR-CATALOG-MULTILINGUA step 4 (July 2026, Italian plan):
// the lookup-before-translate gate keys on this fingerprint; if
// a row exists with matching translation_key + status=READY +
// is_current=1, the translation is REUSED and the LLM call is
// skipped (no row insert, no audit-write). If no matching row
// exists, a new row is inserted (marking the prior is_current=1
// row is_current=0 atomically — see InsertTranslationWithAuditPredecessor).
//
// Formula:
//
//	translation_key = sha256(source_text_hash       + "|" +
//	                         target_language_code   + "|" +
//	                         translation_model      + "|" +
//	                         model_version          + "|" +
//	                         prompt_version)
//
// Distinction from SourceVersion (godlike/06 SSOT — one canonical
// owner per FACT, not per Algorithm):
//
//   - SourceVersion is the DERIVED-row fingerprint (7 inputs,
//     including the source language, provider, model_name); it
//     changes when ANY provenance column changes. Used for
//     downstream cache invalidation (Qdrant outbox events driven
//     by source_version drift).
//   - TranslationKey is the REQUEST fingerprint (5 inputs WITHOUT
//     source_language / provider / model_name) — those are
//     derivable from the resolver context and not part of the
//     per-row idempotency key. Smaller input set = cleaner
//     stability contract: a swap from ollama-qwen to ollama-llama
//     under the SAME model_name does NOT change the request
//     fingerprint, the user instead bumps TranslationModel in the
//     Materializer ResolverConfig.
//
// godlike/06 SSOT: this is the SOLE canonical formula. The
// canonical input quintuple is asserted in text_track_hashes_test.go
// via TestTranslationKey_Deterministic. Mirror callers MUST
// call this helper, not reimplement the SHA-256 chain. Inline
// "translation_key" string concatenation anywhere in the codebase
// is a canonical-drift detector trigger for code-reviewer-minimax-m3.
//
// Notes:
//   - source_text_hash is passed verbatim (the caller already
//     invoked TextHash on the source text; passing a re-normalised
//     payload is a caller bug, NOT a formula bug).
//   - translation_model / model_version / prompt_version are
//     EMPTY strings when the provider does not expose a model
//     taxonomy; the SHA-256 chain remains stable across empty
//     strings.
//   - target_language_code is the BCP-47 tag of the OUTPUT
//     language. Empty string is a contract violation at the call
//     site — this helper does NOT substitute a BCP-47 "und"
//     default; TranslationPort.Translate is the upstream gate.
func TranslationKey(
	sourceTextHash string,
	targetLanguageCode string,
	translationModel string,
	modelVersion string,
	promptVersion string,
) string {
	payload := strings.Join([]string{
		sourceTextHash,
		targetLanguageCode,
		translationModel,
		modelVersion,
		promptVersion,
	}, "|")
	return digest.SHA256Bytes([]byte(payload))
}
