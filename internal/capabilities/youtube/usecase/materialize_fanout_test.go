// Package usecase — materialize_fanout_test.go: regression suite for the
// post-commit multilingual fan-out (POSTGRES-MEDIA-CUTOVER follow-up,
// September 2026).
//
// Before this seam the direct YouTube extraction path committed clip +
// transcript and stopped: no `asset.text.materialize` job was scheduled, so
// a freshly extracted clip produced only the languages the acquisition chain
// happened to find while every other configured target language was never
// generated.
package usecase

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// fanoutRecordingWriter captures every text track the canonical super-tx
// received, so the test can compare the fan-out payload against what was
// actually persisted.
type fanoutRecordingWriter struct {
	stubAtomicWriter
	calls  int
	clip   youtubetypes.ClipAsset
	tracks []detail.TextTrack
}

func (w *fanoutRecordingWriter) CommitClipTextAndIndexEvent(_ context.Context, cmd localized.CommitLocalizedClipCommand) error {
	w.calls++
	w.clip = cmd.Clip
	w.tracks = cmd.TextTracks
	return nil
}

type materializeCall struct {
	assetID        string
	sourceLanguage string
	sourceTextHash string
}

type acquireCall struct {
	assetID        string
	sourceLanguage string
}

type fakeMaterializeFanOut struct {
	materialize []materializeCall
	acquire     []acquireCall
	defaultLang string
}

func (f *fakeMaterializeFanOut) EnqueueMaterializeOne(_ context.Context, assetID, sourceLanguage, sourceTextHash string, _ []detail.TextTrackKind) error {
	f.materialize = append(f.materialize, materializeCall{
		assetID:        assetID,
		sourceLanguage: sourceLanguage,
		sourceTextHash: sourceTextHash,
	})
	return nil
}

func (f *fakeMaterializeFanOut) EnqueueAcquireOne(_ context.Context, assetID, sourceLanguage string, _ []detail.TextTrackKind) error {
	f.acquire = append(f.acquire, acquireCall{assetID: assetID, sourceLanguage: sourceLanguage})
	return nil
}

func (f *fakeMaterializeFanOut) DefaultSourceLanguage() string { return f.defaultLang }

// compile-time assertion: the fake satisfies the port the pipeline consumes,
// and — separately — the real helper must satisfy it too. The latter is
// pinned in internal/capabilities/assets/texttracks; here we only lock the
// use-case-side contract.
var _ MaterializeFanOutPort = (*fakeMaterializeFanOut)(nil)

// TestExecute_SchedulesMaterializeAfterCommitWithPersistedHash pins the
// happy path: a committed clip with a resolved transcript schedules the
// canonical materialize job carrying EXACTLY the language and text hash the
// super-tx persisted (the materializer re-reads that row and fails closed on
// any mismatch).
func TestExecute_SchedulesMaterializeAfterCommitWithPersistedHash(t *testing.T) {
	realPath := filepath.Join(t.TempDir(), "yt_fanout_0_10_v1.mp4")
	require.NoError(t, os.WriteFile(realPath, []byte("fake audio bytes"), 0o644))

	transcriber := &countingTranscriber{text: "the canonical transcript"}
	writer := &fanoutRecordingWriter{}
	fanout := &fakeMaterializeFanOut{defaultLang: "en"}

	core, media, metadata, observability := validProcessSegmentDeps()
	metadata.LocalizedWriter = writer
	core.Cache = &alwaysHitCache{item: &youtubetypes.ExtractItem{
		Filename:      "yt_fanout_0_10_v1.mp4",
		Duration:      10,
		LegacyFileMD5: "fanout-hash",
		DriveFileID:   "fanout-drive-file",
		LocalPath:     realPath,
	}}
	media.TextTrackResolver = &TextTrackResolver{
		Repo:        noRowsRepo{},
		Subtitles:   noSubtitleFetcher{},
		Transcriber: transcriber,
		Log:         zap.NewNop(),
	}
	media.MaterializeFanOut = fanout

	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)
	out, err := uc.Execute(context.Background(), youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_fanout",
		OutDir:  t.TempDir(),
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "Fanout"},
	})
	require.NoError(t, err)
	require.Equal(t, "processed", out.Status)
	require.Equal(t, 1, writer.calls, "the canonical super-tx must run exactly once")

	require.Empty(t, fanout.acquire, "a resolved transcript must NOT take the acquire path")
	require.Len(t, fanout.materialize, 1, "a committed clip must schedule exactly one materialize job")

	var committed *detail.TextTrack
	for i := range writer.tracks {
		if writer.tracks[i].TextKind == detail.TextTrackTranscript && writer.tracks[i].Status == detail.TextTrackReady {
			committed = &writer.tracks[i]
			break
		}
	}
	require.NotNil(t, committed, "the commit must carry a READY transcript track")

	got := fanout.materialize[0]
	require.Equal(t, committed.AssetID, got.assetID)
	require.Equal(t, committed.LanguageCode, got.sourceLanguage,
		"the fan-out must use the persisted track's language")
	require.Equal(t, string(committed.TextHash), got.sourceTextHash,
		"the fan-out must carry the persisted track's TextHash (the materializer fails closed on a mismatch)")
}

