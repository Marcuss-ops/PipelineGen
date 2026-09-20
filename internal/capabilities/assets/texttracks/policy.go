// Package texttracks — policy.go: idempotency + STALE-marking policy
// for TextTrackMaterializer.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// skip/retranslate decision. The materializer main loop delegates
// here; callers MUST NOT inline the version-equality check
// themselves.
package texttracks

// MaterializationKey is the canonical key that determines
// idempotency at the (asset, kind, target_lang) granularity.
type MaterializationKey struct {
	SourceVersion  string
	ModelVersion   string
	PromptVersion  string
	SourceTextHash string
}

// matches returns true when the existing track's fingerprint
// matches this key. Per godlike/06 SSOT: the per-language
// idempotency check uses SourceVersion + ModelVersion. The
// TextHash field on the existing track is the hash of the
// TRANSLATED text, so it is NOT used in the matches check.
// The k.SourceTextHash is preserved in the struct for
// IdempotencyKey() construction but is not a per-language
// skip component.

// ShouldSkip reports whether the materializer should skip
// translating into targetLang because an existing READY track
// already carries the same MaterializationKey.

// ShouldRetranslate reports whether the materializer should
// retranslate an existing READY track because its fingerprint
// has drifted from the candidate key.

// IdempotencyKey constructs the canonical outbox event_key for
// the (asset, kind, source_text_hash, target_language,
// model_version, prompt_version) tuple.
//
// Format:
//
//	asset.text.translate:<asset_id>:<text_kind>:<source_text_hash>:<target_language>:<model_version>:<prompt_version>

// ComputeSourceTextHash is the canonical hash for the source
// text_content. godlike/06 SSOT: the actual hash function
// (sha256 vs fnv vs blake3) is owned by this file. A future
// PR can swap the algorithm via hashSHA256Hex.
func ComputeSourceTextHash(text string) string {
	return hashSHA256Hex(text)
}
