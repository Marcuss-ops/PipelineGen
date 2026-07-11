// Package script — cache_key.go derives the canonical
// memory-gate cache key from a ResolvedGenerationPlan.
//
// EXPLICITLY includes script-text inputs:
//   - SourceFingerprint (resolved source aggregates)
//   - Language, Tone, Style, Model
//   - TargetWords
//   - PromptVersion, PlannerVersion (PromptProfile)
//   - SourceKind (text|clip|catalog|search)
//   - Guidelines (real editorial guidelines)
//   - GroundingPolicy
//
// EXPLICITLY excludes output flags (they don't change the text):
//   - SaveToDB (transport)
//   - DriveFolderID (transport)
//   - GenerateDocument / GenerateVoiceover / GenerateSceneImages /
//     ExtractEntities / GenerateMetadata (postprocessors)
//   - OutputFmt, Languages (postprocessing-only)
//   - ForceRefresh (cache-bypass control, not identity)
//   - Segment sizing (NumClips, SegmentWords, SegmentTopics) —
//     these affect scene planning but are not part of the canonical
//     text-identity fingerprint.
//
// The PR 2 acceptance criteria:
//   - same request -> same CacheKey (deterministic)
//   - language change -> different CacheKey
//   - force_refresh -> memory gate bypassed by the engine, NOT
//     input to BuildCacheKey
//   - output/document/image/voiceover flags -> same CacheKey
//   - fingerprint must NOT appear in the rendered prompt body
package script

// BuildCacheKey returns the deterministic cache key for a plan.
// Two plans that produce identical script text MUST return the
// same CacheKey so the memory gate serves an exact hit. Output
// flags that don't change the text are deliberately excluded
// so toggling "GenerateDocument" doesn't invalidate the cache.
//
// This function is a thin wrapper around the canonical
// BuildFingerprint. All fingerprint logic lives in fingerprint.go;
// this wrapper preserves the existing call sites.
//
// Returns an empty string when plan is nil.
func BuildCacheKey(plan *ResolvedGenerationPlan) string {
	if plan == nil {
		return ""
	}
	return BuildFingerprint(FingerprintInputFromPlan(plan))
}
