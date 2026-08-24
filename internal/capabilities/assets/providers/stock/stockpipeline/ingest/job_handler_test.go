// Package stockpipeline — job_handler_test.go (P6 test coverage, July 2026).
//
// TDD coverage for StockJobResult.ToResultMap(): round-trip with
// all fields populated, omitempty semantics for FinalizationStatus
// (empty vs non-empty) and FinalizationCompletedAt (zero vs non-zero),
// nil Manifest, empty Chunks, and ManifestKey wire-constant assertion.
package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestProjectManifestToPipelineResult_HydratesAllFields(t *testing.T) {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		JobID:         "job-project-1",
		Artifacts: []job.Artifact{
			{
				ID:                 "job-project-1:chunk:1",
				Kind:               string(finalization.KindVideo),
				Path:               "/tmp/chunk-1.mp4",
				RemoteFileID:       "drive-chunk-1",
				RemoteWebViewLink:  "https://drive.example/chunk-1",
				RemoteDownloadLink: "https://drive.example/download/chunk-1",
				SHA256:             "sha256-chunk-1",
				ArtifactMetadata: map[string]any{
					"chunk_index": 1, "clip_count": 2, "start_sec": 12.5,
					"end_sec": 22.5, "title": "Round 2", "source_url": "https://youtu.be/source-1",
				},
			},
			{
				ID:                "job-project-1:metadata",
				Kind:              job.ArtifactKindMetadata,
				RemoteFileID:      "drive-metadata",
				RemoteWebViewLink: "https://drive.example/metadata",
				ArtifactMetadata:  map[string]any{"total_clips": 3},
			},
			{
				ID:                "job-project-1:chunk:0",
				Kind:              string(finalization.KindVideo),
				Path:              "/tmp/chunk-0.mp4",
				RemoteFileID:      "drive-chunk-0",
				RemoteWebViewLink: "https://drive.example/chunk-0",
				SHA256:            "sha256-chunk-0",
				ArtifactMetadata: map[string]any{
					"chunk_index": 0, "clip_count": 1, "start_sec": 0, "end_sec": 10,
					"title": "Round 1", "source_url": "https://youtu.be/source-0",
				},
			},
		},
	}

	result, err := projectManifestToPipelineResult(manifest)
	if err != nil {
		t.Fatalf("projectManifestToPipelineResult: %v", err)
	}
	if result.TotalChunks != 2 || result.TotalClips != 3 {
		t.Fatalf("counts = clips:%d chunks:%d, want clips:3 chunks:2", result.TotalClips, result.TotalChunks)
	}
	if result.MetadataFileID != "drive-metadata" || result.MetadataLink != "https://drive.example/metadata" {
		t.Fatalf("metadata projection = id:%q link:%q", result.MetadataFileID, result.MetadataLink)
	}
	if len(result.Chunks) != 2 || result.Chunks[0].Index != 0 || result.Chunks[1].Index != 1 {
		t.Fatalf("chunks not sorted by manifest chunk index: %+v", result.Chunks)
	}
	if result.Chunks[1].DriveFileID != "drive-chunk-1" || result.Chunks[1].DriveLink != "https://drive.example/chunk-1" || result.Chunks[1].DownloadLink != "https://drive.example/download/chunk-1" || result.Chunks[1].SHA256 != "sha256-chunk-1" {
		t.Fatalf("chunk publication fields not hydrated: %+v", result.Chunks[1])
	}
	if result.Chunks[1].TimelineStart != 12.5 || result.Chunks[1].TimelineEnd != 22.5 || result.Chunks[1].Title != "Round 2" {
		t.Fatalf("chunk metadata not hydrated: %+v", result.Chunks[1])
	}
	if len(result.Chunks[1].SourceIDs) != 1 || result.Chunks[1].SourceIDs[0] != "https://youtu.be/source-1" {
		t.Fatalf("source IDs not hydrated: %+v", result.Chunks[1].SourceIDs)
	}
	if !result.Chunks[1].Rendered || !result.Chunks[1].Uploaded {
		t.Fatalf("published chunk status = rendered:%v uploaded:%v", result.Chunks[1].Rendered, result.Chunks[1].Uploaded)
	}
}

