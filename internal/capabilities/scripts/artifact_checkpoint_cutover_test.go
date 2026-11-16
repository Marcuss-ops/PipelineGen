package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	"github.com/stretchr/testify/require"
)

// countingRunRepository counts whole-result saves without changing the
// in-memory semantics of the shared runner fixture. The cutover assertion is
// about writes that did NOT happen, so a counter is the contract here.
type countingRunRepository struct {
	*inMemRunRepository
	saves int
}

func (r *countingRunRepository) SavePartialResult(ctx context.Context, runID string, result *GenerateResult) error {
	r.saves++
	return r.inMemRunRepository.SavePartialResult(ctx, runID, result)
}

func cutoverRunner(t *testing.T, runRepo *countingRunRepository, artifactRepo ArtifactCheckpointRepository) *Runner {
	t.Helper()
	runner := NewRunner(runRepo, nil, nil, nil)
	runner.SetPhraseAligner(testPhraseAligner{})
	runner.SetArtifactCheckpointRepository(artifactRepo)
	runner.SetArtifactCheckpointDualWrite(true)
	runner.SetArtifactCheckpointResume(true)
	runner.SetArtifactCheckpointVerifier(&localizedResumeVerifier{
		status: capcheckpoint.ArtifactStatus{Exists: true, SHA256Matches: true},
	})
	runner.SetArtifactCheckpointCutover(true)
	return runner
}

// TestLocalizedRenderCutoverUsesCheckpointInsteadOfPerUnitEnvelope is the
// phase-5 contract: while a unit is certified, the per-artifact checkpoint is
// written and the whole-result envelope is NOT rewritten. The next stage
// boundary (checkpoint) still persists the full result, so the cutover removes
// the per-unit amplification without removing crash recovery.
func TestLocalizedRenderCutoverUsesCheckpointInsteadOfPerUnitEnvelope(t *testing.T) {
	ctx := context.Background()
	const runID = "run-cutover-localized-render"
	inner := newInMemRunRepository()
	require.NoError(t, inner.Create(ctx, &GenerationRun{ID: runID, Result: &GenerateResult{}}))
	runRepo := &countingRunRepository{inMemRunRepository: inner}
	artifactRepo := newMemoryArtifactCheckpointRepository()
	runner := cutoverRunner(t, runRepo, artifactRepo)

	exec := NewExecutionContext(runID, "cutover")
	result := &GenerateResult{}
	staged := LocalizedRenderResult{
		SceneID: "scene-1", Language: "it", ClipID: "clip-1",
		RenderJobID: "render-job-1", SHA256: strings.Repeat("a", 64),
		LocalPath: "/tmp/clip-1.mp4", Status: "RENDERED",
		CheckpointFingerprint: strings.Repeat("f", 64),
	}
	require.NoError(t, runner.recordLocalizedRenderReady(ctx, runID, exec, result, staged))
	require.Zero(t, runRepo.saves, "RENDERED must not rewrite the whole-result envelope during cutover")
	require.Equal(t, 1, artifactRepo.count())

	envelope, err := inner.Get(ctx, runID)
	require.NoError(t, err)
	require.NotNil(t, envelope.Result)
	require.Empty(t, envelope.Result.LocalizedRenderStaged,
		"the per-unit checkpoint, not the envelope, owns the RENDERED artifact during cutover")

	final := staged
	final.Status = "UPLOADED"
	final.AssetID = "drive-clip-1"
	final.DriveFileID = "drive-clip-1"
	final.DriveLink = "https://drive.example/clip-1"
	final.ArtifactURL = "cas://" + strings.Repeat("a", 64)
	require.NoError(t, runner.recordLocalizedRender(ctx, runID, exec, result, final))
	require.Zero(t, runRepo.saves, "UPLOADED must not rewrite the whole-result envelope during cutover")
	require.Equal(t, 1, artifactRepo.count(), "the published write must upsert the staged identity")

	// The final-job fixed-media path uses the same per-unit recorder and must
	// obey the same cutover.
	fixed := LocalizedRenderResult{
		SceneID: "scene-fixed", Language: "it", ClipID: "clip-fixed",
		AssetID: "drive-clip-fixed", SHA256: strings.Repeat("b", 64),
		DriveFileID: "drive-clip-fixed", DriveLink: "https://drive.example/clip-fixed",
		ArtifactURL: "cas://" + strings.Repeat("b", 64), Status: "UPLOADED",
		CheckpointFingerprint: strings.Repeat("e", 64),
	}
	require.NoError(t, runner.recordFinalJobFixedMediaRender(ctx, runID, result, fixed))
	require.Zero(t, runRepo.saves, "final-job fragments must not rewrite the envelope during cutover")
	require.Equal(t, 2, artifactRepo.count())

	envelope, err = inner.Get(ctx, runID)
	require.NoError(t, err)
	require.Empty(t, envelope.Result.LocalizedRenders)

	// A stage boundary is still the whole-result owner.
	require.NoError(t, runner.checkpoint(ctx, runID, result))
	require.Equal(t, 1, runRepo.saves)
	envelope, err = inner.Get(ctx, runID)
	require.NoError(t, err)
	require.Len(t, envelope.Result.LocalizedRenders, 2)
}

// TestLocalizedRenderCutoverRequiresTrustedCheckpoint pins the fail-safe:
// if any prerequisite is missing, the envelope remains the recovery copy and
// the unit keeps writing it. A partial cutover would remove the only copy.
func TestLocalizedRenderCutoverRequiresTrustedCheckpoint(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*Runner, *countingRunRepository)
		wantRows  int
		wantSaves int
	}{
		{
			name: "resume disabled",
			mutate: func(runner *Runner, _ *countingRunRepository) {
				runner.SetArtifactCheckpointResume(false)
			},
			wantRows: 1, wantSaves: 1,
		},
		{
			name: "verifier missing",
			mutate: func(runner *Runner, _ *countingRunRepository) {
				runner.SetArtifactCheckpointVerifier(nil)
			},
			wantRows: 1, wantSaves: 1,
		},
		{
			name: "dual-write disabled",
			mutate: func(runner *Runner, _ *countingRunRepository) {
				runner.SetArtifactCheckpointDualWrite(false)
			},
			wantRows: 0, wantSaves: 1,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			runID := "run-cutover-prerequisite-" + strings.ReplaceAll(tc.name, " ", "-")
			inner := newInMemRunRepository()
			require.NoError(t, inner.Create(ctx, &GenerationRun{ID: runID, Result: &GenerateResult{}}))
			runRepo := &countingRunRepository{inMemRunRepository: inner}
			artifactRepo := newMemoryArtifactCheckpointRepository()
			runner := cutoverRunner(t, runRepo, artifactRepo)
			tc.mutate(runner, runRepo)

			rendered := LocalizedRenderResult{
				SceneID: "scene-1", Language: "it", ClipID: "clip-1",
				SHA256: strings.Repeat("a", 64), LocalPath: "/tmp/clip-1.mp4",
				Status: "RENDERED", CheckpointFingerprint: strings.Repeat("f", 64),
			}
			require.NoError(t, runner.recordLocalizedRenderReady(ctx, runID, NewExecutionContext(runID, "prereq"), &GenerateResult{}, rendered))
			require.Equal(t, tc.wantRows, artifactRepo.count())
			require.Equal(t, tc.wantSaves, runRepo.saves,
				"a cutover without every trusted prerequisite must keep writing the envelope")
		})
	}
}
