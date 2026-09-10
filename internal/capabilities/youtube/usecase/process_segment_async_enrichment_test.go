// Package usecase — process_segment_async_enrichment_test.go pins the
// durable async metadata-enrichment contract (Sept 2026):
//
//   - VELOX_YOUTUBE_ASYNC_ENRICHMENT on  → the clip commits WITHOUT inline
//     LLM analysis and the commit carries a serialized enrichment payload
//     (metadata.enrich.requested) for the outbox worker.
//   - gate off (default)                 → inline analysis runs exactly once
//     and no enrichment intent is emitted.
//
// The same-input guarantee (both modes build the analyzer input through
// buildClipMetadataInput) is asserted through the serialized payload.
package usecase

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// asyncEnrichRecorder records the full localized commit command so tests can
// assert on MetadataEnrichmentJSON.
type asyncEnrichRecorder struct {
	captured localized.CommitLocalizedClipCommand
	calls    int
}

func (r *asyncEnrichRecorder) CommitClipTextAndIndexEvent(_ context.Context, cmd localized.CommitLocalizedClipCommand) error {
	r.captured = cmd
	r.calls++
	return nil
}

func asyncEnrichCommand(videoID, name string) youtubetypes.ProcessSegmentCommand {
	return youtubetypes.ProcessSegmentCommand{
		VideoID: videoID,
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: name},
		Index:   0,
	}
}

// TestExecute_AsyncEnrichmentGate_EmitsEnrichmentIntent proves the gate
// defers the LLM out of the critical path while keeping the clip commit.
func TestExecute_AsyncEnrichmentGate_EmitsEnrichmentIntent(t *testing.T) {
	t.Setenv("VELOX_YOUTUBE_ASYNC_ENRICHMENT", "true")

	svc, builder := newRecordingMetadataService(t)
	writer := &asyncEnrichRecorder{}

	core, media, metadata, observability := validProcessSegmentDeps()
	core.Cache = &alwaysHitCache{item: &youtubetypes.ExtractItem{
		Filename:      "yt_yt_async_enrich_0_10_v1.mp4",
		Duration:      10,
		LegacyFileMD5: "async-enrich-hash",
		DriveFileID:   "async-enrich-drive-file",
	}}
	metadata.LocalizedWriter = writer
	metadata.MetadataService = svc

	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)
	cmd := asyncEnrichCommand("yt_async_enrich", "AsyncEnrich")
	cmd.OutDir = t.TempDir()

	out, execErr := uc.Execute(context.Background(), cmd)
	require.NoError(t, execErr)
	require.Equal(t, "processed", out.Status)

	require.Equal(t, int32(0), atomic.LoadInt32(&builder.calls),
		"async gate must NOT run the LLM analyzer inline")
	require.Equal(t, 1, writer.calls, "clip must still be committed")
	require.NotEmpty(t, writer.captured.MetadataEnrichmentJSON,
		"async gate must emit a durable enrichment payload in the same commit")

	var in youtubetypes.ClipMetadataInput
	require.NoError(t, json.Unmarshal([]byte(writer.captured.MetadataEnrichmentJSON), &in),
		"the enrichment payload must be the serialized ClipMetadataInput")
	require.Equal(t, writer.captured.Clip.ID, in.ClipID)
	require.Equal(t, "AsyncEnrich", in.Title)
}

// TestExecute_SyncEnrichmentDefault_NoEnrichmentIntent proves the default
// (gate off) stays on the fail-closed synchronous path.
func TestExecute_SyncEnrichmentDefault_NoEnrichmentIntent(t *testing.T) {
	t.Setenv("VELOX_YOUTUBE_ASYNC_ENRICHMENT", "")

	svc, builder := newRecordingMetadataService(t)
	writer := &asyncEnrichRecorder{}

	core, media, metadata, observability := validProcessSegmentDeps()
	core.Cache = &alwaysHitCache{item: &youtubetypes.ExtractItem{
		Filename:      "yt_yt_sync_enrich_0_10_v1.mp4",
		Duration:      10,
		LegacyFileMD5: "sync-enrich-hash",
		DriveFileID:   "sync-enrich-drive-file",
	}}
	metadata.LocalizedWriter = writer
	metadata.MetadataService = svc

	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)
	cmd := asyncEnrichCommand("yt_sync_enrich", "SyncEnrich")
	cmd.OutDir = t.TempDir()

	out, execErr := uc.Execute(context.Background(), cmd)
	require.NoError(t, execErr)
	require.Equal(t, "processed", out.Status)

	require.Equal(t, int32(1), atomic.LoadInt32(&builder.calls),
		"default path must run the analyzer inline exactly once")
	require.Empty(t, writer.captured.MetadataEnrichmentJSON,
		"default path must not emit an async enrichment intent")
}
