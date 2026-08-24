// Package stockpipeline — step_extract_clips_test.go
// (PR-STOCK-TIMESTAMP-CLIPS Front 4, July 2026).
//
// TDD contract test for StockExtractClipsStep rich-asset write.
// Front 4 replaces the prior 4-field asset.Asset literal (ID +
// Name + Source + MediaType) with a 10-field rich write so
// downstream consumers (Qdrant indexer, asset search, media_assets
// projection) see Title/Description/Round/Tags/Category/LocalPath/
// SHA256/StartSec/EndSec/SearchText at the asset-row write seam
// instead of only in metadata.json emitted later.
//
// godlike/07 fail-closed contracts verified:
//   - Name = "round-7" (slug derived from Title "Round 7")
//   - Filename = "round-7.mp4"
//   - Category = "Boxe" (direct field)
//   - Tags = ["boxing", "pacquiao"] (defensive copy, order preserved)
//   - Metadata["title"] = "Round 7" (raw title preserved verbatim)
//   - Metadata["description"] = "Pacquiao steps in with a quick left cross"
//   - Metadata["round"] = 7
//   - Metadata["start_sec"] = 32
//   - Metadata["end_sec"] = 51
//   - Metadata["slug"] = "round-7"
//   - Metadata["local_path"] = the cutter OutputPath
//   - Metadata["sha256"] non-empty + matches the fileHash argument
//     passed to writer.WriteAndEnqueue (godlike/06 SSOT: read-back
//     mirrors write-side; the fileHash param + the metadata
//     file_hash key carry the same SHA-256)
//   - SearchText contains the canonical segments: title, description,
//     round, category, tags, start_sec, end_sec.
package assets

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// recordingWriter captures the asset + fileHash arguments to
// WriteAndEnqueue for the test assertion surface. It is the
// canonical "write-side observed" seam (godlike/06 SSOT one
// canonical observer per fact).
type recordingWriter struct {
	calls      int
	clips      []*asset.Asset
	fileHashes []string
	// clip/fileHash are the LAST write observed (convenience accessors
	// used by single-write tests).
	clip     *asset.Asset
	fileHash string
}

func (w *recordingWriter) WriteAndEnqueue(_ context.Context, clip *asset.Asset, fileHash string) error {
	w.calls++
	w.clips = append(w.clips, clip)
	w.fileHashes = append(w.fileHashes, fileHash)
	w.clip = clip
	w.fileHash = fileHash
	return nil
}