func TestProjectManifestToPipelineResult_MixedIndexedAndLegacyChunksUseUniqueIndices(t *testing.T) {
	manifest := &job.ArtifactManifest{Artifacts: []job.Artifact{
		{ID: "legacy-first", Kind: string(finalization.KindVideo)},
		{ID: "indexed-zero", Kind: string(finalization.KindVideo), ArtifactMetadata: map[string]any{"chunk_index": 0}},
		{ID: "duplicate-index", Kind: string(finalization.KindVideo), ArtifactMetadata: map[string]any{"chunk_index": 0}},
	}}

	result, err := projectManifestToPipelineResult(manifest)
	if err != nil {
		t.Fatalf("projectManifestToPipelineResult: %v", err)
	}
	if len(result.Chunks) != 3 {
		t.Fatalf("chunks = %d, want 3", len(result.Chunks))
	}
	seen := make(map[int]bool, len(result.Chunks))
	for _, chunk := range result.Chunks {
		if seen[chunk.Index] {
			t.Fatalf("duplicate projected chunk index %d: %+v", chunk.Index, result.Chunks)
		}
		seen[chunk.Index] = true
	}
	if result.Chunks[0].Index != 0 || result.Chunks[0].DriveFileID != "" || result.Chunks[1].Index != 1 || result.Chunks[2].Index != 2 {
		t.Fatalf("mixed chunk projection = %+v, want unique indices [0 1 2] with stable ordering", result.Chunks)
	}
}

func TestProjectManifestToPipelineResult_HandlesLegacyMetadataAndMissingIndex(t *testing.T) {
	manifest := &job.ArtifactManifest{
		Artifacts: []job.Artifact{
			{
				ID:   "legacy-metadata",
				Kind: job.ArtifactKindMetadata,
				ArtifactMetadata: map[string]any{
					"file_id":     "legacy-metadata-id",
					"drive_link":  "https://drive.example/legacy-metadata",
					"total_clips": 0,
				},
			},
			{
				ID:   "chunk-indexed",
				Kind: string(finalization.KindVideo),
				ArtifactMetadata: map[string]any{
					"chunk_index": 2,
					"clip_count":  2,
				},
			},
			{
				ID:   "chunk-legacy",
				Kind: string(finalization.KindVideo),
				ArtifactMetadata: map[string]any{
					"clip_count":    1,
					"source_ids":    []any{"source-a", "source-b"},
					"drive_file_id": "legacy-chunk-id",
					"drive_link":    "https://drive.example/legacy-chunk",
				},
			},
		},
	}

	result, err := projectManifestToPipelineResult(manifest)
	if err != nil {
		t.Fatalf("projectManifestToPipelineResult: %v", err)
	}
	if result.MetadataFileID != "legacy-metadata-id" || result.MetadataLink != "https://drive.example/legacy-metadata" {
		t.Fatalf("legacy metadata = id:%q link:%q", result.MetadataFileID, result.MetadataLink)
	}
	if result.TotalChunks != 2 || result.TotalClips != 3 {
		t.Fatalf("legacy counts = clips:%d chunks:%d, want clips:3 chunks:2", result.TotalClips, result.TotalChunks)
	}
	if result.Chunks[0].Index != 2 || result.Chunks[1].Index != 1 {
		t.Fatalf("chunk indices = [%d %d], want [2 1]", result.Chunks[0].Index, result.Chunks[1].Index)
	}
	if got := result.Chunks[1].SourceIDs; len(got) != 2 || got[0] != "source-a" || got[1] != "source-b" {
		t.Fatalf("legacy source IDs = %#v, want [source-a source-b]", got)
	}
	if result.Chunks[1].DriveFileID != "legacy-chunk-id" || result.Chunks[1].DriveLink != "https://drive.example/legacy-chunk" {
		t.Fatalf("legacy chunk links = %+v", result.Chunks[1])
	}
	if !result.Chunks[1].Uploaded {
		t.Fatalf("metadata-published chunk status = %+v, want uploaded=true", result.Chunks[1])
	}
}

