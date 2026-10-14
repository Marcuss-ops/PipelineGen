// internal/platform/downloader/mirror_sections_test.go —
//
// Pins the October 2026 GPU cutover on the section path:
//   - section argv carries no sleep pacing, the wider fragment budget, and
//     the input-side GPU decode flag;
//   - sectioned transport retries fail fast (one fewer attempt);
//   - section ranges parse to absolute seconds;
//   - N sections of one video cost ONE network fetch + N local cuts.
package downloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFFMPEGScript is an ffmpeg stand-in for local section cuts: it copies
// the file after -i to the last argument (the destination), so the mirror
// wire path can be pinned without a GPU.
const fakeFFMPEGScript = `#!/usr/bin/env bash
set -euo pipefail
args=("$@")
dest="${args[${#args[@]}-1]}"
src=""
prev=""
for arg in "$@"; do
  if [[ "$prev" == "-i" ]]; then src="$arg"; fi
  prev="$arg"
done
cp "$src" "$dest"
`

func writeFakeFFMPEG(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte(fakeFFMPEGScript), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}

func TestParseSectionRange(t *testing.T) {
	cases := []struct {
		in         string
		start, end float64
		ok         bool
	}{
		{"*00:18:00-00:28:00", 1080, 1680, true},
		{"00:00:00-00:10:00", 0, 600, true},
		{"*01:04:00-01:14:00", 3840, 4440, true},
		{"*00:01:00-00:01:05", 60, 65, true},
		{"*1:30-2:00", 90, 120, true},
		{"*90-150", 90, 150, true},
		{"*00:00:00.500-00:00:01.500", 0.5, 1.5, true},
		{"", 0, 0, false},
		{"*00:10:00-00:05:00", 0, 0, false}, // inverted
		{"*00:05:00-00:05:00", 0, 0, false}, // empty
		{"garbage", 0, 0, false},
		{"*00:00:00", 0, 0, false}, // no range
		{"*-5-10", 0, 0, false},    // negative
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			r, ok := parseSectionRange(tc.in)
			if ok != tc.ok {
				t.Fatalf("parseSectionRange(%q) ok=%v, want %v", tc.in, ok, tc.ok)
			}
			if ok && (r.start != tc.start || r.end != tc.end) {
				t.Fatalf("parseSectionRange(%q) = [%v,%v), want [%v,%v)", tc.in, r.start, r.end, tc.start, tc.end)
			}
		})
	}
}

func TestSectionDestPathsMirrorNetworkNaming(t *testing.T) {
	single := &DownloadRequest{OutputPath: "/tmp/clip.mp4", DownloadSections: []string{"*00:01:00-00:01:05"}}
	if got := sectionDestPaths(single); len(got) != 1 || got[0] != "/tmp/clip.mp4" {
		t.Fatalf("single dest = %v, want [/tmp/clip.mp4]", got)
	}
	multi := &DownloadRequest{OutputPath: "/tmp/clip.mp4", DownloadSections: []string{"*00:01:00-00:01:05", "*00:02:00-00:02:05"}}
	got := sectionDestPaths(multi)
	if len(got) != 2 || got[0] != "/tmp/clip_001.mp4" || got[1] != "/tmp/clip_002.mp4" {
		t.Fatalf("multi dest = %v, want _001/_002 suffixed", got)
	}
}

// TestDownloadSections_SectionArgs_NoSleep_WideFragments_GPUDecode pins the
// section argv contract: no sleep pacing, --concurrent-fragments 8, and the
// input-side GPU decode flag.
func TestDownloadSections_SectionArgs_NoSleep_WideFragments_GPUDecode(t *testing.T) {
	setupTestAllowlist(t)
	t.Setenv("PIPELINEGEN_YTDLP_SECTION_CONCURRENT_FRAGMENTS", "")
	t.Setenv("PIPELINEGEN_YTDLP_HWACCEL", "cuda")
	t.Setenv("PIPELINEGEN_YTDLP_CONCURRENT_FRAGMENTS", "")
	runner := &scriptedRunner{script: []scriptedCall{{
		err: fmt.Errorf("section probe failure"),
	}}}
	d := newTestDownloaderCfg(t, &ytcfg.Config{}, runner)

	_, err := d.DownloadSections(context.Background(), &DownloadRequest{
		URL:              youTubeWatchURL,
		OutputPath:       filepath.Join(t.TempDir(), "clip.mp4"),
		DownloadSections: []string{"*00:01:00-00:01:05"},
	})
	require.Error(t, err)
	require.Equal(t, 1, runner.calls)
	argv := strings.Join(runner.argv[0], " ")
	assert.NotContains(t, argv, "--min-sleep-interval",
		"section fetches must not carry sleep pacing")
	assert.NotContains(t, argv, "--max-sleep-interval",
		"section fetches must not carry sleep pacing")
	assert.Contains(t, argv, "--concurrent-fragments 8",
		"section fetches use the wider fragment budget, got: %s", argv)
	assert.Contains(t, argv, "ffmpeg_i:-hwaccel cuda",
		"section fetches carry the input-side GPU decode flag, got: %s", argv)
}

