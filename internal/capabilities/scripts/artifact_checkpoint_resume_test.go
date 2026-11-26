package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

type crashAfterRenderEnqueuer struct {
	staged LocalizedRenderResult
	err    error
}

func (e *crashAfterRenderEnqueuer) EnqueueLocalizedRender(_ context.Context, in LocalizedRenderInput) error {
	if in.OnRenderReady != nil {
		if err := in.OnRenderReady(e.staged); err != nil {
			return err
		}
	}
	if e.err != nil {
		return e.err
	}
	return errors.New("simulated crash before upload")
}

type localizedResumeVerifier struct {
	status capcheckpoint.ArtifactStatus
	err    error
	calls  int
}

func (v *localizedResumeVerifier) VerifyArtifact(context.Context, string, string) (capcheckpoint.ArtifactStatus, error) {
	v.calls++
	return v.status, v.err
}

func localizedResumeInput(runID, sceneID, clipID string, lang Language) LocalizedRenderInput {
	return LocalizedRenderInput{
		RunID: runID, ParentJobID: runID, JobID: runID,
		SceneID: sceneID, SceneIndex: 0, Language: lang,
		Text: "caption for " + string(lang), SourceLanguage: "en", SourceText: "source caption",
		ClipID: clipID, ClipAssetID: clipID, ClipSHA256: strings.Repeat("c", 64), ClipDurationMS: 5000,
		Voiceover: AudioReference{ID: "vo-" + sceneID + "-" + string(lang), Duration: 5},
		// A resume fixture is a requested local render: the enqueue helper
		// must not treat it as the audio-only/timeline no-op path.
		Render: scriptpkg.VideoRenderSpec{Enabled: true},
	}
}

func localizedResumeCheckpoint(t *testing.T, in LocalizedRenderInput, rendered LocalizedRenderResult) ArtifactCheckpoint {
	t.Helper()
	key, err := ArtifactCheckpointKey(in.SceneID, in.ClipID, string(in.Language))
	require.NoError(t, err)
	payload, err := json.Marshal(rendered)
	require.NoError(t, err)
	return ArtifactCheckpoint{
		RunID: in.RunID, Kind: ArtifactCheckpointLocalizedRender, Key: key,
		SchemaVersion:    ArtifactCheckpointSchemaVersion,
		InputFingerprint: localizedRenderCheckpointFingerprint(in),
		ProcessorVersion: localizedRenderCheckpointProcessorVersion,
		ArtifactID:       rendered.AssetID, ArtifactSHA256: rendered.SHA256,
		ArtifactURI:       localizedRenderCheckpointURI(rendered),
		ArtifactSizeBytes: rendered.SizeBytes,
		Payload:           payload,
		Attempt:           1,
		CompletedAt:       time.Now().UTC(),
	}
}

func stagedResumeResult(t *testing.T, in LocalizedRenderInput, content []byte) LocalizedRenderResult {
	t.Helper()
	path := filepath.Join(t.TempDir(), in.ClipID+"."+string(in.Language)+".mp4")
	require.NoError(t, os.WriteFile(path, content, 0o644))
	return LocalizedRenderResult{
		SceneID: in.SceneID, SceneIndex: in.SceneIndex, Language: in.Language, ClipID: in.ClipID,
		RenderJobID: "render-job-" + in.ClipID, LocalPath: path,
		SHA256: digest.SHA256Bytes(content), SizeBytes: int64(len(content)), DurationMS: 5000,
		Status: "RENDERED",
	}
}

func uploadedResumeResult(in LocalizedRenderInput) LocalizedRenderResult {
	return LocalizedRenderResult{
		SceneID: in.SceneID, SceneIndex: in.SceneIndex, Language: in.Language, ClipID: in.ClipID,
		RenderJobID: "render-job-" + in.ClipID, AssetID: "cliprender_" + strings.Repeat("a", 24),
		SHA256: strings.Repeat("a", 64), SizeBytes: 4096, DurationMS: 5000,
		DriveFileID: "drive-" + in.ClipID, DriveLink: "https://drive.example/" + in.ClipID,
		ArtifactURL: "https://objects.example/" + strings.Repeat("a", 64),
		Status:      "UPLOADED",
	}
}