// TestProjectManifestToPipelineResult_UnprojectableManifestFailsClosed
// is the fail-closed regression guard for the silent-empty result class
// (godlike/07 no-fake-availability): a manifest that cannot be projected
// into a meaningful *PipelineResult MUST surface the typed sentinel
// ErrStockManifestUnprojectable instead of returning an all-zeros result
// that would let a SUCCEEDED job report total_clips=0/total_chunks=0/
// chunks=[] despite real uploads.
//
// Covered failure modes:
//  1. nil manifest → ErrStockManifestUnprojectable
//  2. formally valid manifest with zero artifacts → errors.Is match
//  3. manifest with artifacts but none projectable (no video chunk, no
//     metadata artifact) → errors.Is match
func TestProjectManifestToPipelineResult_UnprojectableManifestFailsClosed(t *testing.T) {
	t.Run("nil manifest", func(t *testing.T) {
		result, err := projectManifestToPipelineResult(nil)
		if err == nil {
			t.Fatal("nil manifest returned nil error — want ErrStockManifestUnprojectable")
		}
		if !errors.Is(err, ErrStockManifestUnprojectable) {
			t.Errorf("err = %v, want errors.Is(err, ErrStockManifestUnprojectable) == true", err)
		}
		if result != nil {
			t.Errorf("result = %+v, want nil on error", result)
		}
	})

	t.Run("zero artifacts", func(t *testing.T) {
		manifest := &job.ArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
			JobID:         "job-empty-manifest",
			Artifacts:     []job.Artifact{},
		}
		result, err := projectManifestToPipelineResult(manifest)
		if err == nil {
			t.Fatal("zero-artifact manifest returned nil error — want ErrStockManifestUnprojectable")
		}
		if !errors.Is(err, ErrStockManifestUnprojectable) {
			t.Errorf("err = %v, want errors.Is(err, ErrStockManifestUnprojectable) == true", err)
		}
		if result != nil {
			t.Errorf("result = %+v, want nil on error", result)
		}
	})

	t.Run("no projectable artifacts", func(t *testing.T) {
		manifest := &job.ArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
			JobID:         "job-unknown-kinds",
			Artifacts: []job.Artifact{
				{ID: "artifact-a", Kind: "audio", Path: "/tmp/a.wav"},
				{ID: "artifact-b", Kind: "document", Path: "/tmp/b.pdf"},
			},
		}
		result, err := projectManifestToPipelineResult(manifest)
		if err == nil {
			t.Fatal("manifest without video/metadata artifacts returned nil error — want ErrStockManifestUnprojectable")
		}
		if !errors.Is(err, ErrStockManifestUnprojectable) {
			t.Errorf("err = %v, want errors.Is(err, ErrStockManifestUnprojectable) == true", err)
		}
		if result != nil {
			t.Errorf("result = %+v, want nil on error", result)
		}
	})
}

type handleJobSourceStager struct {
	path string
}

func (s handleJobSourceStager) Prepare(_ context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		return nil, err
	}
	return &acquisition.PrepareContext{
		ID:           "handle-job-source",
		SourceRef:    req.Source,
		LocalPath:    s.path,
		SizeBytes:    info.Size(),
		SHA256:       "fixture-sha256",
		CleanupToken: "handle-job-cleanup",
	}, nil
}

func (handleJobSourceStager) Release(context.Context, string) error { return nil }

var _ acquisition.SourceStager = handleJobSourceStager{}

