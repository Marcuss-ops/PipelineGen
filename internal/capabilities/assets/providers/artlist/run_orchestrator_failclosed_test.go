package artlist

import (
	"context"
	"path/filepath"
	"testing"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestStagePersistResults_BeginTxFailure_CountsItemAsFailed pins the P0.2
// remediation: a BeginTx failure on the per-clip persistence transaction
// must be accounted on the response (item.Error + resp.Failed) instead of
// being a silent drop that only the Policy-B undercount guard could detect.
func TestStagePersistResults_BeginTxFailure_CountsItemAsFailed(t *testing.T) {
	ctx := context.Background()
	db := createTestDB(t)
	logger := zap.NewNop()
	orch := NewRunOrchestratorService(&Service{
		log:            logger,
		cfg:            &config.Config{Video: config.VideoConfig{Duration: 15}},
		mainDB:         db,
		assetFinalizer: assetfinalizer.NewAssetTxFinalizer(logger, nil),
	})

	resp := &RunTagResponse{Found: 1, Items: []RunTagItem{{
		ClipID:        "failclosed-begintx-1",
		Name:          "failclosed-begintx-1",
		Filename:      "failclosed-begintx-1.mp4",
		Status:        "processed",
		DriveFileID:   "drive-id-1",
		DriveLink:     "https://drive.google.com/file/d/drive-id-1/view",
		DownloadLink:  "https://cdn.example.invalid/drive-id-1.mp4",
		LegacyFileMD5: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		LocalPath:     filepath.Join(t.TempDir(), "failclosed-begintx-1.mp4"),
	}}}

	// Close the DB so the per-clip BeginTx deterministically fails.
	require.NoError(t, db.Close())

	err := orch.stagePersistResults(ctx, resp)
	require.NoError(t, err, "per-item BeginTx failure must not abort the persist loop")

	assert.Equal(t, 1, resp.Failed, "BeginTx failure must count as a failed item")
	assert.Equal(t, 0, resp.Processed, "no clip may be counted processed without a committed transaction")
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "persist_failed", resp.Items[0].Status)
	assert.Contains(t, resp.Items[0].Error, "persist failed", "item error must explain the BeginTx failure")
}

// TestStagePersistResults_UnwiredDeps_FailsClosed pins the P0.2 remediation:
// a missing asset finalizer or main DB is a systemic failure, not a
// no-op that returns apparent run success without persisting.
func TestStagePersistResults_UnwiredDeps_FailsClosed(t *testing.T) {
	ctx := context.Background()
	orch := NewRunOrchestratorService(&Service{log: zap.NewNop()})
	resp := &RunTagResponse{Found: 1, Items: []RunTagItem{{
		ClipID: "failclosed-unwired-1", Name: "failclosed-unwired-1", Status: "processed",
	}}}

	err := orch.stagePersistResults(ctx, resp)
	require.Error(t, err, "missing finalizer/main DB must fail closed")
	assert.Contains(t, err.Error(), "cannot persist")
}

// TestRunTag_NonZeroOutputGeometry_FailsClosed pins the P1.1 remediation:
// per-run width/height/FPS cannot be honored by the canonical-profile
// media processor, so non-zero values are rejected at the pipeline entry
// instead of being silently ignored.
func TestRunTag_NonZeroOutputGeometry_FailsClosed(t *testing.T) {
	ctx := context.Background()
	orch := NewRunOrchestratorService(&Service{log: zap.NewNop()})

	for name, req := range map[string]RunTagRequest{
		"width":  {Term: "mountain", Width: 1920},
		"height": {Term: "mountain", Height: 1080},
		"fps":    {Term: "mountain", FPSNum: 60, FPSDen: 1},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := orch.RunTag(ctx, &req)
			require.Error(t, err)
			require.NotNil(t, resp)
			assert.False(t, resp.OK)
			assert.Contains(t, resp.Error, "per-run output geometry")
		})
	}
}

// TestStagePersistResults_EarlyStageFailuresNotRecounted pins the
// OUTCOME-SINGLE-TALLY remediation (September 2026): items that an
// earlier stage already adjudicated AND tallied (resp.Failed++ in
// stageProcessBatch / stageBuildProcessInputs) must NOT be re-adjudicated
// by the persist loop's Drive-field gate. Re-adjudication would re-stamp
// their Status (e.g. transcription_failed → drive_upload_failed) and
// DOUBLE-COUNT them into resp.Failed, breaking the Found == Processed +
// Skipped + Failed invariant that EvaluateRunOutcome owns.
func TestStagePersistResults_EarlyStageFailuresNotRecounted(t *testing.T) {
	ctx := context.Background()
	db := createTestDB(t)
	logger := zap.NewNop()
	orch := NewRunOrchestratorService(&Service{
		log:            logger,
		cfg:            &config.Config{Video: config.VideoConfig{Duration: 15}},
		mainDB:         db,
		assetFinalizer: assetfinalizer.NewAssetTxFinalizer(logger, nil),
	})

	// Two items that stageProcessBatch already failed + tallied. Both have
	// empty Drive fields: before OUTCOME-SINGLE-TALLY the Drive-field gate
	// would re-stamp them drive_upload_failed and bump resp.Failed twice
	// more (3 total for 2 failures). After the fix the persist loop skips
	// them and resp.Failed stays at the 2 the earlier stage recorded.
	resp := &RunTagResponse{Found: 2, Items: []RunTagItem{
		{ClipID: "single-tally-transcription-1", Name: "t1", Status: "transcription_failed",
			Error: "transcription failed: whisper unavailable"},
		{ClipID: "single-tally-blocked-1", Name: "b1", Status: "blocked_mode",
			Error: ErrAcquisitionModeBlocked.Error()},
	}}
	resp.Failed = 2 // already tallied by stageProcessBatch

	require.NoError(t, orch.stagePersistResults(ctx, resp))

	assert.Equal(t, 2, resp.Failed,
		"early-stage failures must NOT be re-counted by the persist loop (double-count guard)")
	assert.Equal(t, 0, resp.Processed)
	assert.Equal(t, "transcription_failed", resp.Items[0].Status,
		"early-stage failure Status must be preserved verbatim (no re-stamping)")
	assert.Equal(t, "blocked_mode", resp.Items[1].Status,
		"early-stage failure Status must be preserved verbatim (no re-stamping)")
}
