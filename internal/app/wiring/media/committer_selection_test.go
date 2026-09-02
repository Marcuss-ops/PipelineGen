package media

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

type selectionCommitter struct{}

func (selectionCommitter) CommitTx(context.Context, persistence.Transaction, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}
func (selectionCommitter) CommitAndIndex(context.Context, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}
func (selectionCommitter) CommitAsset(context.Context, persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return persistence.CommittedAsset{}, nil
}

func TestSelectMediaAssetCommitterUsesPostgresWhenEnabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.MediaPostgreSQL.Enabled = true
	pg := selectionCommitter{}
	selected, err := SelectMediaAssetCommitter(cfg, selectionCommitter{}, pg)
	if err != nil {
		t.Fatalf("SelectMediaAssetCommitter: %v", err)
	}
	if selected != pg {
		t.Fatal("PostgreSQL committer was not selected")
	}
}

func TestSelectMediaAssetCommitterRejectsMissingPostgresAdapter(t *testing.T) {
	cfg := &config.Config{}
	cfg.MediaPostgreSQL.Enabled = true
	if _, err := SelectMediaAssetCommitter(cfg, selectionCommitter{}, nil); err == nil {
		t.Fatal("missing PostgreSQL committer must fail closed")
	}
}

func TestSelectMediaAssetCommitterUsesSQLiteOnlyWhenPostgresDisabled(t *testing.T) {
	cfg := &config.Config{}
	sqlite := selectionCommitter{}
	selected, err := SelectMediaAssetCommitter(cfg, sqlite, nil)
	if err != nil {
		t.Fatalf("SelectMediaAssetCommitter: %v", err)
	}
	if selected != sqlite {
		t.Fatal("SQLite compatibility committer was not selected")
	}
}