type handleJobCutter struct{}

func (handleJobCutter) Cut(_ context.Context, req CutRequest) (CutBatchResult, error) {
	items := make([]CutItemResult, len(req.Jobs))
	for i, clip := range req.Jobs {
		if err := os.WriteFile(clip.OutputPath, bytes.Repeat([]byte("fixture-cut-output"), 128), 0o644); err != nil {
			return CutBatchResult{}, err
		}
		info, err := os.Stat(clip.OutputPath)
		if err != nil {
			return CutBatchResult{}, err
		}
		items[i] = CutItemResult{JobID: clip.OutputPath, OutputPath: clip.OutputPath, Status: CutItemStatusSucceeded, SizeBytes: info.Size()}
	}
	return CutBatchResult{SourcePath: req.SourcePath, Items: items}, nil
}

var _ VideoCutter = handleJobCutter{}

type handleJobPublisherPort struct{}

func (handleJobPublisherPort) Publish(_ context.Context, artifact finalization.VerifiedArtifact) (finalization.AssetLocation, error) {
	return finalization.AssetLocation{
		Provider:     "drive",
		FileID:       "drive-" + artifact.ArtifactID,
		WebViewLink:  "https://drive.example/view/" + artifact.ArtifactID,
		DownloadLink: "https://drive.example/download/" + artifact.ArtifactID,
		FolderID:     "folder-handle-job",
		FolderPath:   "stock/handle-job",
		Action:       finalization.PublishCreated,
	}, nil
}

var _ finalization.PublisherPort = handleJobPublisherPort{}

// handleJobDispatcher is a no-op stockChunkDispatcher fake: the happy
// path must reach EnqueueAndIndex for each persisted clip without error.
type handleJobDispatcher struct{}

func (handleJobDispatcher) EnqueueAndIndex(_ context.Context, _ *asset.Asset, _ string) error {
	return nil
}

var _ stockChunkDispatcher = handleJobDispatcher{}

// handleJobSourceProbe is a fixed SourceDurationProbe fake returning a
// duration large enough for the 0-5s clip of the HandleJob success test.
type handleJobSourceProbe struct{}

func (handleJobSourceProbe) ProbeDurationSec(_ context.Context, _ string) (float64, error) {
	return 60, nil
}

var _ SourceDurationProbe = handleJobSourceProbe{}

// noopBatchRepository is a success-shaped StockBatchRepository fake.
// The production orchestrator gate (NewProductionStockOrchestrator)
// now requires a non-nil batch repository; the HandleJob happy path
// must not fail on the durable-state writes.
type noopBatchRepository struct{}

func (noopBatchRepository) CreateBatch(context.Context, *StockBatch) error        { return nil }
func (noopBatchRepository) GetBatch(context.Context, string) (*StockBatch, error) { return nil, nil }
func (noopBatchRepository) UpdateBatchStatus(context.Context, string, BatchState, string) error {
	return nil
}
func (noopBatchRepository) CreateGroup(context.Context, *StockBatchGroup) error { return nil }
func (noopBatchRepository) GetGroup(context.Context, string) (*StockBatchGroup, error) {
	return nil, nil
}
func (noopBatchRepository) UpdateGroupStatus(context.Context, string, GroupState, string) error {
	return nil
}
func (noopBatchRepository) ListGroups(context.Context, string) ([]StockBatchGroup, error) {
	return nil, nil
}
func (noopBatchRepository) CreateArtifact(context.Context, *StockArtifact) error { return nil }
func (noopBatchRepository) GetArtifact(context.Context, string) (*StockArtifact, error) {
	return nil, nil
}
func (noopBatchRepository) MarkArtifactExtracting(context.Context, string) error { return nil }
func (noopBatchRepository) MarkArtifactExtracted(context.Context, string, string, string, int) error {
	return nil
}
func (noopBatchRepository) MarkArtifactPublished(context.Context, string, string, string, string) error {
	return nil
}
func (noopBatchRepository) MarkArtifactVerified(context.Context, string) error { return nil }
func (noopBatchRepository) MarkArtifactFailed(context.Context, string, ArtifactState, string) error {
	return nil
}
func (noopBatchRepository) MarkGroupSucceeded(context.Context, string, int) error { return nil }
func (noopBatchRepository) MarkBatchSucceeded(context.Context, string, int) error { return nil }
func (noopBatchRepository) FindIncompleteArtifacts(context.Context, string, int) ([]StockArtifact, error) {
	return nil, nil
}

