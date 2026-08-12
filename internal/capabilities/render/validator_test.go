package render

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func validPlanForValidator(t *testing.T, finalAudio *FinalAudioAsset) RenderPlan {
	t.Helper()
	path := t.TempDir() + "/clip.mp4"
	contents := []byte("validator clip")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 1_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene", Index: 0, DurationUS: 1_000_000,
			Video: audio.VideoSegment{AssetID: "clip", SourceInUS: 0, SourceDurationUS: 1_000_000},
			Audio: audio.AudioIntent{Mode: audio.AudioSilence},
		}},
	}
	plan, err := Compile(CompileInput{
		JobID: "job-validator", Revision: "rev-1", OutputPath: t.TempDir() + "/final.mp4", FPS: 30,
		Timeline: timeline, FinalAudio: finalAudio,
		Manifest: []AssetManifestEntry{{AssetID: "clip", Path: path, SHA256: hex.EncodeToString(sum[:]), FrameCount: 30}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if finalAudio != nil {
		finalAudio.AssetKind = "final_audio"
		finalAudio.Strategy = string(audio.FinalAudioCopy)
		finalAudio.AudioContractVersion = audio.AudioContractVersion
		finalAudio.AudioPlanVersion = audio.AudioPlanVersion
		finalAudio.Codec = "aac"
		finalAudio.Profile = "LC"
		finalAudio.SampleRate = 48000
		finalAudio.Channels = 2
		finalAudio.ChannelLayout = "stereo"
		finalAudio.DurationMS = 1000
		finalAudio.StartPTS = 0
		info, err := os.Stat(finalAudio.Path)
		if err != nil {
			t.Fatal(err)
		}
		finalAudio.SizeBytes = info.Size()
		finalAudio.FinalMix = true
		finalAudio.CopyEligible = true
		if err := plan.Seal(); err != nil {
			t.Fatal(err)
		}
	}
	return plan
}

func TestRenderPlanValidatorMintsImmutableValidatedPlan(t *testing.T) {
	plan := validPlanForValidator(t, nil)
	validated, err := ValidateRenderPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	originalHash := validated.Plan().PlanSHA256
	plan.Manifest[0].Path = "/tampered"
	plan.VideoTracks[0].Segments[0].Timeline.StartFrame = 99
	copyPlan := validated.Plan()
	if copyPlan.PlanSHA256 != originalHash || copyPlan.Manifest[0].Path == "/tampered" || copyPlan.VideoTracks[0].Segments[0].Timeline.StartFrame == 99 {
		t.Fatal("validated handoff changed after source plan mutation")
	}
	wire, err := validated.MarshalJSON()
	if err != nil || !strings.Contains(string(wire), `"plan_sha256"`) {
		t.Fatalf("validated wire payload invalid: %s (%v)", wire, err)
	}
}

func TestRenderPlanValidatorRejectsMissingFilesAndMissingVisuals(t *testing.T) {
	plan := validPlanForValidator(t, nil)
	plan.Manifest[0].Path = "/does/not/exist"
	if _, err := ValidateRenderPlan(plan); err == nil {
		t.Fatal("missing manifest file must be rejected before handoff")
	}

	timeline := audio.CanonicalTimeline{Version: audio.TimelineVersion, DurationUS: 1_000_000, Segments: []audio.TimelineSegment{{ID: "scene", Index: 0, DurationUS: 1_000_000, Audio: audio.AudioIntent{Mode: audio.AudioSilence}}}}
	noVideo, err := Compile(CompileInput{JobID: "job-no-video", Revision: "rev-1", OutputPath: "final.mp4", FPS: 30, Timeline: timeline})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRenderPlan(noVideo); err == nil {
		t.Fatal("render handoff without a visual segment must be rejected")
	}
}

func TestRenderPlanValidatorRequiresCertifiedAudioPlanForFinalAudioCopy(t *testing.T) {
	audioPath := t.TempDir() + "/final_audio.m4a"
	contents := []byte("certified audio")
	if err := os.WriteFile(audioPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	plan := validPlanForValidator(t, &FinalAudioAsset{AssetID: "final-audio", Path: audioPath, SHA256: hex.EncodeToString(sum[:])})
	if _, err := ValidateRenderPlan(plan); err == nil {
		t.Fatal("FINAL_AUDIO_COPY without an audio plan SHA must be rejected")
	}

	plan.FinalAudio.PlanSHA256 = strings.Repeat("a", 64)
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRenderPlan(plan); err != nil {
		t.Fatalf("certified final audio should validate: %v", err)
	}

	tampered := plan
	audioMetadata := *plan.FinalAudio
	audioMetadata.SizeBytes++
	tampered.FinalAudio = &audioMetadata
	if err := tampered.Seal(); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRenderPlan(tampered); err == nil {
		t.Fatal("final audio size metadata mismatch must be rejected")
	}

	if err := os.WriteFile(audioPath, []byte("corrupted audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRenderPlan(plan); err == nil {
		t.Fatal("corrupted final audio must be rejected before executor handoff")
	}
}
