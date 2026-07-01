// Package stockpipeline — run_upload_indexing_test.go (Audit P0 #6, July 2026).
//
// Tests the 4-state IndexingStatus enum on ChunkResult, pinning the
// silent-success class closures from the audit:
//
//   (1) assetIndex nil ⇒ IndexingSkipped (NOT IndexingCompleted as
//       the legacy `indexed := true` default-zero would suggest).
//   (2) all indexing steps succeed ⇒ IndexingCompleted.
//   (3) asset_index.Upsert fails ⇒ IndexingFailed (and dispatcher
//       is NOT called — halt-dispatch surface).
//   (4) clipsRepo.UpdateSearchTerms fails ⇒ IndexingFailed (and
//       dispatcher is NOT called — the audit-mandated halt-dispatch
//       contract: UpdateSearchTerms "runs BEFORE the upsert+dispatch
//       so a failure aborts the dispatch", per the original comment).
//
// Recording mocks (no testify-mock dep) so test surface is grep-friendly
// and free of dependency-heavy assertion libs.
package stockpipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ─────────────────────────────────────────────────────────────────────
// Recording fakes — narrow, structural, satisfying the production
// interfaces so the test Service can be wired without concrete ports.
// ─────────────────────────────────────────────────────────────────────

// fakePublisher records Publish + ResolveFolder calls. Returns a
// fixed FileID so the rest of the pipeline (which reads pubResult)
// sees a stable deterministic value.
type fakePublisher struct {
	fileID string
}

var _ delivery.Publisher = (*fakePublisher)(nil)

func (f *fakePublisher) Publish(_ context.Context, _ delivery.PublishRequest) (*delivery.PublishResult, error) {
	return &delivery.PublishResult{
		FileID:       f.fileID,
		WebViewLink:  "https://drive.google.com/file/d/" + f.fileID + "/view",
		DownloadLink: "https://drive.google.com/uc?id=" + f.fileID,
	}, nil
}

func (f *fakePublisher) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	return "stub-folder", nil
}

// fakeAssetIndex records Upsert call count + returns injected err.
type fakeAssetIndex struct {
	err   error
	calls int
	last  *assetindex.AssetRecord
}

var _ stockAssetIndexUpserter = (*fakeAssetIndex)(nil)

func (f *fakeAssetIndex) Upsert(_ context.Context, rec *assetindex.AssetRecord) error {
	f.calls++
	f.last = rec
	return f.err
}

// fakeClipsRepo records UpdateSearchTerms call count + returns injected err.
type fakeClipsRepo struct {
	err   error
	calls int
}

var _ stockClipsSearchTermUpdater = (*fakeClipsRepo)(nil)

func (f *fakeClipsRepo) UpdateSearchTerms(_ context.Context, _ string, _ string, _ string, _ []string, _ string) error {
	f.calls++
	return f.err
}

// fakeDispatcher records EnqueueAndIndex call count + returns injected err.
type fakeDispatcher struct {
	err   error
	calls int
}

var _ stockChunkDispatcher = (*fakeDispatcher)(nil)

func (f *fakeDispatcher) EnqueueAndIndex(_ context.Context, _ *asset.Asset, _ string) error {
	f.calls++
	return f.err
}

// makeTestInput builds a populated RunInput with non-empty SearchText
// (so the clips-repo UpdateSearchTerms branch is reached end-to-end
// in the all-steps-OK test).
func makeTestInput() *RunInput {
	return &RunInput{
		SearchQueries: []string{"test"},
		FolderName:    "test_folder",
		Subfolder:     "stock",
	}
}

func makeTestCfg() *config.VideoConfig {
	return &config.VideoConfig{
		ChunkDuration:  25,
		EffectInterval: 4,
	}
}

// ─────────────────────────────────────────────────────────────────────
// 4-state IndexingStatus tests
// ─────────────────────────────────────────────────────────────────────

func TestUploadAndIndexChunk_AssetIndexNil_SetsIndexingSkipped(t *testing.T) {
	// No assetIndex wired (operator never deployed the indexer).
	// Pre-fix: `indexed := true` ⇒ ChunkResult.Indexed=true ⇒ false
	// success signal. Post-fix: IndexingSkipped status surfaces
	// "we never tried" truthfully.
	svc := &Service{
		log:       zap.NewNop(),
		publisher: &fakePublisher{fileID: "fake-file-skipped"},
		// assetIndex: nil — outer gate short-circuits the indexing surface
		// clipsRepo:  nil — not reached because the outer gate is nil
		// dispatcher: nil — not reached because the outer gate is nil
	}
	chunkRes := &ChunkResult{}

	err := svc.uploadAndIndexChunk(
		context.Background(),
		1,
		"/tmp/chunk_skipped.mp4",
		"chunk_skipped",
		"folder-id",
		chunkRes,
		makeTestInput(),
		makeTestCfg(),
	)
	require.NoError(t, err, "uploadAndIndexChunk must return nil even when indexing surface is unwired")
	assert.Equal(t, IndexingSkipped, chunkRes.Indexed,
		"audit P0 #6: assetIndex nil ⇒ IndexingSkipped (CLOSES legacy silent-success)")
}