var _ StockBatchRepository = noopBatchRepository{}

func TestService_HandleJob_SuccessHydratesProjectedResult(t *testing.T) {
	const sourceURL = "https://example.com/handle-job-source.mp4"
	sourcePath := t.TempDir() + "/source.mp4"
	if err := os.WriteFile(sourcePath, []byte("fixture-source"), 0o644); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}

	svc := &Service{
		runtime:       &RuntimeConfig{WorkDir: t.TempDir(), ClipDurationSec: 5, ChunkDurationSec: 5},
		log:           zap.NewNop(),
		publisher:     &recordingPublisher{},
		cutter:        handleJobCutter{},
		renderer:      successNoopRenderer(),
		publisherPort: handleJobPublisherPort{},
		finalizer:     &fakeJobFinalizer{},
		sourceStager:  handleJobSourceStager{path: sourcePath},
		localFS:       newRealishFakeLocalFS(),
		// The production orchestrator gate (NewProductionStockOrchestrator)
		// rejects nil dispatcher / projection / probe / batch repo /
		// step store — the success path must wire them all.
		dispatcher:  handleJobDispatcher{},
		projection:  noopProjection{},
		sourceProbe: handleJobSourceProbe{},
		batchRepo:   noopBatchRepository{},
		stepStore:   steps.NewInMemoryStore(),
	}
	payload, err := json.Marshal(&StockRunPayload{Clips: []ClipSpec{{URL: sourcePath, StartSec: 0, EndSec: 5, Title: "Fixture clip"}},
		FolderID:      "workflow-handle-job",
		ClipDuration:  5,
		ChunkDuration: 5,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	result, err := svc.HandleJob(context.Background(), &appjobs.Job{
		ID:       "job-handle-success",
		WorkerID: "worker-1",
		LeaseID:  "lease-1",
		Payload:  payload,
	}, &appjobs.JobTools{})
	if err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	if got, ok := result["total_chunks"].(int); !ok || got != 1 {
		t.Fatalf("total_chunks = %v (%T), want 1", result["total_chunks"], result["total_chunks"])
	}
	if got, ok := result["total_clips"].(int); !ok || got != 1 {
		t.Fatalf("total_clips = %v (%T), want 1", result["total_clips"], result["total_clips"])
	}
	chunks, ok := result["chunks"].([]ChunkResult)
	if !ok || len(chunks) != 1 {
		t.Fatalf("chunks = %v (%T), want one hydrated chunk", result["chunks"], result["chunks"])
	}
	if chunks[0].DriveFileID == "" || chunks[0].DriveLink == "" || chunks[0].DownloadLink == "" || chunks[0].SHA256 == "" {
		t.Fatalf("hydrated chunk publication fields = %+v", chunks[0])
	}
	if result["metadata_file_id"] == "" || result["metadata_link"] == "" {
		t.Fatalf("hydrated metadata = id:%v link:%v", result["metadata_file_id"], result["metadata_link"])
	}
}

// TestStockJobResult_ToResultMap_AllFieldsPopulated verifies that every
// field survives the round-trip through ToResultMap() with the correct
// key in the result map and the correct typed value.
func TestStockJobResult_ToResultMap_AllFieldsPopulated(t *testing.T) {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		JobID:         "test-job-123",
		WorkflowID:    "wf-roundtrip",
	}
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

	r := StockJobResult{
		Manifest:                manifest,
		FinalStatus:             "SUCCEEDED",
		TotalClips:              42,
		TotalChunks:             7,
		Chunks:                  []ChunkResult{{Index: 1, TimelineStart: 0, TimelineEnd: 25.0}},
		MetadataLink:            "https://drive.example.com/metadata.json",
		MetadataFileID:          "abc123def456",
		FinalizationStatus:      "completed",
		FinalizationCompletedAt: now,
	}

	m := r.ToResultMap()

	// ── Always-present fields ──────────────────────────────────
	if got, ok := m[job.ManifestKey].(*job.ArtifactManifest); !ok {
		t.Errorf("key %q missing or wrong type: %T", job.ManifestKey, m[job.ManifestKey])
	} else if got != manifest {
		t.Errorf("Manifest pointer mismatch: got %p, want %p (same instance must survive)", got, manifest)
	}

	assertString(t, m, "final_status", "SUCCEEDED")
	assertInt(t, m, "total_clips", 42)
	assertInt(t, m, "total_chunks", 7)

	if chunks, ok := m["chunks"].([]ChunkResult); !ok {
		t.Errorf("chunks missing or wrong type: %T", m["chunks"])
	} else if len(chunks) != 1 || chunks[0].Index != 1 {
		t.Errorf("chunks = %+v, want 1 entry with Index=1", chunks)
	}

	assertString(t, m, "metadata_link", "https://drive.example.com/metadata.json")
	assertString(t, m, "metadata_file_id", "abc123def456")
	if stages, ok := m["stages"].([]StageSnapshot); !ok || stages != nil {
		t.Errorf("stages = %v (%T), want nil []StageSnapshot", m["stages"], m["stages"])
	}

	// ── omitempty-populated fields (non-zero) ──────────────────
	assertString(t, m, "__finalization_status", "completed")

	fc, ok := m["__finalization_completed_at"].(time.Time)
	if !ok {
		t.Errorf("__finalization_completed_at missing or wrong type: %T", m["__finalization_completed_at"])
	} else if !fc.Equal(now) {
		t.Errorf("__finalization_completed_at = %v, want %v", fc, now)
	}
}

