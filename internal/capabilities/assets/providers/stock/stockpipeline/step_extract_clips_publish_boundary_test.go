package stockpipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/idempotency"
)

// ── P0 stock-acquisition certification (September 2026) ──────────────────
//
// These tests pin the publication boundary of the stock extract/publish step:
//
//	cut VERIFIED → Drive publication VERIFIED → canonical media commit
//	→ outbox (asset.index.requested) → indexing
//
// The former order committed media_assets + the index outbox event BEFORE the
// Drive upload, so a failed publication still produced a searchable asset
// (index_state could reach INDEXED) with no addressable Drive location. Both
// the exposure and the corrected order are asserted here.

// boundaryRecordingWriter records every committed asset so the test can
// assert both "no commit happened" and "the commit carried the Drive identity".
type boundaryRecordingWriter struct {
	calls  int
	hashes []string
	clips  []*asset.Asset
	err    error
}

func (w *boundaryRecordingWriter) WriteAndEnqueue(_ context.Context, clip *asset.Asset, fileHash string) error {
	w.calls++
	w.hashes = append(w.hashes, fileHash)
	w.clips = append(w.clips, clip)
	return w.err
}

// failingArtifactPreparation simulates a Drive publication failure.
type failingArtifactPreparation struct{}

func (failingArtifactPreparation) Prepare(_ context.Context, _ finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	return finalization.PublishedArtifact{}, errors.New("simulated Drive publication failure")
}

// publishBoundaryRunner is the minimal StepRunner needed by publishCuts: a
// writer, an artifact preparation service and (absent) durable batch state.
type publishBoundaryRunner struct {
	*fakeStepRunner
	writer TransactionalAssetWriter
	prep   finalization.ArtifactPreparationService
}

func (r *publishBoundaryRunner) Writer() TransactionalAssetWriter { return r.writer }
func (r *publishBoundaryRunner) ArtifactPreparation() finalization.ArtifactPreparationService {
	return r.prep
}
func (r *publishBoundaryRunner) BatchRepository() StockBatchRepository { return nil }
func (r *publishBoundaryRunner) Cutter() VideoCutter                   { return &recordingCutter{} }
func (r *publishBoundaryRunner) Log() *zap.Logger                      { return zap.NewNop() }

var _ StepRunner = (*publishBoundaryRunner)(nil)

func newPublishBoundaryRunner(writer TransactionalAssetWriter, prep finalization.ArtifactPreparationService) *publishBoundaryRunner {
	return &publishBoundaryRunner{
		fakeStepRunner: &fakeStepRunner{
			cfg:   OrchestratorConfig{PolicyVersion: "test-policy-v1"},
			state: &RunState{},
		},
		writer: writer,
		prep:   prep,
	}
}

func boundaryCutResult() CutBatchResult {
	return CutBatchResult{Items: []CutItemResult{{
		Status:     CutItemStatusSucceeded,
		OutputPath: "/tmp/boundary-clip.mp4",
		SHA256Hex:  "sha256-boundary-1",
		SizeBytes:  2048,
	}}}
}

func boundaryPlans() []ClipPlan {
	return []ClipPlan{{
		SourceID:        "https://www.youtube.com/watch?v=boundary",
		SourceProvider:  SourceProviderYouTube,
		SourceVideoID:   "boundary",
		OutputLogicalID: "logical-boundary-1",
		Title:           "Boundary Clip",
		StartSec:        0,
		EndSec:          5,
	}}
}

// TestPublishCutsDoesNotCommitBeforeDrivePublication is the regression pin for
// the ordering exposure: when the Drive publication fails, the canonical
// media commit MUST NOT have happened — no media_assets row and, critically,
// no asset.index.requested outbox event that would make the asset searchable
// without a Drive location.
func TestPublishCutsDoesNotCommitBeforeDrivePublication(t *testing.T) {
	writer := &boundaryRecordingWriter{}
	runner := newPublishBoundaryRunner(writer, failingArtifactPreparation{})

	cutPaths, chunks, err := publishCuts(
		context.Background(), runner, "source", 0, boundaryPlans(), boundaryCutResult(),
		map[string]int{}, map[string]*timestampGroupBuffer{},
		"root", "folder-root", "group", nil, "batch-1",
	)

	require.ErrorIs(t, err, ErrStockPublishArtifactFailed)
	require.Nil(t, cutPaths)
	require.Nil(t, chunks)
	require.Zero(t, writer.calls,
		"the canonical media commit must not run when Drive publication failed (it would emit asset.index.requested for an unpublished clip)")
}