// TestLocalizedRenderCheckpointResumeSurvivesRunnerRestart is the crash/restart
// acceptance in hermetic form: the first runner writes the RENDERED checkpoint
// and then fails before upload; a NEW runner with the same store reuses the
// checkpoint bytes instead of dispatching a fresh render.
func TestLocalizedRenderCheckpointResumeSurvivesRunnerRestart(t *testing.T) {
	ctx := context.Background()
	artifactRepo := newMemoryArtifactCheckpointRepository()
	runRepo := newInMemRunRepository()
	in := localizedResumeInput("run-resume-restart", "scene-restart", "clip-restart", "it")
	require.NoError(t, runRepo.Create(ctx, &GenerationRun{ID: in.RunID, Result: &GenerateResult{}}))

	staged := stagedResumeResult(t, in, []byte("restart-staged-bytes"))
	first := NewRunner(runRepo, nil, nil, nil)
	first.SetLocalizedRenderEnqueuer(&crashAfterRenderEnqueuer{staged: staged})
	first.SetArtifactCheckpointRepository(artifactRepo)
	first.SetArtifactCheckpointDualWrite(true)
	result := &GenerateResult{}
	exec := NewExecutionContext(in.RunID, "restart-checkpoint")
	in.OnRenderReady = func(rendered LocalizedRenderResult) error {
		return first.recordLocalizedRenderReady(ctx, in.RunID, exec, result, rendered)
	}
	err := first.enqueueLocalizedRender(ctx, in)
	require.Error(t, err, "the simulated crash must fail the first attempt")

	key, err := ArtifactCheckpointKey(in.SceneID, in.ClipID, string(in.Language))
	require.NoError(t, err)
	checkpoint, err := artifactRepo.GetArtifactCheckpoint(ctx, in.RunID, ArtifactCheckpointLocalizedRender, key)
	require.NoError(t, err)
	require.NotNil(t, checkpoint, "the RENDERED write must land before the simulated crash")
	require.Equal(t, localizedRenderCheckpointFingerprint(in), checkpoint.InputFingerprint)
	require.Equal(t, "RENDERED", staged.Status)

	second := NewRunner(newInMemRunRepository(), nil, nil, nil)
	recovery := &recoveryLocalizedRenderEnqueuer{}
	second.SetLocalizedRenderEnqueuer(recovery)
	second.SetArtifactCheckpointRepository(artifactRepo)
	second.SetArtifactCheckpointResume(true)
	require.NoError(t, second.enqueueLocalizedRender(ctx, in))

	require.Len(t, recovery.recovered, 1, "the restarted runner must reuse the checkpoint")
	require.Empty(t, recovery.snapshot(), "the restarted runner must not dispatch a fresh render")
	require.Equal(t, staged.SHA256, recovery.recovered[0].SHA256)
}

// TestLocalizedRenderCheckpointResumeUsesStagedBytes proves the crash window
// between RENDERED and UPLOADED: a checkpoint whose local bytes still hash to
// the recorded SHA is published through the upload-only path, never re-rendered.
func TestLocalizedRenderCheckpointResumeUsesStagedBytes(t *testing.T) {
	ctx := context.Background()
	runner, _, _, _, _, _ := newTestRunner()
	artifactRepo := newMemoryArtifactCheckpointRepository()
	enq := &recoveryLocalizedRenderEnqueuer{}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetArtifactCheckpointRepository(artifactRepo)
	runner.SetArtifactCheckpointResume(true)

	in := localizedResumeInput("run-resume-staged", "scene-1", "clip-1", "it")
	rendered := stagedResumeResult(t, in, []byte("certified staged bytes"))
	require.NoError(t, artifactRepo.SaveArtifactCheckpoint(ctx, localizedResumeCheckpoint(t, in, rendered)))

	require.NoError(t, runner.enqueueLocalizedRender(ctx, in))
	require.Len(t, enq.recovered, 1, "the staged checkpoint must take the upload-only path")
	require.Empty(t, enq.snapshot(), "resume must not dispatch a fresh render")
	require.Equal(t, localizedRenderCheckpointFingerprint(in), enq.recovered[0].CheckpointFingerprint)
}

// TestLocalizedRenderCheckpointResumeSkipsUploadForPublishedArtifact proves
// the UPLOADED stage is resumable when the verifier confirms the bytes and the
// artifact verifier is consulted before any reuse.
func TestLocalizedRenderCheckpointResumeSkipsUploadForPublishedArtifact(t *testing.T) {
	ctx := context.Background()
	runner, _, _, _, _, _ := newTestRunner()
	artifactRepo := newMemoryArtifactCheckpointRepository()
	enq := &recoveryLocalizedRenderEnqueuer{}
	verifier := &localizedResumeVerifier{status: capcheckpoint.ArtifactStatus{Exists: true, SHA256Matches: true}}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetArtifactCheckpointRepository(artifactRepo)
	runner.SetArtifactCheckpointResume(true)
	runner.SetArtifactCheckpointVerifier(verifier)

	in := localizedResumeInput("run-resume-uploaded", "scene-2", "clip-2", "es")
	rendered := uploadedResumeResult(in)
	require.NoError(t, artifactRepo.SaveArtifactCheckpoint(ctx, localizedResumeCheckpoint(t, in, rendered)))

	require.NoError(t, runner.enqueueLocalizedRender(ctx, in))
	require.Len(t, enq.recovered, 1)
	require.Empty(t, enq.snapshot())
	require.Equal(t, 1, verifier.calls, "the published artifact must be verified before reuse")
}