// TestStockJobResult_ToResultMap_OmitemptyFinalizationStatus verifies
// that an empty FinalizationStatus is omitted from the result map
// (omitempty contract: zero-value string → key absent).
func TestStockJobResult_ToResultMap_OmitemptyFinalizationStatus(t *testing.T) {
	r := StockJobResult{
		Manifest: &job.ArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
			JobID:         "omitempty-test",
		},
		FinalStatus:        "INDEX_PENDING",
		FinalizationStatus: "", // zero value — must be omitted
	}
	m := r.ToResultMap()

	if _, exists := m["__finalization_status"]; exists {
		t.Errorf("__finalization_status key present for empty FinalizationStatus (omitempty violation)")
	}
	assertString(t, m, "final_status", "INDEX_PENDING")
}

// TestStockJobResult_ToResultMap_OmitemptyFinalizationCompletedAt verifies
// that a zero time.Time is omitted from the result map.
func TestStockJobResult_ToResultMap_OmitemptyFinalizationCompletedAt(t *testing.T) {
	r := StockJobResult{
		Manifest: &job.ArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
			JobID:         "zero-time-test",
		},
		FinalStatus:             "SUCCEEDED",
		FinalizationStatus:      "done",
		FinalizationCompletedAt: time.Time{}, // zero value — must be omitted
	}
	m := r.ToResultMap()

	if _, exists := m["__finalization_completed_at"]; exists {
		t.Errorf("__finalization_completed_at key present for zero time (omitempty violation)")
	}
	assertString(t, m, "__finalization_status", "done")
	assertString(t, m, "final_status", "SUCCEEDED")
}

