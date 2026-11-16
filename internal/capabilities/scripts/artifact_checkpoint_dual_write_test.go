package scriptgeneration

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// memoryArtifactCheckpointRepository is the focused test double for the
// dual-write seam. It records the rows the runner writes without pulling the
// SQLite adapter (and its platform imports) into this package's tests.
type memoryArtifactCheckpointRepository struct {
	mu     sync.Mutex
	rows   map[string]ArtifactCheckpoint
	getErr error
}

func newMemoryArtifactCheckpointRepository() *memoryArtifactCheckpointRepository {
	return &memoryArtifactCheckpointRepository{rows: make(map[string]ArtifactCheckpoint)}
}

func artifactCheckpointMemoryKey(runID string, kind ArtifactCheckpointKind, key string) string {
	return runID + "\x00" + string(kind) + "\x00" + key
}

func (r *memoryArtifactCheckpointRepository) SaveArtifactCheckpoint(_ context.Context, checkpoint ArtifactCheckpoint) error {
	// Mirror the adapter-managed read-side values so the assertions below
	// exercise the same row shape the production store returns.
	if checkpoint.PayloadSHA256 == "" {
		checkpoint.PayloadSHA256 = checkpoint.PayloadDigest()
	}
	if checkpoint.CreatedAt.IsZero() {
		checkpoint.CreatedAt = time.Now().UTC()
	}
	checkpoint.UpdatedAt = time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[artifactCheckpointMemoryKey(checkpoint.RunID, checkpoint.Kind, checkpoint.Key)] = checkpoint
	return nil
}

func (r *memoryArtifactCheckpointRepository) GetArtifactCheckpoint(_ context.Context, runID string, kind ArtifactCheckpointKind, key string) (*ArtifactCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getErr != nil {
		return nil, r.getErr
	}
	checkpoint, ok := r.rows[artifactCheckpointMemoryKey(runID, kind, key)]
	if !ok {
		return nil, nil
	}
	clone := checkpoint
	return &clone, nil
}

func (r *memoryArtifactCheckpointRepository) ListArtifactCheckpoints(_ context.Context, runID string) ([]ArtifactCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ArtifactCheckpoint, 0, len(r.rows))
	for _, checkpoint := range r.rows {
		if checkpoint.RunID == runID {
			out = append(out, checkpoint)
		}
	}
	return out, nil
}

func (r *memoryArtifactCheckpointRepository) InvalidateArtifactCheckpoint(_ context.Context, runID string, kind ArtifactCheckpointKind, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rows, artifactCheckpointMemoryKey(runID, kind, key))
	return nil
}

func (r *memoryArtifactCheckpointRepository) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rows)
}

