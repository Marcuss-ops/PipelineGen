// Package e2e — youtube_subtitles_clip_window_live_test.go: the live
// certificate for the subtitle CLIP-WINDOW contract (PR-SUBS-CLIP-WINDOW).
//
// WHY THIS TEST EXISTS: this deployment runs the acquisition chain with
// media.multilingual.source_priority=whisper_first, so a live
// POST /api/clips/process proves the Whisper leg and never the YouTube-caption
// leg — and the caption leg is exactly the one that used to break. It reads
// the FULL video's VTT and only WINDOW-filters it, so its cues carried
// SOURCE-video timestamps (e.g. 65.000–80.000 ms for a clip cut at 65s) while
// every consumer of a clip's cues expects the clip timeline:
//
//   - texttracks.validateASSFile rejects an artifact whose last cue end
//     exceeds clipDurationMs+250ms → status=FAILED, nothing published;
//   - cliprender.trimClipRenderCues drops every cue with StartMs >= duration
//     → the render ships with no subtitles at all.
//
// The test therefore drives the REAL yt-dlp subtitle fetcher + the REAL
// TextTrackResolver for a window that deliberately does NOT start at 0, and
// asserts the whole certificate in one place:
//
//  1. real captions are downloaded (yt-dlp --skip-download) and parsed;
//  2. the returned cues are CLIP-local: 0 <= start, end <= duration, and the
//     bundle's language is honestly one of the configured ones;
//  3. the .ass compiled from those cues passes texttracks.ValidateASSFile
//     with the real clip duration — the same validator clip.render and the
//     subtitle materializer run.
//
// Hard-gated behind VELOX_E2E_LIVE=1 (godlike/07: a live test that silently
// degrades to a mock is worse than no test), so `go test ./...` stays hermetic.
//
// Required environment:
//
//	VELOX_E2E_LIVE=1          enables this test
//
// Optional environment:
//
//	VELOX_E2E_YOUTUBE_URL     source video (must expose captions)
//	                          default: the runbook canary iHaK0M-207o
//	VELOX_E2E_YTDLP           yt-dlp command (default `yt-dlp`;
//	                          use `bash scripts/yt-dlp-pipeline` when the
//	                          process HOME lacks the pip user install)
//	VELOX_E2E_SUBTITLE_LANGS  configured languages CSV (default `en,it`)
//	VELOX_E2E_CLIP_START_SEC  window start in the SOURCE video (default 65)
//	VELOX_E2E_CLIP_END_SEC    window end in the SOURCE video (default 80)
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	texttracks "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	youtubeusecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	ytplatform "github.com/Marcuss-ops/PipelineGen/internal/platform/youtube"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// liveClipWindow is the [start, end) window inside the SOURCE video that the
// certificate cuts. startSec must be > 0 (that is the regression), and
// endSec-startSec must be shorter than startSec so a source-absolute cue set
// can never accidentally fit inside the clip duration.
func liveClipWindow(t *testing.T) (videoID string, startSec, endSec int) {
	t.Helper()

	rawURL := os.Getenv("VELOX_E2E_YOUTUBE_URL")
	if rawURL == "" {
		rawURL = "https://www.youtube.com/watch?v=iHaK0M-207o"
	}
	id, err := urlutil.ExtractVideoID(rawURL)
	require.NoErrorf(t, err, "VELOX_E2E_YOUTUBE_URL must be a recognizable YouTube URL: %q", rawURL)
	require.NotEmpty(t, id)

	startSec = liveEnvInt(t, "VELOX_E2E_CLIP_START_SEC", 65)
	endSec = liveEnvInt(t, "VELOX_E2E_CLIP_END_SEC", 80)
	require.Greater(t, startSec, 0, "the certificate is ABOUT a window that does not start at 0")
	require.Greater(t, endSec, startSec, "window end must precede start")
	require.Less(t, endSec-startSec, startSec,
		"the clip must be shorter than its start offset, otherwise source-absolute cues could fit by accident")
	return id, startSec, endSec
}

func liveEnvInt(t *testing.T, key string, fallback int) int {
	t.Helper()
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	require.NoErrorf(t, err, "%s must be an integer, got %q", key, raw)
	return v
}

// liveYtDlpCommand turns VELOX_E2E_YTDLP into argv[0] + prefix args. The
// runbook documents the knob as a possibly MULTI-word command
// (`bash scripts/yt-dlp-pipeline`), while the adapter's runner passes its
// single YTDLPPath straight to exec — a multi-word value would be looked up
// as one executable name and fail with ENOENT. A cwd-relative wrapper path is
// also rewritten to absolute: go test runs the binary from tests/e2e/, so
// `scripts/yt-dlp-pipeline` does not resolve from there even though it does
// from the module root the runbook assumes.
func liveYtDlpCommand(t *testing.T) []string {
	t.Helper()
	fields := strings.Fields(liveEnv("VELOX_E2E_YTDLP", "yt-dlp"))
	require.NotEmpty(t, fields, "VELOX_E2E_YTDLP resolved to an empty command")
	for i, field := range fields {
		if !strings.Contains(field, "/") || filepath.IsAbs(field) {
			continue // bare binary name, or already absolute
		}
		if _, err := os.Stat(field); err == nil {
			abs, absErr := filepath.Abs(field)
			require.NoError(t, absErr, "resolve %q", field)
			fields[i] = abs
			continue
		}
		fromRoot := filepath.Join("..", "..", field)
		if _, err := os.Stat(fromRoot); err == nil {
			abs, absErr := filepath.Abs(fromRoot)
			require.NoError(t, absErr, "resolve %q from the module root", field)
			fields[i] = abs
		}
	}
	return fields
}

