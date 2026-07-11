package asset

// text_track_hashes.go defines the CANONICAL hash factories for
// asset_text_tracks rows (PR-PY-CLIPS-CORRETTE-TRADOTTE, Fase 1.a,
// July 2026).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The SHA-256 input formula for TextHash    → THIS file
//   - The SHA-256 input formula for SourceVersion → THIS file
//   - The Normalize() canonicalisation for both hashes → THIS file
//
// godlike/07 honest lock: the SQLite repository accepts TextHash and
// SourceVersion VERBATIM (it does not compute them — see
// internal/infrastructure/database/sqlite/assets/text_track_repository.go
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
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Normalize is the canonical hash-invariant text representation.
// Whitespace is collapsed to single spaces; leading/trailing
// whitespace stripped; Unicode-case-folded for stability so
// "Hello" and "hello" hash identically (a derived translation
// MUST invalidate on real content changes, not on incidental
// casing drift from the upstream provider).
//
// godlike/06 SSOT: callers MUST call Normalize before passing a
// text payload to TextHash. Inline `strings.ToLower(TrimSpace(s))`
// duplicates must NOT exist elsewhere.
func Normalize(text string) string {
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
		Normalize(text),
		language,
		string(kind),
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
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
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
