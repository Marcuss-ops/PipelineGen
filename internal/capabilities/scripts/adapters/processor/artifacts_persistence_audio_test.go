package processor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	script "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	filesystem "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

func TestPersistGeneratedArtifactsRequiresAndPublishesCertifiedFinalAudio(t *testing.T) {
	jobID := "audio-persistence-test"
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(os.TempDir(), "pipelinegen", "jobs", jobID)) })
	source := filepath.Join(t.TempDir(), "source.m4a")
	if err := os.WriteFile(source, []byte("certified-audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceSHA, err := job.ComputeSHA256(filesystem.NewOS(), source)
	if err != nil {
		t.Fatal(err)
	}
	result := &script.GenerationResult{
		ItemID: "item", AudioMode: "COMBINED_TIMELINE",
		FinalAudio: &script.FinalAudioArtifact{
			AssetID: "audio-1", Path: source, AudioContractVersion: "canonical-audio.v1",
			AudioPlanVersion: "compiled-audio-plan.v1", AudioPlanSHA256: "plan", FinalAudioSHA256: sourceSHA,
			Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo",
			Bitrate: 128000, DurationMS: 1000, FinalMix: true, CopyEligible: true,
		},
	}
	artifacts, err := PersistGeneratedArtifacts(context.Background(), jobID, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artifacts=%d, want script_json plus final_audio", len(artifacts))
	}
	var final job.Artifact
	for _, artifact := range artifacts {
		if artifact.Kind == job.ArtifactKindFinalAudio {
			final = artifact
		}
	}
	if final.Required != true || final.Filename != "final_audio.m4a" || final.SizeBytes == 0 || final.SHA256 == "" {
		t.Fatalf("final audio artifact was not certified: %+v", final)
	}
	if result.FinalAudio.Path != final.Path || result.FinalAudio.FinalAudioSHA256 != final.SHA256 {
		t.Fatalf("result final audio was not rebound to persisted artifact: %+v / %+v", result.FinalAudio, final)
	}
	if final.Path == source {
		t.Fatal("final audio must be materialized in the artifact directory, not left at the source path")
	}
	copied, readErr := os.ReadFile(final.Path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(copied) != "certified-audio" {
		t.Fatalf("persisted final audio bytes changed: %q", copied)
	}
}

func TestCopyCertifiedAudioOverwritesDestinationAndPreservesBytes(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.m4a")
	destination := filepath.Join(dir, "final_audio.m4a")
	payload := bytes.Repeat([]byte("certified-audio-"), 4096)
	if err := os.WriteFile(source, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyCertifiedAudio(source, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("copied final audio differs from the certified source (%d vs %d bytes)", len(got), len(payload))
	}
}

func TestPersistGeneratedArtifactsFailsClosedWhenCombinedAudioIsMissing(t *testing.T) {
	_, err := PersistGeneratedArtifacts(context.Background(), "audio-missing-test", &script.GenerationResult{AudioMode: "COMBINED_TIMELINE"}, nil)
	if err == nil {
		t.Fatal("combined generation without certified final audio must fail")
	}
}

func TestPersistGeneratedArtifactsRejectsReplacedFinalAudio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "final.m4a")
	if err := os.WriteFile(path, []byte("replaced"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := &script.GenerationResult{AudioMode: "COMBINED_TIMELINE", FinalAudio: &script.FinalAudioArtifact{
		Path: path, AudioContractVersion: "canonical-audio.v1", AudioPlanVersion: "compiled-audio-plan.v1", AudioPlanSHA256: "plan", FinalAudioSHA256: "old-hash",
		Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo", DurationMS: 1000, Bitrate: 128000, FinalMix: true, CopyEligible: true,
	}}
	if _, err := PersistGeneratedArtifacts(context.Background(), "audio-replaced-test", result, nil); err == nil {
		t.Fatal("replaced final audio must fail hash certification")
	}
}
