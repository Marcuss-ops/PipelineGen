// Package script — cache_key.go derives the canonical
// memory-gate cache key from a ResolvedGenerationPlan.
//
// EXPLICITLY includes script-text inputs:
//   - SourceFingerprint (resolved source aggregates)
//   - Language, Tone, Style, Model
//   - TargetWords / Duration / MinWords
//   - PromptVersion, PromptProfile
//   - SourceKind (text|clip|catalog|search)
//   - Guidelines (real editorial guidelines)
//
// EXPLICITLY excludes output flags (they don't change the text):
//   - SaveToDB (transport)
//   - DriveFolderID (transport)
//   - GenerateDocument / GenerateVoiceover / GenerateSceneImages /
//     ExtractEntities / GenerateMetadata (postprocessors)
//   - OutputFmt, Languages (postprocessing-only)
//   - ForceRefresh (cache-bypass control, not identity)
//
// The PR 2 acceptance criteria:
//   - same request -> same CacheKey (deterministic)
//   - language change -> different CacheKey
//   - force_refresh -> memory gate bypassed by the engine, NOT
//     input to BuildCacheKey
//   - output/document/image/voiceover flags -> same CacheKey
//   - fingerprint must NOT appear in the rendered prompt body
package script

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// BuildCacheKey returns the deterministic cache key for a plan.
// Two plans that produce identical script text MUST return the
// same CacheKey so the memory gate serves an exact hit. Output
// flags that don't change the text are deliberately excluded
// so toggling "GenerateDocument" doesn't invalidate the cache.
//
// Returns an empty string when plan is nil.
func BuildCacheKey(plan *ResolvedGenerationPlan) string {
	if plan == nil {
		return ""
	}

	h := sha256.New()

	add := func(key, val string) {
		if val == "" {
			return
		}
		h.Write([]byte(key))
		h.Write([]byte{'='})
		h.Write([]byte(val))
		h.Write([]byte{'|'})
	}

	addInt := func(key string, n int) {
		if n <= 0 {
			return
		}
		add(key, strconv.Itoa(n))
	}

	// Script-text inputs only. Order is fixed for determinism; do
	// NOT reorder without bumping a cache-version suffix.
	add("fingerprint", plan.SourceFingerprint)
	add("lang", plan.Language)
	add("tone", plan.Tone)
	add("style", plan.Style)
	add("model", plan.Model)
	add("kind", plan.SourceKind)
	add("guidelines", plan.Guidelines)
	addInt("tw", plan.TargetWords)
	addInt("dur", plan.Duration)
	addInt("min", plan.MinWords)
	add("prompt_v", plan.PromptVersion)
	add("profile", plan.PromptProfile)

	sum := h.Sum(nil)
	// First 16 hex chars (64 bits) is the canonical cache address,
	// matching the convention in generation_identity.go's
	// BuildItemIdentity so consumers see one shape across the
	// codebase.
	return hex.EncodeToString(sum[:])[:16]
}
