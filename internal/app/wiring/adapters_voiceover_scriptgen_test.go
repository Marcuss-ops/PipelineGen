// Package wiring — adapters_voiceover_scriptgen_test.go
//
// Tests for ScriptVoiceoverGenerator, the scriptgeneration.VoiceoverGenerator
// adapter that routes scene text through the canonical per-item voiceover
// pipeline (voiceover.VoiceoverItemExecutor) and maps the result onto
// AudioReference — crucially carrying the full SpeechTimingArtifact so the
// runner can derive the phrase→timestamp projection from the same bytes as
// the audio.
package wiring

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// stubScriptVOExecutor implements voiceover.VoiceoverItemExecutor by
// capturing the command it receives and returning a pre-configured result.
type stubScriptVOExecutor struct {
	result *voiceover.VoiceoverItemResult
	err    error
	gotCmd *voiceover.GenerateVoiceoverItemCommand
}

func (s *stubScriptVOExecutor) Execute(_ context.Context, item *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	s.gotCmd = item
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

var _ voiceover.VoiceoverItemExecutor = (*stubScriptVOExecutor)(nil)

// validTimingArtifact returns a minimal, valid canonical artifact bound to
// two words.
func validTimingArtifact() *capabilityaudio.SpeechTimingArtifact {
	artifact, err := capabilityaudio.BuildSpeechTimingArtifact(
		"edge-tts", "en", "en-US-TestNeural",
		"1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222",
		1_000_000,
		[]capabilityaudio.SpeechWordTiming{
			{Index: 0, Text: "Hello", StartUS: 0, EndUS: 400_000},
			{Index: 1, Text: "world", StartUS: 500_000, EndUS: 900_000},
		},
	)
	if err != nil {
		panic(err)
	}
	return artifact
}

// TestScriptVoiceoverGenerator_CarriesTimingArtifact pins the core wiring:
// when the per-item pipeline returns a completed result with a timing
// artifact, Generate must surface it on AudioReference.Timing so the
// runner's phrase-timing compiler has the word-level SSOT.
func TestScriptVoiceoverGenerator_CarriesTimingArtifact(t *testing.T) {
	artifact := validTimingArtifact()
	exec := &stubScriptVOExecutor{
		result: &voiceover.VoiceoverItemResult{
			Status:        voiceover.StatusCompleted,
			LegacyFileMD5: "e2e-content-hash",
			DriveLink:     "https://drive.google.com/file/d/vo-1/view",
			LocalPath:     "/tmp/out/scene_1_en.mp3",
			CleanedPath:   "",
			DurationMs:    1000,
			Timing: &voiceover.VoiceoverTimingResult{
				Status:   voiceover.TimingStatusCompleted,
				Artifact: artifact,
			},
		},
	}

	gen := NewScriptVoiceoverGenerator(exec, "/tmp/out", nil)
	ref, err := gen.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:  "1",
		Language: "en",
		Text:     "Hello world",
	})
	require.NoError(t, err)

	assert.Equal(t, "e2e-content-hash", ref.ID)
	assert.Equal(t, "https://drive.google.com/file/d/vo-1/view", ref.URL)
	assert.Equal(t, "/tmp/out/scene_1_en.mp3", ref.FilePath)
	assert.Equal(t, 1.0, ref.Duration)
	require.NotNil(t, ref.Timing)
	assert.Same(t, artifact, ref.Timing)
	assert.Len(t, ref.Timing.Words, 2)
}

// TestScriptVoiceoverGenerator_CarriesTimingBundle pins the summary
// mapping: the published timing bundle references (timing.json SSOT +
// optional SRT/VTT links + hashes) must land on AudioReference.TimingBundle
// so the document renderer can surface the original timing links.
func TestScriptVoiceoverGenerator_CarriesTimingBundle(t *testing.T) {
	artifact := validTimingArtifact()
	exec := &stubScriptVOExecutor{
		result: &voiceover.VoiceoverItemResult{
			Status:        voiceover.StatusCompleted,
			LegacyFileMD5: "e2e-content-hash",
			DriveLink:     "https://drive.google.com/file/d/vo-1/view",
			LocalPath:     "/tmp/out/scene_1_en.mp3",
			DurationMs:    1000,
			Timing: &voiceover.VoiceoverTimingResult{
				Status:       voiceover.TimingStatusCompleted,
				JSONLink:     "https://drive.google.com/file/d/timing-json/view",
				SRTLink:      "https://drive.google.com/file/d/timing-srt/view",
				VTTLink:      "https://drive.google.com/file/d/timing-vtt/view",
				BoundaryMode: "word",
				WordCount:    2,
				DurationUS:   1_000_000,
				TextSHA256:   "text-hash",
				AudioSHA256:  "audio-hash",
				Artifact:     artifact,
			},
		},
	}

	gen := NewScriptVoiceoverGenerator(exec, "/tmp/out", nil)
	ref, err := gen.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:  "1",
		Language: "en",
		Text:     "Hello world",
	})
	require.NoError(t, err)

	require.NotNil(t, ref.TimingBundle, "timing bundle must be carried for the document")
	assert.Equal(t, "completed", ref.TimingBundle.Status)
	assert.Equal(t, "https://drive.google.com/file/d/timing-json/view", ref.TimingBundle.JSONLink)
	assert.Equal(t, "https://drive.google.com/file/d/timing-srt/view", ref.TimingBundle.SRTLink)
	assert.Equal(t, "https://drive.google.com/file/d/timing-vtt/view", ref.TimingBundle.VTTLink)
	assert.Equal(t, "word", ref.TimingBundle.BoundaryMode)
	assert.Equal(t, 2, ref.TimingBundle.WordCount)
	assert.Equal(t, int64(1_000_000), ref.TimingBundle.DurationUS)
	assert.Equal(t, "text-hash", ref.TimingBundle.TextSHA256)
	assert.Equal(t, "audio-hash", ref.TimingBundle.AudioSHA256)
}

