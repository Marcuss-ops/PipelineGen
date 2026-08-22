// Package usecase — process_segment_cachehit_finalization_test.go.
//
// PR-CACHE-HIT-FINALIZATION (godlike/06 SSOT): a binary cache hit must
// skip ONLY acquisition/cut + ffprobe (Steps 3-5 + 5a); the canonical
// enrichment/finalization gate (Steps 6-9) must STILL run on the cached
// binary so a cache hit REPAIRS missing/stale metadata, text tracks and
// the index request instead of short-circuiting before the semantic
// snapshot exists. The legacy "skipped" early-return is retired.
package usecase

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
)

// countingVideoPipeline records DownloadAndCutYouTubeVideo calls so the
// cache-hit tests can prove acquisition/cut is skipped on a binary cache hit.
type countingVideoPipeline struct {
	stubVideoPipeline
	calls int32
}

func (v *countingVideoPipeline) DownloadAndCutYouTubeVideo(_ context.Context, _ youtubeports.VideoCutRequest) (*youtubeports.VideoCutResult, error) {
	atomic.AddInt32(&v.calls, 1)
	return &youtubeports.VideoCutResult{LocalPath: "unused"}, nil
}

// compile-time assertion: countingVideoPipeline satisfies VideoPipelinePort.
var _ youtubeports.VideoPipelinePort = (*countingVideoPipeline)(nil)

// TestExecute_CacheHit_RunsMetadataEnrichment is the canonical regression
// for PR-CACHE-HIT-FINALIZATION: on a binary cache hit the canonical
// finalization gate (now INSIDE step6to9, per PR-ASSET-COMMITTER-
// ENRICHMENT) MUST still run even though acquisition/cut is skipped.
// The recording Builder is invoked exactly once (AnalyzeClip →
// GenerateClipMetadata → Build), the enriched snapshot is folded into the
// ClipAsset, and the canonical commit STILL fires — proving the cached
// clip is re-enriched AND re-committed rather than reported as "skipped".
func TestExecute_CacheHit_RunsMetadataEnrichment(t *testing.T) {
	vpipe := &countingVideoPipeline{}
	svc, builder := newRecordingMetadataService(t)
	writer := &stubWriterAssetRecorder{}

	core, media, metadata, observability := validProcessSegmentDeps()
	core.VideoPipeline = vpipe
	core.Writer = writer
	core.Cache = &alwaysHitCache{item: &youtubetypes.ExtractItem{
		Filename:    "yt_yt_cachehit_meta_0_10_v1.mp4",
		Duration:    10,
		LegacyFileMD5:    "cache-hit-hash",
		DriveFileID: "cache-hit-drive-file",
	}}
	metadata.MetadataService = svc

	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_cachehit_meta",
		OutDir:  t.TempDir(),
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "CacheHitMeta"},
		Index:   0,
	}

	out, execErr := uc.Execute(context.Background(), cmd)
	require.NoError(t, execErr, "Execute must succeed on cache hit")
	require.Equal(t, "processed", out.Status, "cache hit must finalize through the enrichment gate")

	if got := atomic.LoadInt32(&vpipe.calls); got != 0 {
		t.Fatalf("cache hit must skip acquisition/cut: VideoPipeline invoked %d times", got)
	}
	if got := atomic.LoadInt32(&builder.calls); got != 1 {
		t.Fatalf("cache hit must run metadata analysis: builder invoked %d times, want 1", got)
	}

	// The canonical commit MUST still fire on a cache hit, carrying the
	// FOLDED enrichment (quality score from the analyzer) so missing/stale
	// metadata is REPAIRED — not just re-analyzed in a void.
	if writer.calls != 1 {
		t.Fatalf("cache hit must still commit: writer invoked %d times, want 1", writer.calls)
	}
	if writer.captured.Metadata.QualityScore <= 0 {
		t.Fatalf("cache hit commit must carry the folded enrichment: QualityScore = %v, want > 0", writer.captured.Metadata.QualityScore)
	}
	if writer.captured.SearchText == "" {
		t.Fatal("cache hit commit must carry a non-empty recomputed search text")
	}
}

// TestExecute_CacheHit_RepairsMissingTranscript pins the transcript
// REPAIR half of PR-CACHE-HIT-FINALIZATION: when a cached binary still
// has a local file but its text tracks are missing, the canonical
// finalization gate (step6to9) must still run the 5-level chain and
// re-acquire the transcript via the Whisper fallback (priority 5) — a
// cache hit skips acquisition/cut, NOT transcript repair.
func TestExecute_CacheHit_RepairsMissingTranscript(t *testing.T) {
	realPath := filepath.Join(t.TempDir(), "yt_yt_cachehit_tx_0_10_v1.mp4")
	require.NoError(t, os.WriteFile(realPath, []byte("fake audio bytes for cache-hit transcript repair"), 0o644))

	tport := &countingTranscriber{text: "re-acquired transcript"}

	core, media, metadata, observability := validProcessSegmentDeps()
	core.Cache = &alwaysHitCache{item: &youtubetypes.ExtractItem{
		Filename:    "yt_yt_cachehit_tx_0_10_v1.mp4",
		Duration:    10,
		LegacyFileMD5:    "cache-hit-hash",
		DriveFileID: "cache-hit-drive-file",
		LocalPath:   realPath,
	}}
	// DB miss (noRowsRepo) + subtitle miss (noSubtitleFetcher) force the
	// chain to priority 5 (Whisper), which needs the cached local file.
	media.TextTrackResolver = &TextTrackResolver{
		Repo:        noRowsRepo{},
		Subtitles:   noSubtitleFetcher{},
		Transcriber: tport,
		Log:         zap.NewNop(),
	}

	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_cachehit_tx",
		OutDir:  t.TempDir(),
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "CacheHitTx"},
		Index:   0,
	}

	_, execErr := uc.Execute(context.Background(), cmd)
	require.NoError(t, execErr, "Execute must succeed on cache hit")

	if got := atomic.LoadInt32(&tport.calls); got != 1 {
		t.Fatalf("cache hit must re-acquire the missing transcript via Whisper: %d calls, want 1", got)
	}
}