// TestLocalizedRenderCheckpointResumeRejectsStaleInputs pins the input
// fingerprint boundary: a changed caption/voiceover/render identity is a
// definitive cache miss and the stale row is removed.
func TestLocalizedRenderCheckpointResumeRejectsStaleInputs(t *testing.T) {
	ctx := context.Background()
	runner, _, _, _, _, _ := newTestRunner()
	artifactRepo := newMemoryArtifactCheckpointRepository()
	enq := &recoveryLocalizedRenderEnqueuer{}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetArtifactCheckpointRepository(artifactRepo)
	runner.SetArtifactCheckpointResume(true)

	in := localizedResumeInput("run-resume-stale", "scene-3", "clip-3", "de")
	rendered := stagedResumeResult(t, in, []byte("stale staged bytes"))
	cp := localizedResumeCheckpoint(t, in, rendered)
	cp.InputFingerprint = strings.Repeat("f", 64)
	require.NoError(t, artifactRepo.SaveArtifactCheckpoint(ctx, cp))

	require.NoError(t, runner.enqueueLocalizedRender(ctx, in))
	require.Empty(t, enq.recovered, "a changed input fingerprint must not reuse the artifact")
	require.Len(t, enq.snapshot(), 1, "a changed input fingerprint must execute the unit")
	stored, err := artifactRepo.GetArtifactCheckpoint(ctx, in.RunID, ArtifactCheckpointLocalizedRender, cp.Key)
	require.NoError(t, err)
	require.Nil(t, stored, "definitive staleness must invalidate the row")
}

// TestLocalizedRenderCheckpointResumeRejectsProcessorVersion pins the
// processor-version boundary independently from the input fingerprint.
func TestLocalizedRenderCheckpointResumeRejectsProcessorVersion(t *testing.T) {
	ctx := context.Background()
	runner, _, _, _, _, _ := newTestRunner()
	artifactRepo := newMemoryArtifactCheckpointRepository()
	enq := &recoveryLocalizedRenderEnqueuer{}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetArtifactCheckpointRepository(artifactRepo)
	runner.SetArtifactCheckpointResume(true)

	in := localizedResumeInput("run-resume-version", "scene-4", "clip-4", "fr")
	rendered := stagedResumeResult(t, in, []byte("old processor bytes"))
	cp := localizedResumeCheckpoint(t, in, rendered)
	cp.ProcessorVersion = "localized-render/v1"
	require.NoError(t, artifactRepo.SaveArtifactCheckpoint(ctx, cp))

	require.NoError(t, runner.enqueueLocalizedRender(ctx, in))
	require.Empty(t, enq.recovered)
	require.Len(t, enq.snapshot(), 1)
	stored, err := artifactRepo.GetArtifactCheckpoint(ctx, in.RunID, ArtifactCheckpointLocalizedRender, cp.Key)
	require.NoError(t, err)
	require.Nil(t, stored)
}

// TestLocalizedRenderCheckpointResumeRejectsChangedStagedBytes proves a
// checkpoint whose local file no longer matches its SHA is not uploaded as if
// it were the certified render.
func TestLocalizedRenderCheckpointResumeRejectsChangedStagedBytes(t *testing.T) {
	ctx := context.Background()
	runner, _, _, _, _, _ := newTestRunner()
	artifactRepo := newMemoryArtifactCheckpointRepository()
	enq := &recoveryLocalizedRenderEnqueuer{}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetArtifactCheckpointRepository(artifactRepo)
	runner.SetArtifactCheckpointResume(true)

	in := localizedResumeInput("run-resume-corrupt-bytes", "scene-5", "clip-5", "pt")
	rendered := stagedResumeResult(t, in, []byte("original bytes"))
	require.NoError(t, os.WriteFile(rendered.LocalPath, []byte("tampered bytes"), 0o644))
	require.NoError(t, artifactRepo.SaveArtifactCheckpoint(ctx, localizedResumeCheckpoint(t, in, rendered)))

	require.NoError(t, runner.enqueueLocalizedRender(ctx, in))
	require.Empty(t, enq.recovered)
	require.Len(t, enq.snapshot(), 1)
}