// TestScriptVoiceoverGenerator_NoTimingLeavesNil pins the graceful
// degradation: a completed result without a timing artifact yields an
// AudioReference with nil Timing (never a fabricated artifact).
func TestScriptVoiceoverGenerator_NoTimingLeavesNil(t *testing.T) {
	exec := &stubScriptVOExecutor{
		result: &voiceover.VoiceoverItemResult{
			Status:        voiceover.StatusCompleted,
			LegacyFileMD5: "hash-no-timing",
			LocalPath:     "/tmp/out/scene_1_en.mp3",
			DurationMs:    800,
		},
	}

	gen := NewScriptVoiceoverGenerator(exec, "/tmp/out", nil)
	ref, err := gen.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:  "1",
		Language: "en",
		Text:     "Hello world",
	})
	require.NoError(t, err)
	assert.Nil(t, ref.Timing)
	assert.Equal(t, 0.8, ref.Duration)
}

// TestScriptVoiceoverGenerator_CommandShape pins the command the adapter
// builds: deterministic filename, computed text hash, forwarded timing
// policy, and RemoveSilence=false (raw boundaries must describe the exact
// produced bytes — no stale pre-trim timestamps).
func TestScriptVoiceoverGenerator_CommandShape(t *testing.T) {
	exec := &stubScriptVOExecutor{
		result: &voiceover.VoiceoverItemResult{
			Status:        voiceover.StatusCompleted,
			LegacyFileMD5: "hash",
			LocalPath:     "/tmp/out/scene_My_Scene_en.mp3",
			DurationMs:    500,
		},
	}

	gen := NewScriptVoiceoverGenerator(exec, "/tmp/out", nil)
	_, err := gen.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:  "My Scene",
		Language: "en",
		Text:     "Hello world",
	})
	require.NoError(t, err)
	require.NotNil(t, exec.gotCmd)

	assert.Equal(t, "scene_My_Scene_en.mp3", exec.gotCmd.Filename)
	assert.Equal(t, "Hello world", exec.gotCmd.Text)
	assert.Equal(t, voiceover.Language("en"), exec.gotCmd.Language)
	assert.Equal(t, voiceover.ComputeTextHash("Hello world"), exec.gotCmd.TextHash)
	assert.False(t, exec.gotCmd.RemoveSilence,
		"RemoveSilence must be false so raw Edge boundaries describe the exact produced bytes")
	require.NotNil(t, exec.gotCmd.Timing)
	assert.Equal(t, capabilityaudio.TimingBestEffort, exec.gotCmd.Timing.Mode)
	assert.Equal(t, capabilityaudio.BoundaryWord, exec.gotCmd.Timing.BoundaryMode)
}

// TestScriptVoiceoverGenerator_ForwardsProject pins the P0 routing-context
// fix: input.Project MUST be forwarded to the per-item command's Project
// field. Dropping it here was the gap that made the publisher fail
// (ErrVoiceoverPublishProjectRequired) AFTER TTS work was already spent.
func TestScriptVoiceoverGenerator_ForwardsProject(t *testing.T) {
	exec := &stubScriptVOExecutor{
		result: &voiceover.VoiceoverItemResult{
			Status:        voiceover.StatusCompleted,
			LegacyFileMD5: "hash",
			LocalPath:     "/tmp/out/scene_1_en.mp3",
			DurationMs:    500,
		},
	}

	gen := NewScriptVoiceoverGenerator(exec, "/tmp/out", nil)
	_, err := gen.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:  "1",
		Language: "en",
		Text:     "Hello world",
		Project:  "request-project",
	})
	require.NoError(t, err)
	require.NotNil(t, exec.gotCmd)
	assert.Equal(t, "request-project", exec.gotCmd.Project,
		"input.Project must be forwarded verbatim to GenerateVoiceoverItemCommand.Project")
}