// TestDownloadRange_SectionedTransportFailsFast pins the retry budget: a
// sectioned acquisition gets one fewer transport attempt than a full-source
// one (3 → 2 by default) so a doomed 600s window fails fast and the
// job-level retry owns the outer budget.
func TestDownloadRange_SectionedTransportFailsFast(t *testing.T) {
	setupTestAllowlist(t)
	t.Setenv(EnvTransportRetries, "")
	runner := &scriptedRunner{script: []scriptedCall{
		{err: fmt.Errorf("connection reset by peer")},
		{err: fmt.Errorf("connection reset by peer")},
		{err: fmt.Errorf("connection reset by peer")},
	}}
	d := newTestDownloaderCfg(t, &ytcfg.Config{}, runner)

	_, err := d.DownloadRange(context.Background(), &DownloadRequest{
		URL:              youTubeWatchURL,
		OutputPath:       filepath.Join(t.TempDir(), "clip.mp4"),
		DownloadSections: []string{"*00:02:00-00:02:05"},
	})
	require.Error(t, err)
	require.Equal(t, transportRetryAttempts-1, runner.calls,
		"sectioned transport must fail fast with one fewer attempt")
}

func TestTransportAttemptsEnvOverrideAndClamp(t *testing.T) {
	t.Setenv(EnvTransportRetries, "1")
	if got := transportAttempts(false); got != 1 {
		t.Fatalf("env 1 full = %d, want 1", got)
	}
	if got := transportAttempts(true); got != 1 {
		t.Fatalf("env 1 sectioned = %d, want 1 (never below 1)", got)
	}
	t.Setenv(EnvTransportRetries, "99")
	if got := transportAttempts(false); got != 5 {
		t.Fatalf("env 99 = %d, want clamp to 5", got)
	}
	t.Setenv(EnvTransportRetries, "garbage")
	if got := transportAttempts(false); got != transportRetryAttempts {
		t.Fatalf("env garbage = %d, want default %d", got, transportRetryAttempts)
	}
}

// TestDownloadSections_ViaMirror_OneFetchNCuts pins plan A end to end: two
// windows of one video cost EXACTLY one yt-dlp network fetch; both windows
// land as verified local cuts.
func TestDownloadSections_ViaMirror_OneFetchNCuts(t *testing.T) {
	t.Setenv(EnvYTDLPMirrorRoot, filepath.Join(t.TempDir(), "mirror"))
	t.Setenv(EnvYTDLPMirrorSections, "")
	workDir := t.TempDir()
	d := newMirrorTestDownloader(t, workDir)
	t.Setenv("FFMPEG_PATH", writeFakeFFMPEG(t, workDir))
	countFile := filepath.Join(workDir, "count")
	t.Setenv("FAKE_YTDLP_COUNT", countFile)

	output := filepath.Join(workDir, "clip.mp4")
	segs, err := d.DownloadSections(context.Background(), &DownloadRequest{
		URL:              "https://www.youtube.com/watch?v=mirrorSection01",
		OutputPath:       output,
		DownloadSections: []string{"*00:01:00-00:01:05", "*00:02:00-00:02:05"},
	})
	require.NoError(t, err)
	require.Len(t, segs, 2)
	if got := downloadCount(t, countFile); got != 1 {
		t.Fatalf("network fetches = %d, want 1 (one full-source fetch, two local cuts)", got)
	}
	for i, s := range segs {
		fi, err := os.Stat(s.Path)
		require.NoError(t, err, "cut %d missing", i)
		require.Positive(t, fi.Size(), "cut %d empty", i)
	}

	// A repeat call costs zero additional fetches: the full source is cached.
	segs2, err := d.DownloadSections(context.Background(), &DownloadRequest{
		URL:              "https://www.youtube.com/watch?v=mirrorSection01",
		OutputPath:       filepath.Join(workDir, "clip2.mp4"),
		DownloadSections: []string{"*00:03:00-00:03:05"},
	})
	require.NoError(t, err)
	require.Len(t, segs2, 1)
	if got := downloadCount(t, countFile); got != 1 {
		t.Fatalf("network fetches after repeat = %d, want still 1", got)
	}
}

// TestDownloadSections_ViaMirror_UnparseableFallsBackToNetwork pins the
// fail-soft contract: a section string the local cutter cannot parse keeps
// the legacy per-section network path instead of failing.
func TestDownloadSections_ViaMirror_UnparseableFallsBackToNetwork(t *testing.T) {
	setupTestAllowlist(t)
	t.Setenv(EnvYTDLPMirrorRoot, filepath.Join(t.TempDir(), "mirror"))
	runner := &scriptedRunner{script: []scriptedCall{{
		err: fmt.Errorf("section probe failure"),
	}}}
	d := newTestDownloaderCfg(t, &ytcfg.Config{}, runner)

	_, err := d.DownloadSections(context.Background(), &DownloadRequest{
		URL:              youTubeWatchURL,
		OutputPath:       filepath.Join(t.TempDir(), "clip.mp4"),
		DownloadSections: []string{"*00:10:00-00:05:00"},
	})
	require.Error(t, err)
	require.Equal(t, 1, runner.calls, "unparseable section must use the network path")
	require.Contains(t, runner.argv[0], "*00:10:00-00:05:00")
}