// TestStockJobResult_ToResultMap_NilManifest verifies that a nil
// Manifest does not panic and is correctly represented as nil in
// the result map.
func TestStockJobResult_ToResultMap_NilManifest(t *testing.T) {
	r := StockJobResult{
		Manifest:    nil,
		FinalStatus: "FAILED",
	}
	m := r.ToResultMap()

	v, ok := m[job.ManifestKey]
	if !ok {
		t.Errorf("key %q missing (nil manifest must still be present)", job.ManifestKey)
	}
	// Go interface-footgun: interface{}((*T)(nil)) != nil.
	// Use a type assertion to unwrap the typed nil value.
	if mv, ok := v.(*job.ArtifactManifest); !ok || mv != nil {
		t.Errorf("key %q = %v (%T), want nil *ArtifactManifest", job.ManifestKey, v, v)
	}
	assertString(t, m, "final_status", "FAILED")
}

// TestStockJobResult_ToResultMap_EmptyChunks verifies that a nil
// Chunks slice is correctly represented in the result map.
func TestStockJobResult_ToResultMap_EmptyChunks(t *testing.T) {
	r := StockJobResult{
		Manifest: &job.ArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
		},
		FinalStatus: "SUCCEEDED",
		Chunks:      nil,
	}
	m := r.ToResultMap()

	v, ok := m["chunks"]
	if !ok {
		t.Errorf("chunks key missing (nil chunks must still be present)")
	}
	// Go interface-footgun: interface{}(([]T)(nil)) != nil.
	// Use a type assertion to unwrap the typed nil slice.
	if chunks, ok := v.([]ChunkResult); !ok || chunks != nil {
		t.Errorf("chunks = %v (%T), want nil []ChunkResult", v, v)
	}
}

// TestStockJobResult_ToResultMap_ManifestKeyConstant verifies that
// job.ManifestKey is "__artifact_manifest" — the wire key the
// broker's downstream runner reads per domain/job.ManifestKey.
func TestStockJobResult_ToResultMap_ManifestKeyConstant(t *testing.T) {
	if job.ManifestKey != "__artifact_manifest" {
		t.Errorf("job.ManifestKey = %q, want %q (wire-format contract: broker runner reads this key)",
			job.ManifestKey, "__artifact_manifest")
	}
}

// TestStockJobResult_ToResultMap_AllFieldsZero verifies the zero-value
// round-trip: all fields at their zero value, only the 7 always-present
// keys exist in the map.
func TestStockJobResult_ToResultMap_AllFieldsZero(t *testing.T) {
	r := StockJobResult{}
	m := r.ToResultMap()

	// 8 always-present keys, including the canonical stage snapshot.
	wantKeys := 8
	if len(m) != wantKeys {
		t.Errorf("map has %d keys, want %d (zero-valued omitempty fields must be absent): %v", len(m), wantKeys, keysOf(m))
	}

	// omitempty fields must be absent
	for _, omitKey := range []string{"__finalization_status", "__finalization_completed_at"} {
		if _, exists := m[omitKey]; exists {
			t.Errorf("omitempty key %q present for zero-valued struct", omitKey)
		}
	}
}

// ── Helpers ──────────────────────────────────────────────────────────

func assertString(t *testing.T, m map[string]any, key, want string) {
	t.Helper()
	got, ok := m[key].(string)
	if !ok {
		t.Errorf("key %q missing or wrong type: %T, want string = %q", key, m[key], want)
		return
	}
	if got != want {
		t.Errorf("key %q = %q, want %q", key, got, want)
	}
}

func assertInt(t *testing.T, m map[string]any, key string, want int) {
	t.Helper()
	got, ok := m[key].(int)
	if !ok {
		t.Errorf("key %q missing or wrong type: %T, want int = %d", key, m[key], want)
		return
	}
	if got != want {
		t.Errorf("key %q = %d, want %d", key, got, want)
	}
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
