// Package scriptgeneration — scene_stage_trace.go: the per-scene lifecycle
// trace seam (§7.3 of the pipeline roadmap: "registrare
// ready_at/enqueued_at/started_at/completed_at per testo, traduzione, NLP,
// TTS, asset e audio ... calcolare il critical path dalla trace, senza sommare
// work concorrente").
//
// Every pipeline owner that measures ONE scene through ONE stage records its
// own span here:
//
//	text        SceneTextReady boundary        scene_commit_emit.go
//	translation per (scene, language) pool     scene_ready_coordinator.go / runner_phase_translation.go
//	nlp         enrichment extraction          incremental_coordinator_enrich.go
//	tts         per (scene, language) pool     scene_ready_coordinator.go / runner_phase_voiceover.go
//	asset       provider fan-out/materialize   incremental_coordinator_enrich.go
//	audio       the scene's audio-intent seal  scene_ready_coordinator.go / runner_phase_voiceover.go
//
// The kernel derives queue wait / work / total only from the instants the
// owner actually supplies (never a guessed zero) and computes the critical
// path from the trace: kernel/observability/scene_timeline.go and
// scene_timeline_critical_path.go.
//
// This facade exists so each call site stays one line and every touched file
// stays under max_lines_per_file_strict=600 (godlike/08).
package scriptgeneration

import (
	"context"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// recordSceneStage records one owner-measured (scene, stage) lifecycle on the
// Run bound to ctx; it is a no-op when no Run is bound (tests, dry runs). A
// non-nil err marks the entry failed with its stable error code.
func recordSceneStage(ctx context.Context, sceneID string, stage kernobs.SceneStage, times kernobs.SceneStageTimes, language string, err error) {
	kernobs.RecordSceneStage(ctx, sceneID, stage, times, language, "", err)
}