// liveYtDlpRunner runs the configured (possibly multi-word) command and keeps
// the LAST yt-dlp result. FetchFullVTT deliberately ignores the subprocess
// outcome — "best-effort: no error if yt-dlp can't fetch subs" is the
// production contract — so a live download that fails (network, 403, wrong
// HOME) would otherwise surface only as "acquisition exhausted all 5
// priorities", which names no cause. Recording it lets the certificate fail
// with the real yt-dlp stderr.
type liveYtDlpRunner struct {
	fields     []string
	inner      ytplatform.ProcessRunnerPort
	lastErr    error
	lastOutput string
}

func (r *liveYtDlpRunner) Run(ctx context.Context, _ string, args []string) (string, string, error) {
	full := make([]string, 0, len(r.fields)-1+len(args))
	full = append(full, r.fields[1:]...)
	full = append(full, args...)
	stdout, stderr, err := r.inner.Run(ctx, r.fields[0], full)
	r.lastErr = err
	r.lastOutput = strings.TrimSpace(stdout + "\n" + stderr)
	return stdout, stderr, err
}

// TestLiveYouTube_SubtitleCuesAreClipLocalAndASSValidates is the certificate
// described in the package comment above.
func TestLiveYouTube_SubtitleCuesAreClipLocalAndASSValidates(t *testing.T) {
	if os.Getenv("VELOX_E2E_LIVE") != "1" {
		t.Skip("VELOX_E2E_LIVE not set; skipping the live subtitle clip-window certificate")
	}

	videoID, startSec, endSec := liveClipWindow(t)
	durationMs := int64(endSec-startSec) * 1000

	ytdlpCommand := liveYtDlpCommand(t)
	langs := os.Getenv("VELOX_E2E_SUBTITLE_LANGS")
	if langs == "" {
		langs = "en,it"
	}

	// The REAL fetcher: yt-dlp --write-auto-subs/--write-subs --skip-download
	// into a throwaway cache, then the canonical VTT parser. No fakes on the
	// critical path — this is the leg that silently fell through to Whisper
	// when the language/caching contract was wrong.
	runner := &liveYtDlpRunner{
		fields: ytdlpCommand,
		inner:  ytplatform.NewProcessRunnerAdapter(),
	}
	fetcher := ytplatform.NewSubtitleFetcherAdapter(
		ytplatform.SubtitleCacheConfig{
			YTDLPPath:    ytdlpCommand[0],
			DefaultLangs: langs,
			CacheDir:     t.TempDir(),
		},
		runner, // splits the multi-word command and records the last failure
		nil,    // canonical command builder
		false,
	)

	resolver := &youtubeusecase.TextTrackResolver{
		Subtitles: fetcher,              // priorities 3+4; Repo/Transcriber nil on purpose:
		Log:       zaptest.NewLogger(t), // this certificate is about the caption leg only
	}

	clipID := "yt_" + videoID + "_" + strconv.Itoa(startSec) + "_" + strconv.Itoa(endSec) + "_live"
	bundle, err := resolver.AcquireSegmentText(context.Background(), youtubeusecase.TextTrackAcquireRequest{
		ClipID:             clipID,
		VideoID:            videoID,
		StartSec:           startSec,
		EndSec:             endSec,
		PreferredLanguages: []string{"en", "it"},
	})
	require.NoError(t, err, "caption acquisition must not error for a captioned video")
	if bundle == nil {
		t.Fatalf("real captions must be acquired (no Whisper fallback is wired in this test);\n"+
			"yt-dlp command: %v\nlast yt-dlp err: %v\nlast yt-dlp output:\n%s",
			ytdlpCommand, runner.lastErr, runner.lastOutput)
	}
	require.NotEmpty(t, bundle.PlainText)
	require.NotEmpty(t, bundle.Cues, "a transcript without timed cues cannot produce subtitles")

	// 1. Honest language: the bundle must carry a configured language, i.e.
	// the label comes from the VTT that was actually read.
	require.Contains(t, []string{"en", "it"}, bundle.LanguageCode,
		"the bundle language must be the language of the file actually read")

	// 2. CLIP timeline: every cue inside [0, duration]. Source-absolute cues
	// (the pre-fix behaviour) start near startSec*1000 and end near
	// endSec*1000, both far outside this range.
	for i, cue := range bundle.Cues {
		require.GreaterOrEqualf(t, cue.StartMs, int64(0), "cue %d must not be negative: %+v", i, cue)
		require.Greaterf(t, cue.EndMs, cue.StartMs, "cue %d must have a positive window: %+v", i, cue)
		require.LessOrEqualf(t, cue.EndMs, durationMs,
			"cue %d ends after the %dms clip — the cue is still on the SOURCE timeline: %+v", i, durationMs, cue)
		require.Lessf(t, cue.StartMs, durationMs,
			"cue %d starts after the clip ends — the cue is still on the SOURCE timeline: %+v", i, cue)
	}

	// 3. The .ass compiled from those real cues must pass the canonical
	// validator with the real clip duration — the exact gate that used to
	// mark the artifact FAILED (and that clip.render runs before sealing a
	// plan).
	ass, err := texttracks.CompileASSContent(bundle.Cues, texttracks.SubtitleArtifactStyleID)
	require.NoError(t, err)
	require.Contains(t, ass, "Dialogue:", "the compiled artifact must carry dialogue lines")

	assPath := filepath.Join(t.TempDir(), "subtitles.ass")
	require.NoError(t, os.WriteFile(assPath, []byte(ass), 0o644))
	require.NoErrorf(t, texttracks.ValidateASSFile(assPath, durationMs),
		"the real clip's .ass must pass ValidateASSFile (duration=%dms, cues=%d)", durationMs, len(bundle.Cues))
}
