package stockpipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
)

type durableStateFailureRepo struct {
	failOn string
}

func (r *durableStateFailureRepo) failure(operation string) error {
	if r.failOn == operation {
		return errors.New("injected " + operation + " failure")
	}
	return nil
}

func (r *durableStateFailureRepo) CreateBatch(ctx context.Context, batch *StockBatch) error {
	return r.failure("CreateBatch")
}
func (r *durableStateFailureRepo) GetBatch(ctx context.Context, id string) (*StockBatch, error) {
	return nil, nil
}
func (r *durableStateFailureRepo) UpdateBatchStatus(ctx context.Context, id string, status BatchState, lastError string) error {
	return nil
}
func (r *durableStateFailureRepo) CreateGroup(ctx context.Context, group *StockBatchGroup) error {
	return r.failure("CreateGroup")
}
func (r *durableStateFailureRepo) GetGroup(ctx context.Context, id string) (*StockBatchGroup, error) {
	return nil, nil
}
func (r *durableStateFailureRepo) UpdateGroupStatus(ctx context.Context, id string, status GroupState, lastError string) error {
	return nil
}
func (r *durableStateFailureRepo) ListGroups(ctx context.Context, batchID string) ([]StockBatchGroup, error) {
	return nil, nil
}
func (r *durableStateFailureRepo) CreateArtifact(ctx context.Context, artifact *StockArtifact) error {
	return r.failure("CreateArtifact")
}
func (r *durableStateFailureRepo) GetArtifact(ctx context.Context, id string) (*StockArtifact, error) {
	return nil, nil
}
func (r *durableStateFailureRepo) MarkArtifactExtracting(ctx context.Context, id string) error {
	return r.failure("MarkArtifactExtracting")
}
func (r *durableStateFailureRepo) MarkArtifactExtracted(ctx context.Context, id, localPath, sha256 string, actualDurationMs int) error {
	return r.failure("MarkArtifactExtracted")
}
func (r *durableStateFailureRepo) MarkArtifactPublished(ctx context.Context, id, driveFileID, driveFolderID, driveLink string) error {
	return nil
}
func (r *durableStateFailureRepo) MarkArtifactVerified(ctx context.Context, id string) error {
	return nil
}
func (r *durableStateFailureRepo) MarkArtifactFailed(ctx context.Context, id string, status ArtifactState, lastError string) error {
	return r.failure("MarkArtifactFailed")
}
func (r *durableStateFailureRepo) MarkGroupSucceeded(ctx context.Context, id string, verifiedClips int) error {
	return nil
}
func (r *durableStateFailureRepo) MarkBatchSucceeded(ctx context.Context, id string, verifiedClips int) error {
	return nil
}
func (r *durableStateFailureRepo) FindIncompleteArtifacts(ctx context.Context, groupID string, maxAttempts int) ([]StockArtifact, error) {
	return nil, nil
}

var _ StockBatchRepository = (*durableStateFailureRepo)(nil)

type durableStateRunner struct {
	*fakeStepRunner
	repo   StockBatchRepository
	cutter VideoCutter
}

func (r *durableStateRunner) BatchRepository() StockBatchRepository {
	return r.repo
}

func (r *durableStateRunner) Cutter() VideoCutter {
	return r.cutter
}

var _ StepRunner = (*durableStateRunner)(nil)

func newDurableStateRunner(repo StockBatchRepository) *durableStateRunner {
	return &durableStateRunner{
		fakeStepRunner: &fakeStepRunner{
			cfg:   OrchestratorConfig{PolicyVersion: "test-policy-v1"},
			state: &RunState{},
		},
		repo:   repo,
		cutter: &recordingCutter{},
	}
}

func TestPrepareBatchStateFailsClosedWhenCreateBatchFails(t *testing.T) {
	runner := newDurableStateRunner(&durableStateFailureRepo{failOn: "CreateBatch"})

	_, err := prepareBatchState(
		context.Background(), runner, "source", []ClipPlan{{SourceID: "source"}},
		1, 1, "batch-1", false,
	)

	require.ErrorIs(t, err, ErrStockExtractClipsDurableStateFailed)
}

func TestPrepareBatchStateFailsClosedWhenCreateArtifactFails(t *testing.T) {
	runner := newDurableStateRunner(&durableStateFailureRepo{failOn: "CreateArtifact"})

	_, err := prepareBatchState(
		context.Background(), runner, "source", []ClipPlan{{SourceID: "source"}},
		1, 1, "batch-1", false,
	)

	require.ErrorIs(t, err, ErrStockExtractClipsDurableStateFailed)
}

func TestStockExtractClipsStepFailsClosedWhenDurableStateCannotBeCreated(t *testing.T) {
	runner := newDurableStateRunner(&durableStateFailureRepo{failOn: "CreateBatch"})
	runner.state.Plan = []ClipPlan{{SourceID: "source", OutputLogicalID: "logical-1"}}
	runner.state.StagedAssets = []*assets.StagedAsset{{SourceID: "source", LocalPath: "/tmp/source.mp4"}}

	err := (StockExtractClipsStep{}).Run(context.Background(), runner)

	require.ErrorIs(t, err, ErrStockExtractClipsDurableStateFailed)
}

func TestMarkArtifactsExtractingFailsClosedOnDurableStateError(t *testing.T) {
	runner := newDurableStateRunner(&durableStateFailureRepo{failOn: "MarkArtifactExtracting"})

	err := markArtifactsExtracting(
		context.Background(), runner, "batch-1", "source", []ClipPlan{{SourceID: "source"}},
	)

	require.ErrorIs(t, err, ErrStockExtractClipsDurableStateFailed)
}

func TestPublishCutsFailsClosedWhenExtractedStateCannotBePersisted(t *testing.T) {
	runner := newDurableStateRunner(&durableStateFailureRepo{failOn: "MarkArtifactExtracted"})
	plan := ClipPlan{SourceID: "source", OutputLogicalID: "logical-1"}
	result := CutBatchResult{Items: []CutItemResult{{
		Status:     CutItemStatusSucceeded,
		OutputPath: "/tmp/clip.mp4",
		SHA256Hex:  "sha256-1",
	}}}

	_, _, err := publishCuts(
		context.Background(), runner, "source", 0, []ClipPlan{plan}, result,
		map[string]int{}, map[string]*timestampGroupBuffer{},
		"root", "", "group", nil, "batch-1",
	)

	require.ErrorIs(t, err, ErrStockExtractClipsDurableStateFailed)
}

func TestPublishCutsFailsClosedWhenFailedArtifactStateCannotBePersisted(t *testing.T) {
	runner := newDurableStateRunner(&durableStateFailureRepo{failOn: "MarkArtifactFailed"})
	plan := ClipPlan{SourceID: "source", OutputLogicalID: "logical-1"}
	result := CutBatchResult{Items: []CutItemResult{{
		Status: CutItemStatusFailed,
	}}}

	_, _, err := publishCuts(
		context.Background(), runner, "source", 0, []ClipPlan{plan}, result,
		map[string]int{}, map[string]*timestampGroupBuffer{},
		"root", "", "group", nil, "batch-1",
	)

	require.ErrorIs(t, err, ErrStockExtractClipsDurableStateFailed)
}