// TestLocalizedRenderCheckpointResumeVerifierMissExecutesAndInvalidates makes
// a missing published artifact a definitive miss, not a silent reuse.
func TestLocalizedRenderCheckpointResumeVerifierMissExecutesAndInvalidates(t *testing.T) {
	ctx := context.Background()
	runner, _, _, _, _, _ := newTestRunner()
	artifactRepo := newMemoryArtifactCheckpointRepository()
	enq := &recoveryLocalizedRenderEnqueuer{}
	verifier := &localizedResumeVerifier{status: capcheckpoint.ArtifactStatus{Exists: false, SHA256Matches: false}}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetArtifactCheckpointRepository(artifactRepo)
	runner.SetArtifactCheckpointResume(true)
	runner.SetArtifactCheckpointVerifier(verifier)

	in := localizedResumeInput("run-resume-missing-artifact", "scene-6", "clip-6", "en")
	cp := localizedResumeCheckpoint(t, in, uploadedResumeResult(in))
	require.NoError(t, artifactRepo.SaveArtifactCheckpoint(ctx, cp))

	require.NoError(t, runner.enqueueLocalizedRender(ctx, in))
	require.Empty(t, enq.recovered)
	require.Len(t, enq.snapshot(), 1)
	stored, err := artifactRepo.GetArtifactCheckpoint(ctx, in.RunID, ArtifactCheckpointLocalizedRender, cp.Key)
	require.NoError(t, err)
	require.Nil(t, stored)
}

// TestLocalizedRenderCheckpointResumeVerifierErrorExecutesWithoutDeleting
// separates "cannot judge" from "definitely stale": the row survives so a
// later attempt can still reuse it.
func TestLocalizedRenderCheckpointResumeVerifierErrorExecutesWithoutDeleting(t *testing.T) {
	ctx := context.Background()
	runner, _, _, _, _, _ := newTestRunner()
	artifactRepo := newMemoryArtifactCheckpointRepository()
	enq := &recoveryLocalizedRenderEnqueuer{}
	verifier := &localizedResumeVerifier{err: errors.New("media SSOT unavailable")}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetArtifactCheckpointRepository(artifactRepo)
	runner.SetArtifactCheckpointResume(true)
	runner.SetArtifactCheckpointVerifier(verifier)

	in := localizedResumeInput("run-resume-verifier-error", "scene-7", "clip-7", "it")
	cp := localizedResumeCheckpoint(t, in, uploadedResumeResult(in))
	require.NoError(t, artifactRepo.SaveArtifactCheckpoint(ctx, cp))

	require.NoError(t, runner.enqueueLocalizedRender(ctx, in))
	require.Empty(t, enq.recovered)
	require.Len(t, enq.snapshot(), 1)
	stored, err := artifactRepo.GetArtifactCheckpoint(ctx, in.RunID, ArtifactCheckpointLocalizedRender, cp.Key)
	require.NoError(t, err)
	require.NotNil(t, stored, "an unjudgeable artifact must not be deleted")
}

// TestLocalizedRenderCheckpointResumeOffLeavesEnvelopePath proves the loader is
// opt-in: attaching the store without the resume flag still executes the unit.
func TestLocalizedRenderCheckpointResumeOffLeavesEnvelopePath(t *testing.T) {
	ctx := context.Background()
	runner, _, _, _, _, _ := newTestRunner()
	artifactRepo := newMemoryArtifactCheckpointRepository()
	enq := &recoveryLocalizedRenderEnqueuer{}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetArtifactCheckpointRepository(artifactRepo)

	in := localizedResumeInput("run-resume-off", "scene-8", "clip-8", "es")
	cp := localizedResumeCheckpoint(t, in, stagedResumeResult(t, in, []byte("staged bytes")))
	require.NoError(t, artifactRepo.SaveArtifactCheckpoint(ctx, cp))

	require.NoError(t, runner.enqueueLocalizedRender(ctx, in))
	require.Empty(t, enq.recovered)
	require.Len(t, enq.snapshot(), 1)
	stored, err := artifactRepo.GetArtifactCheckpoint(ctx, in.RunID, ArtifactCheckpointLocalizedRender, cp.Key)
	require.NoError(t, err)
	require.NotNil(t, stored, "resume-off must not mutate the store")
}