// byLogicalID returns the recorded clip with the given logical ID,
// or nil if no such clip was written.
func (w *recordingWriter) byLogicalID(id string) *asset.Asset {
	for _, c := range w.clips {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// extractClipsFakeRunner is the canonical "extract-clips-isolated"
// test fixture. It embeds fakeStepRunner from step_plan_clips_test.go
// (proven pattern) and overrides ONLY the accessors the
// StockExtractClipsStep actually calls (Cutter, Writer, State via
// the embedded state field).
type extractClipsFakeRunner struct {
	*fakeStepRunner
	writer       TransactionalAssetWriter
	cutter       VideoCutter
	artifactPrep finalization.ArtifactPreparationService
}

func (r *extractClipsFakeRunner) Cutter() VideoCutter { return r.cutter }
func (r *extractClipsFakeRunner) Writer() TransactionalAssetWriter {
	return r.writer
}
func (r *extractClipsFakeRunner) ArtifactPreparation() finalization.ArtifactPreparationService {
	return r.artifactPrep
}

// deterministicRichCutter is a focused VideoCutter stub that
// returns a single Succeeded item with the configured output path.
// Front 4 needs a real file on disk at the returned path (the
// step's job.ComputeSHA256 call would otherwise fail-closed), so
// the test pre-creates the file before invoking step.Run.
type deterministicRichCutter struct {
	outputPath string
}

func (d *deterministicRichCutter) Cut(_ context.Context, req CutRequest) (CutBatchResult, error) {
	items := make([]CutItemResult, len(req.Jobs))
	for i, j := range req.Jobs {
		items[i] = CutItemResult{
			JobID:      j.OutputPath,
			OutputPath: d.outputPath,
			Status:     CutItemStatusSucceeded,
			SizeBytes:  1024,
		}
	}
	return CutBatchResult{
		SourcePath: req.SourcePath,
		Items:      items,
	}, nil
}

// recordingCutter is a focused VideoCutter stub that records the
// number of Cut() invocations. PR-STOCK-TIMESTAMP-CLIPS Front 5
// uses it to assert the probe-before-cut contract: when the
// pre-cut duration validation fails, the cutter must NOT be
// invoked (the step aborts before reaching cutter.Cut). The
// returned CutBatchResult is empty (no items) so any code path
// that erroneously reaches the cutter surface immediately
// surfaces a "zero cut files" production gate (terminal error
// per the existing stock.extract_clips zero-cut-files gate).
type recordingCutter struct {
	calls int
}

func (r *recordingCutter) Cut(_ context.Context, req CutRequest) (CutBatchResult, error) {
	r.calls++
	return CutBatchResult{
		SourcePath: req.SourcePath,
		Items:      nil,
	}, nil
}

// batchRecordingCutter records every CutRequest it receives so
// tests can assert batch-cutting behaviour: one CutRequest per
// source group containing all CutJobs for that group.
type batchRecordingCutter struct {
	mu       sync.Mutex
	requests []CutRequest
}

func (b *batchRecordingCutter) Cut(_ context.Context, req CutRequest) (CutBatchResult, error) {
	b.mu.Lock()
	b.requests = append(b.requests, req)
	b.mu.Unlock()
	items := make([]CutItemResult, len(req.Jobs))
	for i, j := range req.Jobs {
		// Create the output file so the step's SHA256 compute succeeds.
		if err := os.WriteFile(j.OutputPath, []byte("fake-clip-"+j.OutputPath), 0o644); err != nil {
			return CutBatchResult{}, fmt.Errorf("batchRecordingCutter: create output file %q: %w", j.OutputPath, err)
		}
		items[i] = CutItemResult{
			JobID:      j.OutputPath,
			OutputPath: j.OutputPath,
			Status:     CutItemStatusSucceeded,
			SizeBytes:  1024,
		}
	}
	return CutBatchResult{
		SourcePath: req.SourcePath,
		Items:      items,
	}, nil
}

// concurrencyTrackingArtifactPrep is a fake ArtifactPreparationService
// that records the maximum number of concurrent Prepare calls. It is
// used to verify that the stock.extract_clips upload worker pool
// respects its concurrency limit.
type concurrencyTrackingArtifactPrep struct {
	mu            sync.Mutex
	current       int
	maxConcurrent int
	calls         int
	delay         time.Duration
}

func (p *concurrencyTrackingArtifactPrep) Prepare(_ context.Context, artifact finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	p.mu.Lock()
	p.current++
	if p.current > p.maxConcurrent {
		p.maxConcurrent = p.current
	}
	p.calls++
	p.mu.Unlock()

	if p.delay > 0 {
		time.Sleep(p.delay)
	}

	p.mu.Lock()
	p.current--
	p.mu.Unlock()

	return finalization.PublishedArtifact{
		ArtifactID: artifact.ArtifactID,
		Filename:   artifact.Filename,
		MIMEType:   artifact.MIMEType,
		SizeBytes:  artifact.SizeBytes,
		SHA256:     artifact.SHA256,
		Location: finalization.AssetLocation{
			Provider:    "drive",
			FileID:      "file-" + artifact.ArtifactID,
			WebViewLink: "https://drive.google.com/file/d/file-" + artifact.ArtifactID,
		},
	}, nil
}

// TestStockExtractClips_BatchPerSourceGroup asserts the batch-cutting
// contract: all ClipPlan entries sharing the same SourceID are folded
// into a single CutRequest with multiple CutJobs, and the cutter is
// invoked exactly once per source group (not once per clip).