// TestScriptVoiceoverGenerator_JobUniqueFilename pins the collision fix:
// concurrent script.generate jobs share one voiceover output directory and
// their per-job scene IDs are sequential ("scene-0"), so the semantic
// project namespace MUST be folded into the local filename to avoid two
// jobs corrupting each other's audio/metadata.
func TestScriptVoiceoverGenerator_JobUniqueFilename(t *testing.T) {
	exec := &stubScriptVOExecutor{
		result: &voiceover.VoiceoverItemResult{
			Status:        voiceover.StatusCompleted,
			LegacyFileMD5: "hash",
			LocalPath:     "/tmp/out/scene_project_scene-0_it.mp3",
			DurationMs:    500,
		},
	}

	gen := NewScriptVoiceoverGenerator(exec, "/tmp/out", nil)
	_, err := gen.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:  "scene-0",
		Language: "it",
		Text:     "Ciao mondo",
		Project:  "comici-sandler",
	})
	require.NoError(t, err)
	require.NotNil(t, exec.gotCmd)
	assert.Equal(t, "scene_comici-sandler_scene-0_it.mp3", exec.gotCmd.Filename,
		"the project namespace must make the voiceover filename job-unique")
}

// TestScriptVoiceoverGenerator_ForwardsExplicitDestination pins the
// caller-explicit voiceover folder threading: input.VoiceoverFolderID
// (resolved from output.voiceover_folder_id by the routing context) MUST be
// forwarded as an explicit Destination on the per-item command. Dropping it
// made scriptgen voiceover fail with VOICEOVER_DESTINATION_UNAVAILABLE on
// servers without a configured voiceover_root_folder, even when the caller
// supplied a folder.
func TestScriptVoiceoverGenerator_ForwardsExplicitDestination(t *testing.T) {
	exec := &stubScriptVOExecutor{
		result: &voiceover.VoiceoverItemResult{
			Status:        voiceover.StatusCompleted,
			LegacyFileMD5: "hash",
			LocalPath:     "/tmp/out/scene_1_en.mp3",
			DurationMs:    500,
		},
	}

	gen := NewScriptVoiceoverGenerator(exec, "/tmp/out", nil)
	_, err := gen.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:           "1",
		Language:          "en",
		Text:              "Hello world",
		Project:           "request-project",
		VoiceoverFolderID: "explicit-folder-id",
	})
	require.NoError(t, err)
	require.NotNil(t, exec.gotCmd)
	require.NotNil(t, exec.gotCmd.Destination,
		"caller-explicit voiceover folder must produce an explicit Destination")
	assert.Equal(t, string(voiceover.KindExplicit), exec.gotCmd.Destination.Kind)
	assert.Equal(t, "explicit-folder-id", exec.gotCmd.Destination.FolderID)
	assert.Equal(t, "request-project", exec.gotCmd.Destination.Project)
}

// TestScriptVoiceoverGenerator_NilDestinationWithoutFolder pins the
// fallback contract: an empty VoiceoverFolderID leaves Destination nil so
// the per-item pipeline resolves the configured voiceover folder via its
// DefaultFolderResolver.
func TestScriptVoiceoverGenerator_NilDestinationWithoutFolder(t *testing.T) {
	exec := &stubScriptVOExecutor{
		result: &voiceover.VoiceoverItemResult{
			Status:        voiceover.StatusCompleted,
			LegacyFileMD5: "hash",
			LocalPath:     "/tmp/out/scene_1_en.mp3",
			DurationMs:    500,
		},
	}

	gen := NewScriptVoiceoverGenerator(exec, "/tmp/out", nil)
	_, err := gen.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:  "1",
		Language: "en",
		Text:     "Hello world",
	})
	require.NoError(t, err)
	assert.Nil(t, exec.gotCmd.Destination,
		"empty VoiceoverFolderID must leave Destination nil (config default applies)")
}

// TestScriptVoiceoverGenerator_FailedPipelineFailsClosed pins the
// fail-closed contract: a non-completed pipeline result surfaces an error
// instead of returning a plausible AudioReference.
func TestScriptVoiceoverGenerator_FailedPipelineFailsClosed(t *testing.T) {
	exec := &stubScriptVOExecutor{
		result: &voiceover.VoiceoverItemResult{
			Status: voiceover.StatusFailed,
			Error:  "timing required + remove_silence: no edit map",
		},
	}

	gen := NewScriptVoiceoverGenerator(exec, "/tmp/out", nil)
	_, err := gen.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:  "1",
		Language: "en",
		Text:     "Hello world",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status failed")
}

// TestScriptVoiceoverGenerator_ExecutorErrorFailsClosed pins the transport
// error path: an executor error propagates as a generation error.
func TestScriptVoiceoverGenerator_ExecutorErrorFailsClosed(t *testing.T) {
	exec := &stubScriptVOExecutor{err: context.DeadlineExceeded}

	gen := NewScriptVoiceoverGenerator(exec, "/tmp/out", nil)
	_, err := gen.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:  "1",
		Language: "en",
		Text:     "Hello world",
	})
	require.Error(t, err)
}
