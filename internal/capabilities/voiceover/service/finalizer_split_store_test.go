package voiceover

import (
	"context"
	"testing"

	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type recordingAssetCommitter struct {
	commitTxCalls       int
	commitAndIndexCalls int
}

func (c *recordingAssetCommitter) CommitTx(context.Context, assetspersistence.Transaction, assetspersistence.CommitRequest) (assetspersistence.CommitResult, error) {
	c.commitTxCalls++
	return assetspersistence.CommitResult{}, nil
}

func (c *recordingAssetCommitter) CommitAndIndex(context.Context, assetspersistence.CommitRequest) (assetspersistence.CommitResult, error) {
	c.commitAndIndexCalls++
	return assetspersistence.CommitResult{}, nil
}

func (c *recordingAssetCommitter) CommitAsset(context.Context, assetspersistence.AssetCommitRequest) (assetspersistence.CommittedAsset, error) {
	return assetspersistence.CommittedAsset{}, nil
}

func TestVoiceoverFinalizer_SplitStoreUsesSelfOwnedCommitterTransaction(t *testing.T) {
	db := openFinalizerTestDB(t)
	repo := &finalizerTestRepo{db: db}
	committer := &recordingAssetCommitter{}
	f := newVoiceoverFinalizer(voiceoverFinalizerDeps{
		VoiceoverRepo:        repo,
		Outbox:               &stubOutboxEnqueuer{},
		LifecycleService:     &stubProjectionUpserter{},
		Committer:            committer,
		CommitterSelfOwnedTx: true,
		Logger:               zap.NewNop(),
	})

	tx, err := repo.BeginTx(context.Background())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	_, err = f.Finalize(context.Background(), tx, &FinalizeCommand{
		ID:            "vo-split-store",
		RequestID:     "request-split-store",
		TextHash:      "text-hash",
		Text:          "Donald Trump",
		Language:      "en",
		Filename:      "vo-split-store.mp3",
		LegacyFileMD5: "content-hash",
	})
	require.NoError(t, err)
	require.Equal(t, 0, committer.commitTxCalls)
	require.Equal(t, 1, committer.commitAndIndexCalls)
}

var _ assetspersistence.AssetCommitter = (*recordingAssetCommitter)(nil)