func TestUploadAndIndexChunk_AllStepsOK_SetsIndexingCompleted(t *testing.T) {
	assetIdx := &fakeAssetIndex{}
	clipsR := &fakeClipsRepo{}
	disp := &fakeDispatcher{}

	svc := &Service{
		log:        zap.NewNop(),
		publisher:  &fakePublisher{fileID: "fake-file-ok"},
		assetIndex: assetIdx,
		clipsRepo:  clipsR,
		dispatcher: disp,
	}
	chunkRes := &ChunkResult{}

	err := svc.uploadAndIndexChunk(
		context.Background(),
		1,
		"/tmp/chunk_ok.mp4",
		"chunk_ok",
		"folder-id",
		chunkRes,
		makeTestInput(),
		makeTestCfg(),
	)
	require.NoError(t, err)
	assert.Equal(t, IndexingCompleted, chunkRes.Indexed,
		"all deps succeed ⇒ IndexingCompleted")
	assert.Equal(t, 1, assetIdx.calls, "assetIndex.Upsert invoked exactly once")
	assert.Equal(t, 1, clipsR.calls, "clipsRepo.UpdateSearchTerms invoked exactly once")
	assert.Equal(t, 1, disp.calls, "dispatcher.EnqueueAndIndex invoked exactly once (audit P0 #6 dispatch path reached)")
}

func TestUploadAndIndexChunk_AssetIndexUpsertFails_SetsIndexingFailed(t *testing.T) {
	// Pre-fix: `indexed=false` ⇒ caller can't distinguish "tried and
	// failed" from "never wired". Post-fix: IndexingFailed surface
	// honestly reports "tried-and-failed" so operators can backfill.
	assetIdx := &fakeAssetIndex{err: errors.New("sqlite: database is locked")}

	svc := &Service{
		log:        zap.NewNop(),
		publisher:  &fakePublisher{fileID: "fake-file-asset-fail"},
		assetIndex: assetIdx,
		// clipsRepo + dispatcher intentionally nil — early-return
		// after assetIndex.Upsert failure means indexChunkToClipsDB
		// is NEVER called. Tests must compile and pass with these
		// fields nil.
	}
	chunkRes := &ChunkResult{}

	err := svc.uploadAndIndexChunk(
		context.Background(),
		1,
		"/tmp/chunk_asset_fail.mp4",
		"chunk_asset_fail",
		"folder-id",
		chunkRes,
		makeTestInput(),
		makeTestCfg(),
	)
	require.NoError(t, err,
		"uploadAndIndexChunk must NOT return err at the upload level — chunk is on Drive + best-effort indexing")
	assert.Equal(t, IndexingFailed, chunkRes.Indexed,
		"assetIndex.Upsert fail ⇒ IndexingFailed")
	assert.Equal(t, 1, assetIdx.calls)
}

func TestUploadAndIndexChunk_UpdateSearchTermsFails_SetsIndexingFailedAndHaltsDispatch(t *testing.T) {
	// The audit's MOST IMPORTANT contract: UpdateSearchTerms failure
	// MUST halt the dispatch path. Pre-fix: `s.log.Warn(...)` and
	// continued to `s.dispatcher.EnqueueAndIndex` — the worker would
	// then persist a row with stale tags_norm that the worker cannot
	// repair (no backfill machinery matches legacy tags). Post-fix:
	// return err from upsertChunkAndDispatch ⇒ dispatcher.EnqueueAndIndex
	// is NOT called ⇒ chunkRes.Indexed = IndexingFailed.
	assetIdx := &fakeAssetIndex{}
	clipsR := &fakeClipsRepo{err: errors.New("update search terms blocked by schema lock")}
	disp := &fakeDispatcher{}

	svc := &Service{
		log:        zap.NewNop(),
		publisher:  &fakePublisher{fileID: "fake-file-st-fail"},
		assetIndex: assetIdx,
		clipsRepo:  clipsR,
		dispatcher: disp,
	}
	chunkRes := &ChunkResult{}

	err := svc.uploadAndIndexChunk(
		context.Background(),
		1,
		"/tmp/chunk_st_fail.mp4",
		"chunk_st_fail",
		"folder-id",
		chunkRes,
		makeTestInput(),
		makeTestCfg(),
	)
	require.NoError(t, err, "uploadAndIndexChunk must NOT return err at the upload level (best-effort indexing)")
	assert.Equal(t, IndexingFailed, chunkRes.Indexed,
		"UpdateSearchTerms fail ⇒ IndexingFailed (audit P0 #6 halt-dispatch)")
	assert.Equal(t, 1, assetIdx.calls, "assetIndex.Upsert was reached BEFORE UpdateSearchTerms")
	assert.Equal(t, 1, clipsR.calls, "UpdateSearchTerms was invoked exactly once")
	assert.Equal(t, 0, disp.calls,
		"dispatcher.EnqueueAndIndex MUST NOT be invoked when UpdateSearchTerms fails (audit P0 #6 halt-dispatch — closes silent-success on dispatch surface)")
}
