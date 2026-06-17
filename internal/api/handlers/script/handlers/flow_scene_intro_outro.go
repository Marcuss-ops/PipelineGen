package handlers

// Package handlers — markScenesIntroOutro: positional labelling helper for
// the user-facing `scenes[]` array.
//
// Why this file exists:
//   The /api/script/generate-with-images endpoint produces a scenes[] array
//   where the first scene is, by convention, an intro narration and the last
//   scene is the outro narration. As of June 2026, /api/script/generate-from
//   -clips must match that output. The writer LLM (especially the smaller
//   qwen2.5 / gemma2 variants) does NOT reliably emit [Narration: intro] /
//   [Narration: outro] markers, so post-processing the OUTPUT is the only
//   reliable mechanism to guarantee they appear.
//
//   This helper does exactly that: it labels the first and last scene of the
//   scenes[] array without touching the middle scenes. It is applied at the
//   end of generateSceneImages and at the end of any other path that produces
//   `[]ScriptSceneImage`. By design it is additive — it only sets fields the
//   caller left empty so previously-set values are preserved.
//
// Modification points (rules):
//   - Both fields are omitempty in the JSON schema (see types_clip_source.go);
//     consumers that don't know about kind/narration_role still work.
//   - The helper is intentionally positional — see decision_log below for why
//     we did NOT use LLM-emitted marker parsing.
//
// Decision log:
//   We considered three approaches:
//     1. Use LLM-emitted [Narration: intro] / [Narration: outro] markers
//     2. Always PREPEND an intro scene + APPEND an outro scene (additive)
//     3. Positional labelling on existing scenes[] (this file's choice)
//   Approach 1 fails when the writer is a small model that drops markers.
//   Approach 2 changes the scene[] length and breaks downstream
//     scenes[i]/voiceover/phase2 wiring that indexes by position.
//   Approach 3 is the smallest possible change: zero new scenes, zero risk
//     of mistaking marker-omission for marker-emission, and the rendered
//     Google Doc / JSON payload gets an explicit intro/outro label pair.

// markScenesIntroOutro labels the first scene as narration/introduction and the
// last as narration/outroduction. Middle scenes are untouched.
//
// Idempotent: it only assigns Kind/NarrationRole when those fields are still
// empty (zero-value). Existing values from upstream callers — for example a
// clip-aware path that already produced a `Kind="clip"` scene — are preserved
// because the writer LLM could have placed a marker-equivalent narration scene
// at the boundary; in that case the caller already populated the field.
//
// Returns the same slice for fluent chaining. Safe on nil/empty input.
func markScenesIntroOutro(scenes []ScriptSceneImage) []ScriptSceneImage {
	if len(scenes) == 0 {
		return scenes
	}

	// First scene → intro narration.
	// We touch the field only when the caller hasn't already set it, so a
	// clip-aware path that pre-set Kind="narration" with role="transition"
	// (rare but possible) is preserved instead of being overwritten.
	if scenes[0].Kind == "" {
		scenes[0].Kind = "narration"
	}
	if scenes[0].NarrationRole == "" {
		scenes[0].NarrationRole = "intro"
	}

	// Last scene → outro narration. Only needed when there is more than one
	// scene (single-scene scripts are intentionally NOT relabelled — there
	// is no body to separate intro from outro).
	if len(scenes) > 1 {
		last := len(scenes) - 1
		if scenes[last].Kind == "" {
			scenes[last].Kind = "narration"
		}
		if scenes[last].NarrationRole == "" {
			scenes[last].NarrationRole = "outro"
		}
	}

	return scenes
}
