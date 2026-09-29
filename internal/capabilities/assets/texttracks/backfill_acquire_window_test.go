// Package texttracks — backfill_acquire_window_test.go: pins the SOURCE
// WINDOW the backfill hands to the acquisition chain (PR-SUBS-CLIP-WINDOW).
//
// Priorities 2/2.5/5 read the CLIP file itself and need no window; priority
// 3+4 (YouTube subtitles) reads the FULL source video's VTT. Before this
// fix tryAcquire left AcquireCommand.StartSec/EndSec at zero, so the port
// was asked for the WHOLE video and attached whole-video cues to a 30s clip
// — the .ass validation then rejected them (last cue end ≫ clip duration)
// or the clip render trimmed every cue away.
package texttracks

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWindowTestService wires a BackfillService whose acquisition chain stops
// at the subtitle stub (no local file on disk, no Drive copy, no Whisper).
func newWindowTestService(t *testing.T, subs *stubSubtitles) *BackfillService {
	t.Helper()
	acquirer, err := NewAcquireService(subs, &stubWhisper{}, zap.NewNop())
	require.NoError(t, err)
	return &BackfillService{
		repo:     newFakeRepo(),
		cues:     &cueWriterStub{},
		acquirer: acquirer,
		log:      zap.NewNop(),
	}
}

// windowTestAsset builds a YouTube clip asset that starts `startSec` seconds
// into its source video and lasts `dur`. startSecMetadata=false omits the
// start_sec metadata so the asset-id fallback can be exercised.
func windowTestAsset(id string, startSec float64, dur time.Duration, withStartMeta bool) *asset.Asset {
	a := &asset.Asset{
		ID:        id,
		Source:    asset.Source("youtube"),
		Filename:  "clip.mp4",
		Duration:  dur,
		SourceURL: "https://www.youtube.com/watch?v=abc123",
	}
	if withStartMeta {
		a.SetStartSec(startSec)
	}
	return a
}

func windowTestOptions() BackfillOptions {
	return BackfillOptions{Source: "youtube", SourceLanguage: "en", TextKind: detail.TextTrackTranscript}
}

// The canonical case: metadata start_sec + duration define the window the
// source VTT must be sliced to, and the returned cues arrive clip-local.
func TestTryAcquire_PassesClipWindowFromMetadata(t *testing.T) {
	subs := &stubSubtitles{bundle: &detail.ResolvedTextBundle{
		LanguageCode: "en",
		PlainText:    "slice of the source video",
		Cues: []detail.TimedCue{
			{StartMs: 146_000, EndMs: 148_000, Text: "first"},
			{StartMs: 148_000, EndMs: 176_000, Text: "second"},
		},
		SourceType: detail.TextSourceYouTubeSubtitle,
		IsOriginal: true,
	}}
	svc := newWindowTestService(t, subs)

	res, err := svc.tryAcquire(context.Background(),
		windowTestAsset("yt_abc123_146_176_v1", 146, 30*time.Second, true),
		windowTestOptions())
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Equal(t, 146, subs.gotStart, "the clip's source start must reach the subtitle port")
	assert.Equal(t, 176, subs.gotEnd, "start + clip duration must define the window end")
	require.Len(t, res.Cues, 2)
	assert.Equal(t, int64(0), res.Cues[0].StartMs, "saved cues must be on the CLIP timeline")
	assert.LessOrEqual(t, res.Cues[1].EndMs, int64(30_000), "cues must not exceed the clip duration")
}

// Fallback: rows written without start_sec metadata still carry the
// deterministic clip id yt_<videoID>_<start>_<end>_<policy>.
func TestTryAcquire_ParsesWindowFromClipID(t *testing.T) {
	subs := &stubSubtitles{bundle: &detail.ResolvedTextBundle{
		LanguageCode: "en",
		PlainText:    "window from the id",
		Cues:         []detail.TimedCue{{StartMs: 10_000, EndMs: 40_000, Text: "cue"}},
		SourceType:   detail.TextSourceYouTubeSubtitle,
		IsOriginal:   true,
	}}
	svc := newWindowTestService(t, subs)

	res, err := svc.tryAcquire(context.Background(),
		windowTestAsset("yt_abc123_10_40_v1", 0, 0, false),
		windowTestOptions())
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Equal(t, 10, subs.gotStart)
	assert.Equal(t, 40, subs.gotEnd)
	require.Len(t, res.Cues, 1)
	assert.Equal(t, int64(0), res.Cues[0].StartMs, "id-derived window must rebase the cue to 0")
	assert.Equal(t, int64(30_000), res.Cues[0].EndMs)
}

// A clip that starts at 0s still needs an END bound so a boundary cue cannot
// overshoot the clip duration.
func TestTryAcquire_WindowStartsAtZeroWithKnownDuration(t *testing.T) {
	subs := &stubSubtitles{bundle: &detail.ResolvedTextBundle{
		LanguageCode: "en",
		PlainText:    "from the top",
		Cues:         []detail.TimedCue{{StartMs: 0, EndMs: 35_000, Text: "overshooting cue"}},
		SourceType:   detail.TextSourceYouTubeSubtitle,
		IsOriginal:   true,
	}}
	svc := newWindowTestService(t, subs)

	res, err := svc.tryAcquire(context.Background(),
		windowTestAsset("yt_abc123_0_30_v1", 0, 30*time.Second, false),
		windowTestOptions())
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Equal(t, 0, subs.gotStart)
	assert.Equal(t, 30, subs.gotEnd)
	require.Len(t, res.Cues, 1)
	assert.Equal(t, int64(30_000), res.Cues[0].EndMs, "the end-straddling cue must clamp to the clip duration")
}

// Nothing known about the window: keep the legacy whole-video contract
// (0/0) instead of inventing a degenerate start==end window, which the VTT
// window filter would read as "drop every cue".
func TestTryAcquire_UnknownWindowKeepsWholeVideoContract(t *testing.T) {
	subs := &stubSubtitles{bundle: &detail.ResolvedTextBundle{
		LanguageCode: "en",
		PlainText:    "whole video",
		Cues:         []detail.TimedCue{{StartMs: 5_000, EndMs: 9_000, Text: "cue"}},
		SourceType:   detail.TextSourceYouTubeSubtitle,
		IsOriginal:   true,
	}}
	svc := newWindowTestService(t, subs)

	_, err := svc.tryAcquire(context.Background(),
		windowTestAsset("not-a-yt-id", 0, 0, false),
		windowTestOptions())
	require.NoError(t, err)

	assert.Equal(t, 0, subs.gotStart, "unknown window must stay 0/0")
	assert.Equal(t, 0, subs.gotEnd)
}

// A non-YouTube id with a known duration must still bound the window: the
// window filter is what keeps whole-video cues off a 30s clip.
func TestTryAcquire_UnknownStartStillBoundsByDuration(t *testing.T) {
	subs := &stubSubtitles{bundle: &detail.ResolvedTextBundle{
		LanguageCode: "en",
		PlainText:    "bounded",
		Cues:         []detail.TimedCue{{StartMs: 0, EndMs: 9_000, Text: "cue"}},
		SourceType:   detail.TextSourceYouTubeSubtitle,
		IsOriginal:   true,
	}}
	svc := newWindowTestService(t, subs)

	_, err := svc.tryAcquire(context.Background(),
		windowTestAsset("generic-asset-1", 0, 30*time.Second, false),
		windowTestOptions())
	require.NoError(t, err)

	assert.Equal(t, 0, subs.gotStart)
	assert.Equal(t, 30, subs.gotEnd)
}