// TestLocalizedRenderDualWriteMatchesEnvelopeAtBothStages proves the rollout
// contract: the checkpoint body is byte-identical to the corresponding
// artifact in the whole-result envelope, first at RENDERED and then at
// UPLOADED, and the second write upserts the same identity instead of adding a
// second row.
func TestLocalizedRenderDualWriteMatchesEnvelopeAtBothStages(t *testing.T) {
	ctx := context.Background()
	const runID = "run-dual-write-localized-render"
	runRepo := newInMemRunRepository()
	require.NoError(t, runRepo.Create(ctx, &GenerationRun{ID: runID, Result: &GenerateResult{}}))

	artifactRepo := newMemoryArtifactCheckpointRepository()
	runner := NewRunner(runRepo, nil, nil, nil)
	runner.SetArtifactCheckpointRepository(artifactRepo)
	runner.SetArtifactCheckpointDualWrite(true)

	exec := NewExecutionContext(runID, "dual-write-idempotency")
	result := &GenerateResult{}
	staged := LocalizedRenderResult{
		SceneID:     "scene-1",
		Language:    "it",
		ClipID:      "clip-7",
		RenderJobID: "render-job-1",
		SHA256:      strings.Repeat("a", 64),
		LocalPath:   "/tmp/clip-7.mp4",
		Status:      "RENDERED",
	}
	staged.CheckpointFingerprint = localizedRenderCheckpointFingerprint(LocalizedRenderInput{
		SceneID: staged.SceneID, Language: staged.Language, ClipID: staged.ClipID,
		Text: "test caption", SourceLanguage: "en", SourceText: "source text",
		ClipAssetID: staged.ClipID, ClipSHA256: strings.Repeat("c", 64), ClipDurationMS: 5000,
		Voiceover: AudioReference{ID: "vo-1", Duration: 5},
	})
	require.NoError(t, runner.recordLocalizedRenderReady(ctx, runID, exec, result, staged))

	key, err := ArtifactCheckpointKey(staged.SceneID, staged.ClipID, string(staged.Language))
	require.NoError(t, err)
	checkpoint, err := artifactRepo.GetArtifactCheckpoint(ctx, runID, ArtifactCheckpointLocalizedRender, key)
	require.NoError(t, err)
	require.NotNil(t, checkpoint)

	envelope, err := runRepo.Get(ctx, runID)
	require.NoError(t, err)
	require.NotNil(t, envelope.Result)
	require.Len(t, envelope.Result.LocalizedRenderStaged, 1)
	stagedEnvelopeBody, err := json.Marshal(envelope.Result.LocalizedRenderStaged[0])
	require.NoError(t, err)
	require.Equal(t, stagedEnvelopeBody, []byte(checkpoint.Payload),
		"the RENDERED checkpoint body must be byte-identical to the envelope's staged artifact")
	require.Equal(t, checkpoint.PayloadDigest(), checkpoint.PayloadSHA256)
	require.Equal(t, localizedRenderCheckpointProcessorVersion, checkpoint.ProcessorVersion)
	require.NotEmpty(t, checkpoint.InputFingerprint)
	require.False(t, checkpoint.CompletedAt.IsZero())

	final := staged
	final.CheckpointFingerprint = staged.CheckpointFingerprint
	final.Status = "UPLOADED"
	final.AssetID = "asset-1"
	final.DriveFileID = "drive-1"
	final.DriveLink = "https://drive.example/drive-1"
	final.ArtifactURL = "cas://" + strings.Repeat("a", 64)
	final.SizeBytes = 4096
	final.DurationMS = 5000
	require.NoError(t, runner.recordLocalizedRender(ctx, runID, exec, result, final))

	checkpoint, err = artifactRepo.GetArtifactCheckpoint(ctx, runID, ArtifactCheckpointLocalizedRender, key)
	require.NoError(t, err)
	require.NotNil(t, checkpoint)
	require.Equal(t, 1, artifactRepo.count(), "the published write must upsert the staged identity")

	envelope, err = runRepo.Get(ctx, runID)
	require.NoError(t, err)
	require.NotNil(t, envelope.Result)
	require.Empty(t, envelope.Result.LocalizedRenderStaged)
	require.Len(t, envelope.Result.LocalizedRenders, 1)
	finalEnvelopeBody, err := json.Marshal(envelope.Result.LocalizedRenders[0])
	require.NoError(t, err)
	require.Equal(t, finalEnvelopeBody, []byte(checkpoint.Payload),
		"the UPLOADED checkpoint body must be byte-identical to the envelope's final artifact")
	require.Equal(t, final.AssetID, checkpoint.ArtifactID)
	require.Equal(t, final.SHA256, checkpoint.ArtifactSHA256)
	require.Equal(t, final.ArtifactURL, checkpoint.ArtifactURI)
	require.Equal(t, final.SizeBytes, checkpoint.ArtifactSizeBytes)
	require.Equal(t, final.DurationMS*1000, checkpoint.ArtifactDurationUS)
}