// TestExecute_SchedulesAcquireWithoutTranscript pins the fallback: a clip
// committed WITHOUT any transcript still schedules the canonical acquisition
// chain (payload → DB → YouTube manual → YouTube auto → Whisper) before
// translation, using the configured default source language.
func TestExecute_SchedulesAcquireWithoutTranscript(t *testing.T) {
	realPath := filepath.Join(t.TempDir(), "yt_fanout_none_0_10_v1.mp4")
	require.NoError(t, os.WriteFile(realPath, []byte("fake audio bytes"), 0o644))

	writer := &fanoutRecordingWriter{}
	fanout := &fakeMaterializeFanOut{defaultLang: "en"}

	core, media, metadata, observability := validProcessSegmentDeps()
	metadata.LocalizedWriter = writer
	core.Cache = &alwaysHitCache{item: &youtubetypes.ExtractItem{
		Filename:      "yt_fanout_none_0_10_v1.mp4",
		Duration:      10,
		LegacyFileMD5: "fanout-none-hash",
		DriveFileID:   "fanout-none-drive-file",
		LocalPath:     realPath,
	}}
	// No TextTrackResolver → no bundle is acquired in the segment pipeline.
	media.MaterializeFanOut = fanout

	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)
	out, err := uc.Execute(context.Background(), youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_fanout_none",
		OutDir:  t.TempDir(),
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "FanoutNone"},
	})
	require.NoError(t, err)
	require.Equal(t, "processed", out.Status)
	require.Equal(t, 1, writer.calls)

	require.Empty(t, fanout.materialize, "no transcript → no materialize job")
	require.Len(t, fanout.acquire, 1, "no transcript → the canonical acquire chain is scheduled")
	require.Equal(t, "en", fanout.acquire[0].sourceLanguage)
}

// TestExecute_NoFanOutPortIsANoOp pins the back-compatible posture: every
// composition without a wired fan-out behaves exactly as before.
func TestExecute_NoFanOutPortIsANoOp(t *testing.T) {
	realPath := filepath.Join(t.TempDir(), "yt_fanout_off_0_10_v1.mp4")
	require.NoError(t, os.WriteFile(realPath, []byte("fake audio bytes"), 0o644))

	writer := &fanoutRecordingWriter{}
	core, media, metadata, observability := validProcessSegmentDeps()
	metadata.LocalizedWriter = writer
	core.Cache = &alwaysHitCache{item: &youtubetypes.ExtractItem{
		Filename:      "yt_fanout_off_0_10_v1.mp4",
		Duration:      10,
		LegacyFileMD5: "fanout-off-hash",
		DriveFileID:   "fanout-off-drive-file",
		LocalPath:     realPath,
	}}

	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)
	out, err := uc.Execute(context.Background(), youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_fanout_off",
		OutDir:  t.TempDir(),
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "FanoutOff"},
	})
	require.NoError(t, err)
	require.Equal(t, "processed", out.Status)
	require.Equal(t, 1, writer.calls)
}

// TestService_WithMaterializeFanOut_WiresThePipeline pins the late-binding
// seam the composition root uses: after WithMaterializeFanOut, the
// per-segment pipeline carries the port.
func TestService_WithMaterializeFanOut_WiresThePipeline(t *testing.T) {
	fanout := &fakeMaterializeFanOut{defaultLang: "en"}
	uc := NewProcessYouTubeSegmentFromSubBundles(validProcessSegmentDeps())

	svc := &Service{processSeg: uc}
	svc.WithMaterializeFanOut(fanout)

	if uc.media.MaterializeFanOut == nil {
		t.Fatal("WithMaterializeFanOut must populate the per-segment pipeline's port")
	}

	// nil-receiver safety (godlike/07: disabled composition is an observable
	// no-op, never a panic).
	var nilSvc *Service
	nilSvc.WithMaterializeFanOut(fanout)
}