// TestPublishCutsCommitsAfterDrivePublicationWithDriveIdentity asserts the
// corrected boundary: the commit runs only after a successful publication and
// carries the published Drive identity, so media_assets is committed with a
// real addressable Drive location instead of a local-only one.
func TestPublishCutsCommitsAfterDrivePublicationWithDriveIdentity(t *testing.T) {
	writer := &boundaryRecordingWriter{}
	prep := &recordingArtifactPreparation{}
	runner := newPublishBoundaryRunner(writer, prep)

	cutPaths, chunks, err := publishCuts(
		context.Background(), runner, "source", 0, boundaryPlans(), boundaryCutResult(),
		map[string]int{}, map[string]*timestampGroupBuffer{},
		"root", "folder-root", "group", nil, "batch-1",
	)

	require.NoError(t, err)
	require.Equal(t, []string{"/tmp/boundary-clip.mp4"}, cutPaths)
	require.Len(t, prep.artifacts, 1, "the clip must be published to Drive exactly once")

	require.Equal(t, 1, writer.calls, "exactly one canonical commit per published clip")
	require.Equal(t, []string{"sha256-boundary-1"}, writer.hashes)

	committed := writer.clips[0]
	require.Equal(t, "logical-boundary-1", committed.ID)
	require.Equal(t, "logical-boundary-1-file", committed.DriveFileID(),
		"the commit must carry the Drive file published by the preparation service")
	require.NotEmpty(t, committed.DriveLink())
	require.Equal(t, "folder-123", committed.FolderID())

	require.Len(t, chunks, 1)
	require.Equal(t, "logical-boundary-1-file", chunks[0].RemoteFileID)
	require.Equal(t, "https://www.youtube.com/watch?v=boundary", chunks[0].SourceURL)
	require.Equal(t, "boundary", chunks[0].SourceVideoID)
	require.Equal(t, "folder-123", chunks[0].TimestampFolderID,
		"the published parent Drive folder must ride on the chunk so the finalizer's spine write cannot blank media_assets.folder_id")
}

// TestPublishCutsAndFinalizerConvergeOnOneAssetIdentityAndOneIndexEvent is the
// regression pin for the September 2026 stock certification defect: the same
// stock clip is committed by TWO producers — the post-publication commit made
// here by publishCuts and the stock job finalizer's single-TX spine write
// (AssetTxFinalizer, which consumes BuildFinalizationRequest). When the two
// disagreed, one clip produced TWO asset.index.requested outbox events (the key
// is provider-scoped), the later write clobbered media_assets.source (dropping
// the YouTube provenance), and it restamped semantic_role from the provider
// default because the earlier write's declared taxonomy was discarded.
//
// Convergence is asserted WIRE-level, on the two facts that decide the outbox
// key and the persisted taxonomy: the artifact Source must equal the clip's
// Source, and the declared asset_kind / semantic_role must be identical. When
// both hold, the canonical event key computed by the two producers is byte
// equal and the second outbox insert is a no-op.
func TestPublishCutsAndFinalizerConvergeOnOneAssetIdentityAndOneIndexEvent(t *testing.T) {
	writer := &boundaryRecordingWriter{}
	runner := newPublishBoundaryRunner(writer, &recordingArtifactPreparation{})

	// BuildFinalizationRequest validates the chunk digest strictly.
	const boundarySHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cutResult := boundaryCutResult()
	cutResult.Items[0].SHA256Hex = boundarySHA

	_, chunks, err := publishCuts(
		context.Background(), runner, "source", 0, boundaryPlans(), cutResult,
		map[string]int{}, map[string]*timestampGroupBuffer{},
		"root", "folder-root", "group", nil, "batch-1",
	)
	require.NoError(t, err)
	require.Len(t, writer.clips, 1)
	committed := writer.clips[0]

	// The other producer's request for the very same bytes.
	finalReq, err := BuildFinalizationRequest(
		"batch-1", validLease("batch-1"), []byte(`{}`), chunks,
		MetadataState{
			LocalPath: "/tmp/boundary-meta.json", SHA256: fakeSHA(99), SizeBytes: 512,
			RemoteFileID: "meta-file", RemoteWebViewLink: "https://drive/meta",
		},
		"fp-convergence",
	)
	require.NoError(t, err)
	require.Len(t, finalReq.Artifacts, 2, "1 metadata artifact + 1 chunk artifact")
	chunkArt := finalReq.Artifacts[1]

	// ── Identity: the spine write must not re-label the acquisition ──
	require.Equal(t, string(committed.Source), chunkArt.Source,
		"the finalizer must commit the clip under its acquisition provider, not the stock family label")
	require.Equal(t, committed.ID, chunkArt.ArtifactID)
	require.Equal(t, boundarySHA, chunkArt.SHA256)

	// ── Taxonomy: the declared stock family must survive either commit order ──
	require.Equal(t, committed.Metadata["asset_kind"], chunkArt.ArtifactMetadata["asset_kind"])
	require.Equal(t, StockAssetKind, chunkArt.ArtifactMetadata["asset_kind"])
	require.Equal(t, committed.Metadata["semantic_role"], chunkArt.ArtifactMetadata["semantic_role"])
	require.Equal(t, StockSemanticRole, chunkArt.ArtifactMetadata["semantic_role"])

	// ── The Drive folder must not be blanked by the second write ──
	require.Equal(t, committed.FolderID(), chunkArt.Location.FolderID)
	require.Equal(t, "folder-123", chunkArt.Location.FolderID)

	// ── The outbox identity derived by both producers must collide ──
	postPublishKey, err := idempotency.OutboxKey("asset.index.requested", string(committed.Source), committed.ID, boundarySHA)
	require.NoError(t, err)
	finalizerKey, err := idempotency.OutboxKey("asset.index.requested", chunkArt.Source, chunkArt.ArtifactID, chunkArt.SHA256)
	require.NoError(t, err)
	require.Equal(t, postPublishKey, finalizerKey,
		"one asset must resolve to ONE canonical index event key — otherwise every stock clip emits a duplicate asset.index.requested")
}