// TestLocalizedRenderDualWriteUsesDurableRunIDNotJobID pins the checkpoint
// foreign key to the run ledger identity. A broker job ID is not a run_id:
// using it makes SaveArtifactCheckpoint fail with FOREIGN KEY constraint failed
// (observed on the first live run) and silently drops the envelope partial
// write, so the writer must receive the durable run ID explicitly.
func TestLocalizedRenderDualWriteUsesDurableRunIDNotJobID(t *testing.T) {
	ctx := context.Background()
	const runID = "run-durable-identity"
	const jobID = "job-broker-identity"
	runRepo := newInMemRunRepository()
	require.NoError(t, runRepo.Create(ctx, &GenerationRun{ID: runID, JobID: jobID, Result: &GenerateResult{}}))

	artifactRepo := newMemoryArtifactCheckpointRepository()
	runner := NewRunner(runRepo, nil, nil, nil)
	runner.SetArtifactCheckpointRepository(artifactRepo)
	runner.SetArtifactCheckpointDualWrite(true)

	exec := ExecutionContext{RootJobID: runID, JobID: jobID, CorrelationID: "durable-identity", Attempt: 1}
	staged := LocalizedRenderResult{
		SceneID: "scene-identity", Language: "it", ClipID: "clip-identity",
		RenderJobID: "render-job-identity", SHA256: strings.Repeat("d", 64),
		LocalPath: "/tmp/clip-identity.mp4", Status: "RENDERED",
	}
	staged.CheckpointFingerprint = strings.Repeat("e", 64)
	require.NoError(t, runner.recordLocalizedRenderReady(ctx, runID, exec, &GenerateResult{}, staged))

	key, err := ArtifactCheckpointKey(staged.SceneID, staged.ClipID, string(staged.Language))
	require.NoError(t, err)
	checkpoint, err := artifactRepo.GetArtifactCheckpoint(ctx, runID, ArtifactCheckpointLocalizedRender, key)
	require.NoError(t, err)
	require.NotNil(t, checkpoint, "the checkpoint must be written under the durable run ID")
	orphan, err := artifactRepo.GetArtifactCheckpoint(ctx, jobID, ArtifactCheckpointLocalizedRender, key)
	require.NoError(t, err)
	require.Nil(t, orphan, "the broker job ID must never be used as a checkpoint run_id")

	envelope, err := runRepo.Get(ctx, runID)
	require.NoError(t, err)
	require.NotNil(t, envelope.Result)
	require.Len(t, envelope.Result.LocalizedRenderStaged, 1,
		"the envelope partial write must also target the durable run, not the job")
}

func TestLocalizedRenderDualWriteIsOffByDefault(t *testing.T) {
	ctx := context.Background()
	const runID = "run-dual-write-off"
	runRepo := newInMemRunRepository()
	require.NoError(t, runRepo.Create(ctx, &GenerationRun{ID: runID, Result: &GenerateResult{}}))

	artifactRepo := newMemoryArtifactCheckpointRepository()
	runner := NewRunner(runRepo, nil, nil, nil)
	// The repository is attached but the rollout flag is left at its zero
	// value: no per-artifact row may be written.
	runner.SetArtifactCheckpointRepository(artifactRepo)

	rendered := LocalizedRenderResult{
		SceneID:     "scene-1",
		Language:    "en",
		ClipID:      "clip-1",
		RenderJobID: "render-job-1",
		AssetID:     "asset-1",
		SHA256:      strings.Repeat("b", 64),
		DriveFileID: "drive-1",
		Status:      "UPLOADED",
	}
	require.NoError(t, runner.recordLocalizedRender(ctx, runID, NewExecutionContext(runID, "off"), &GenerateResult{}, rendered))
	require.Zero(t, artifactRepo.count(), "the dual-write must be disabled unless explicitly enabled")

	envelope, err := runRepo.Get(ctx, runID)
	require.NoError(t, err)
	require.NotNil(t, envelope.Result)
	require.Len(t, envelope.Result.LocalizedRenders, 1,
		"the legacy envelope write must remain active with the flag off")
}
