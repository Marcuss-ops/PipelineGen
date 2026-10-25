// Package scriptgeneration — scene_trace_contract_test.go is the §7.3
// ACCEPTANCE pin: the per-scene lifecycle trace must be populated by the REAL
// pipeline seams (not by a test-only recorder), and the critical path must be
// derivable from that trace without summing concurrent work.
//
// The test drives the actual seam functions the runner calls:
//   - emitSceneCommit — the SceneTextReady text anchor (both batch and streaming);
//   - translateLanguage — the per-(scene, language) translation pool;
//   - synthesizeLanguage — the per-(scene, language) TTS pool.
//
// It then asserts the run report carries the entries with their real instants
// and that CriticalPath() chains text → translation → TTS for the scene.
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type traceContractTranslator struct{}

func (traceContractTranslator) Translate(_ context.Context, in TranslationInput) (string, error) {
	return "translated:" + in.SourceText, nil
}

type traceContractVoiceover struct{}

func (traceContractVoiceover) Generate(_ context.Context, in VoiceoverInput) (AudioReference, error) {
	return AudioReference{ID: "vo-" + in.SceneID + "-" + string(in.Language), FilePath: "/tmp/vo.mp3", Duration: 1.25}, nil
}

func TestSceneTraceContract_RealSeamsFeedTheTimelineAndCriticalPath(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-trace-contract", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	runner := &Runner{translator: traceContractTranslator{}, voiceoverGen: traceContractVoiceover{}}
	req := GenerateRequest{SourceLanguage: "en", Languages: []Language{"en", "es"}, Audio: capabilityaudio.AudioModeChunkedVoiceover}
	coordinator := newSceneReadyCoordinator(ctx, runner, "run-trace-contract", req, scriptpkg.ArtifactRoutingContext{Project: "trace-project"}, ExecutionContext{})

	textReadyAt := time.Now().UTC().Add(-3 * time.Millisecond)
	scene := Scene{ID: "scene-trace", Index: 0, Text: map[Language]string{"en": "source text"}, TextReadyAt: textReadyAt}

	// 1) SceneTextReady anchor (the real commit seam, per-scene).
	if err := runner.emitSceneCommit(ctx, "run-trace-contract", req, ExecutionContext{}, scene); err != nil {
		t.Fatalf("emitSceneCommit: %v", err)
	}
	// 2) Per-language translation through the real pool seam.
	if _, err := coordinator.translateLanguage(ctx, 0, scene.ID, "es", "source text", textReadyAt); err != nil {
		t.Fatalf("translateLanguage: %v", err)
	}
	// 3) Per-language TTS through the real pool seam.
	if _, err := coordinator.synthesizeLanguage(ctx, 0, scene.ID, "es", "translated:source text", textReadyAt); err != nil {
		t.Fatalf("synthesizeLanguage: %v", err)
	}

	timeline := run.SceneTimeline()
	if timeline == nil {
		t.Fatal("scene timeline must be populated by the real seams")
	}
	stages := make(map[kernobs.SceneStage]kernobs.SceneStageEntry, len(timeline.Entries))
	for _, entry := range timeline.Entries {
		if entry.SceneID != scene.ID {
			t.Fatalf("trace entry leaked to another scene: %+v", entry)
		}
		stages[entry.Stage] = entry
	}
	textEntry, ok := stages[kernobs.SceneStageText]
	if !ok || !textEntry.CompletedAt.Equal(textReadyAt) {
		t.Fatalf("text anchor = %+v, want completed_at = scene TextReadyAt", textEntry)
	}
	translationEntry, ok := stages[kernobs.SceneStageTranslation]
	if !ok || translationEntry.Language != "es" {
		t.Fatalf("translation entry = %+v, want language es", translationEntry)
	}
	if translationEntry.ReadyAt.IsZero() || translationEntry.StartedAt.IsZero() || translationEntry.CompletedAt.IsZero() {
		t.Fatalf("translation entry lost a lifecycle instant: %+v", translationEntry)
	}
	ttsEntry, ok := stages[kernobs.SceneStageTTS]
	if !ok || ttsEntry.Language != "es" {
		t.Fatalf("tts entry = %+v, want language es", ttsEntry)
	}
	if ttsEntry.CompletedAt.Before(translationEntry.CompletedAt) {
		t.Fatalf("tts completed before its translation: tts=%v translation=%v", ttsEntry.CompletedAt, translationEntry.CompletedAt)
	}

	path := timeline.CriticalPath()
	if path == nil || len(path.Chain) < 2 {
		t.Fatalf("critical path = %+v, want text→translation→tts chain", path)
	}
	got := ""
	for i, entry := range path.Chain {
		if i > 0 {
			got += "→"
		}
		got += string(entry.Stage)
	}
	if got != "text→translation→tts" {
		t.Fatalf("critical chain = %s, want text→translation→tts (concurrent branches must not be summed)", got)
	}
	if path.TotalMs < 0 {
		t.Fatalf("critical path total must never be negative: %d", path.TotalMs)
	}
}
