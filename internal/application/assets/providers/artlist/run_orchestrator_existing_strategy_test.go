package artlist

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func existingArtlistAsset() asset.Asset {
	return asset.Asset{
		ID:        "artlist-existing-1",
		Name:      "Existing clip",
		SourceURL: "https://cdn.example.com/existing.m3u8",
		Metadata: asset.Metadata{
			"_download_link": "https://cdn.example.com/existing.m3u8",
			"_drive_file_id": "drive-existing-1",
			"_drive_link":    "https://drive.google.com/file/d/drive-existing-1/view",
			"_file_hash":     "sha256-existing-1",
		},
	}
}

func existingStrategyOrchestrator() *RunOrchestratorService {
	return &RunOrchestratorService{svc: &Service{
		cfg: &config.Config{},
		log: zap.NewNop(),
	}}
}

func TestStageBuildProcessInputsVerifySkipsBeforeDownload(t *testing.T) {
	t.Parallel()

	resp := &RunTagResponse{Term: "existing", Found: 1}
	work := existingStrategyOrchestrator().stageBuildProcessInputs(
		context.Background(),
		&RunTagRequest{Strategy: "verify"},
		resp,
		[]asset.Asset{existingArtlistAsset()},
	)

	require.Empty(t, work, "verify must not create processor work for a canonical Drive asset with a persisted hash")
	require.Equal(t, 1, resp.Skipped)
	require.Len(t, resp.Items, 1)
	require.Equal(t, "skipped_existing", resp.Items[0].Status)
	require.Equal(t, "drive-existing-1", resp.Items[0].DriveFileID)
	require.Equal(t, "sha256-existing-1", resp.Items[0].FileHash)
}

func TestStageBuildProcessInputsReplaceForcesProcessing(t *testing.T) {
	t.Parallel()

	resp := &RunTagResponse{Term: "existing", Found: 1}
	work := existingStrategyOrchestrator().stageBuildProcessInputs(
		context.Background(),
		&RunTagRequest{Strategy: "replace"},
		resp,
		[]asset.Asset{existingArtlistAsset()},
	)

	require.Len(t, work, 1, "replace must bypass the existing-asset skip gate")
	require.Zero(t, resp.Skipped)
	require.Empty(t, resp.Items)
	require.Equal(t, "https://cdn.example.com/existing.m3u8", work[0].processInput.SourceURL)
}

func TestStageBuildProcessInputsDryRunPrecedesDedup(t *testing.T) {
	t.Parallel()

	resp := &RunTagResponse{Term: "existing", Found: 1}
	work := existingStrategyOrchestrator().stageBuildProcessInputs(
		context.Background(),
		&RunTagRequest{Strategy: "verify", DryRun: true},
		resp,
		[]asset.Asset{existingArtlistAsset()},
	)

	require.Empty(t, work)
	require.Equal(t, 1, resp.Skipped)
	require.Len(t, resp.Items, 1)
	require.Equal(t, "dry_run", resp.Items[0].Status, "dry-run must preserve its historical audit status")
}
