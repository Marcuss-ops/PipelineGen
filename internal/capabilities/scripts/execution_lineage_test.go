package scriptgeneration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

type lineageAudioRenderer struct{}

func (lineageAudioRenderer) Render(_ context.Context, plan capabilityaudio.CompiledAudioPlan, _ capabilityaudio.ResolvedAudioAssets) (FinalAudioReference, AudioPipelineMetrics, error) {
	return FinalAudioReference{
		AssetID: "final-audio", Path: "/tmp/final-audio.m4a",
		AudioContractVersion: capabilityaudio.AudioContractVersion, AudioPlanVersion: plan.Version,
		PlanSHA256: plan.PlanSHA256, FinalAudioSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Codec: plan.Output.Codec,
		Profile: plan.Output.Profile, SampleRate: plan.Output.SampleRate, Channels: plan.Output.Channels,
		ChannelLayout: plan.Output.ChannelLayout, Bitrate: 128000, DurationMS: plan.DurationUS / 1000,
		StartPTS: 0, SizeBytes: 1, FinalMix: true, CopyEligible: true,
	}, AudioPipelineMetrics{AudioDurationMS: plan.DurationUS / 1000}, nil
}

type recordingExecutionRecorder struct {
	contexts    []ExecutionContext
	steps       []ExecutionStep
	inputs      []string
	outputs     []string
	completeErr error
}

func (r *recordingExecutionRecorder) StartStep(_ context.Context, exec ExecutionContext, step ExecutionStep) error {
	r.contexts = append(r.contexts, exec)
	r.steps = append(r.steps, step)
	return nil
}
func (r *recordingExecutionRecorder) CompleteStep(_ context.Context, exec ExecutionContext, step ExecutionStep) error {
	r.contexts = append(r.contexts, exec)
	r.steps = append(r.steps, step)
	return r.completeErr
}
func (r *recordingExecutionRecorder) FailStep(_ context.Context, exec ExecutionContext, step ExecutionStep, _ error) error {
	r.contexts = append(r.contexts, exec)
	r.steps = append(r.steps, step)
	return nil
}
func (r *recordingExecutionRecorder) AttachInputAsset(_ context.Context, exec ExecutionContext, _ string, assetID string, _ int) error {
	r.contexts = append(r.contexts, exec)
	r.inputs = append(r.inputs, assetID)
	return nil
}
func (r *recordingExecutionRecorder) AttachOutputAsset(_ context.Context, exec ExecutionContext, _ string, assetID string, _ int) error {
	r.contexts = append(r.contexts, exec)
	r.outputs = append(r.outputs, assetID)
	return nil
}
func (r *recordingExecutionRecorder) RecordMetric(_ context.Context, exec ExecutionContext, _ string, _ string, _ float64, _ string) error {
	r.contexts = append(r.contexts, exec)
	return nil
}

func TestRunnerExecutionContextAndLineageCertification(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	clipPath := t.TempDir() + "/clip.mp4"
	clipBytes := []byte("lineage clip")
	if err := os.WriteFile(clipPath, clipBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(clipBytes)
	// The combined-timeline resolver makes a clip-bound scene span
	// max(visual, voiceover). The stub narration is 12.5s, so the clip must
	// cover its scene (12.5s @ 30fps = 375 frames) for the canonical timeline
	// to validate — a shorter clip would legitimately demand a freeze tail.
	for i := range runner.textGen.(*stubTextGenerator).scenes {
		runner.textGen.(*stubTextGenerator).scenes[i].Clip = &ClipReference{
			ID: "clip-lineage", Path: clipPath, SHA256: hex.EncodeToString(sum[:]), FrameCount: 375, SourceInMS: 0, SourceOutMS: 12500,
		}
	}

	recorder := &recordingExecutionRecorder{}
	runner.SetExecutionRecorder(recorder)
	runner.SetCombinedAudioRenderer(lineageAudioRenderer{})
	runner.SetScriptPersistence(&recordingScriptPersistence{})
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.SaveToDB = true
	runID := "run-lineage-001"
	if err := repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}); err != nil {
		t.Fatal(err)
	}
	exec := ExecutionContext{RootJobID: "root-job-1", JobID: "job-lineage-1", ProjectID: "project-1", VideoID: "video-1", CorrelationID: "correlation-1", Attempt: 1}
	runner.ExecuteWithContext(context.Background(), runID, req, exec)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	if final.Status != RunStatusCompleted {
		t.Fatalf("run status = %s, error=%s", final.Status, final.ErrorMessage)
	}

	for _, got := range recorder.contexts {
		if got != exec {
			t.Fatalf("execution context was not propagated: got %+v want %+v", got, exec)
		}
	}
	if err := CertifyExecutionLineage(recorder.steps, recorder.inputs, recorder.outputs, []string{
		"NORMALIZE", "SCRIPT", "TRANSLATION", "VOICEOVER", "AUDIO_COMPILE", "PERSISTENCE", "DOCUMENT",
	}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.inputs) == 0 || recorder.inputs[0] != "clip-lineage" {
		t.Fatalf("input lineage = %v", recorder.inputs)
	}
	if len(recorder.outputs) < 2 {
		t.Fatalf("output lineage = %v", recorder.outputs)
	}
}

func TestCertifyExecutionLineageRejectsIncompleteTerminalState(t *testing.T) {
	started := time.Now().UTC()
	err := CertifyExecutionLineage([]ExecutionStep{{
		StepID: "job:script:script", Name: "SCRIPT", Type: "generation", Status: "RUNNING", StartedAt: started,
	}}, []string{"clip-1"}, []string{"audio-1"}, []string{"SCRIPT"})
	if err == nil || !strings.Contains(err.Error(), "RUNNING") {
		t.Fatalf("expected non-terminal lineage rejection, got %v", err)
	}
}

func TestExecutionRecorderFailureFailsRun(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runID := "run-recorder-failure-001"
	req := defaultTestRequest()
	if err := repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingExecutionRecorder{completeErr: fmt.Errorf("registry unavailable")}
	runner.SetExecutionRecorder(recorder)
	runner.ExecuteWithContext(context.Background(), runID, req, ExecutionContext{
		RootJobID: "root-recorder-failure", JobID: "job-recorder-failure", CorrelationID: "corr-recorder-failure",
	})
	final := awaitCompletion(t, repo, runID, time.Second)
	if final.Status != RunStatusFailed {
		t.Fatalf("run status = %s, want FAILED", final.Status)
	}
	if final.ErrorMessage == "" {
		t.Fatal("recorder failure was not persisted on the run")
	}
}

func TestExecutionContextRequiresCorrelationIdentity(t *testing.T) {
	if err := (ExecutionContext{JobID: "job", RootJobID: "root"}).Validate(); err == nil {
		t.Fatal("missing correlation ID must be rejected")
	}
	if NewExecutionContext("job", "correlation").CorrelationID != "correlation" {
		t.Fatal("explicit correlation ID was not preserved")
	}
}
