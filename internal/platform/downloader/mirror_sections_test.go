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

// TestMirrorSectionExt pins the container resolution for mirror-served cuts:
// the merge policy wins, empty defaults to mp4 (the network path default).
func TestMirrorSectionExt(t *testing.T) {
	cases := map[string]string{
		"":      "mp4",
		"mp4":   "mp4",
		"mkv":   "mkv",
		".mkv":  "mkv",
		" mp4 ": "mp4",
	}
	for in, want := range cases {
		if got := mirrorSectionExt(in); got != want {
			t.Fatalf("mirrorSectionExt(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSectionDestPathsRespectsMergeFormat pins that a mirror hit lands on
// the same filename the network path would produce for a non-default merge.
func TestSectionDestPathsRespectsMergeFormat(t *testing.T) {
	req := &DownloadRequest{
		OutputPath:       "/tmp/clip.mp4",
		MergeFormat:      "mkv",
		DownloadSections: []string{"*00:01:00-00:01:05", "*00:02:00-00:02:05"},
	}
	got := sectionDestPaths(req)
	if len(got) != 2 || got[0] != "/tmp/clip_001.mkv" || got[1] != "/tmp/clip_002.mkv" {
		t.Fatalf("merge-aware dests = %v, want _001/_002 .mkv", got)
	}
}

// fakeYTDLPArgvScript is fakeYTDLPScript with argv capture: every invocation
// appends its full argv to $FAKE_YTDLP_ARGV, so tests can pin which flags the
// full-source mirror fetch carried (with the section-via-mirror path only one
// yt-dlp invocation runs, so the file holds exactly the fetch argv).
const fakeYTDLPArgvScript = `#!/usr/bin/env bash
set -euo pipefail
count_file="${FAKE_YTDLP_COUNT}"
out=""
prev=""
for arg in "$@"; do
  if [[ "$prev" == "-o" ]]; then out="$arg"; fi
  prev="$arg"
done
count=$(cat "$count_file" 2>/dev/null || echo 0)
echo $((count + 1)) > "$count_file"
printf '%s\n' "$@" > "${FAKE_YTDLP_ARGV}"
resolved="${out/\%\(ext\)s/mp4}"
printf 'fake-bytes-%s' "$count" > "$resolved"
`

func writeFakeYTDLPWithArgv(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(path, []byte(fakeYTDLPArgvScript), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	return path
}

// TestDownloadSections_ViaMirror_FullSourceFetchHonorsUseCookies pins the
// stock/staging contract: stock and the YouTube section stager set UseCookies
// (and ForceKeyframes) on every section request, so the single full-source
// mirror fetch must carry --cookies too — and must itself carry NEITHER
// --download-sections NOR --force-keyframes-at-cuts, i.e. zero yt-dlp-side
// re-encodes on the exact production staging shape. Otherwise a cookie-gated
// video fails the mirror fetch and every window of that video pays the slow
// per-section network path forever without ever warming the cache (fail-soft
// hides it as merely slow).
func TestDownloadSections_ViaMirror_FullSourceFetchHonorsUseCookies(t *testing.T) {
	setupTestAllowlist(t)
	mirrorRoot := filepath.Join(t.TempDir(), "mirror")
	t.Setenv(EnvYTDLPMirrorRoot, mirrorRoot)
	t.Setenv(EnvYTDLPMirrorSections, "")
	t.Setenv("VELOX_YOUTUBE_COOKIES_FILE", "")
	workDir := t.TempDir()
	cookieFile := filepath.Join(workDir, "cookies.txt")
	require.NoError(t, os.WriteFile(cookieFile, []byte("# test cookies"), 0o600))
	argvFile := filepath.Join(workDir, "argv")
	countFile := filepath.Join(workDir, "count")
	t.Setenv("FAKE_YTDLP_COUNT", countFile)
	t.Setenv("FAKE_YTDLP_ARGV", argvFile)
	t.Setenv("FFMPEG_PATH", writeFakeFFMPEG(t, workDir))

	d := NewYTDLP(&ytcfg.Config{External: ytcfg.ExternalConfig{YouTubeCookiesPath: cookieFile}})
	d.path = writeFakeYTDLPWithArgv(t, workDir)
	d.runner = defaultRunner{} // real subprocess: argv capture needs a real exec

	segs, err := d.DownloadSections(context.Background(), &DownloadRequest{
		URL:              "https://www.youtube.com/watch?v=cookieSection01",
		OutputPath:       filepath.Join(workDir, "clip.mp4"),
		DownloadSections: []string{"*00:01:00-00:01:05", "*00:02:00-00:02:05"},
		ForceKeyframes:   true,
		UseCookies:       true,
	})
	require.NoError(t, err)
	require.Len(t, segs, 2)
	require.Equal(t, 1, downloadCount(t, countFile), "want exactly one full-source fetch")
	argvRaw, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	argv := string(argvRaw)
	assert.Contains(t, argv, "--cookies", "mirror full-source fetch must carry cookies, got:\n%s", argv)
	assert.Contains(t, argv, cookieFile, "mirror fetch must use the configured cookie file, got:\n%s", argv)
	assert.NotContains(t, argv, "--download-sections", "mirror fetch must not be a section fetch, got:\n%s", argv)
	assert.NotContains(t, argv, "--force-keyframes-at-cuts", "staging-shaped request must not re-encode in yt-dlp, got:\n%s", argv)
	leftovers, err := filepath.Glob(filepath.Join(mirrorRoot, ".fetch-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers, "mirror must not leave .fetch twins, got %v", leftovers)
}

// TestNVENCPreset pins the operator knob: p1..p7 pass through (case-
// insensitive), anything else falls back to p1 so a typo never fails a cut.
func TestNVENCPreset(t *testing.T) {
	cases := map[string]string{
		"":       "p1",
		"p1":     "p1",
		"p3":     "p3",
		"P5":     "p5",
		"p7":     "p7",
		"p0":     "p1",
		"p8":     "p1",
		"fast":   "p1",
		"off":    "p1",
		" p4 \t": "p4",
	}
	for in, want := range cases {
		t.Setenv(EnvNVENCPreset, in)
		if got := nvencPreset(); got != want {
			t.Fatalf("nvencPreset(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeFFMPEGArgvScript is an ffmpeg stand-in that records its argv and
// materialises the destination by copying the -i source, so tests can pin
// the encoder flags cutSectionLocal actually execs.
const fakeFFMPEGArgvScript = `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" > "${FFMPEG_ARGV}"
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

// TestCutSectionLocal_UsesConfiguredNVENCPreset pins the wiring: the NVENC
// leg execs -preset with the operator knob value on the very first ffmpeg
// invocation (no fallback run when NVENC succeeds).
func TestCutSectionLocal_UsesConfiguredNVENCPreset(t *testing.T) {
	workDir := t.TempDir()
	script := filepath.Join(workDir, "ffmpeg")
	if err := os.WriteFile(script, []byte(fakeFFMPEGArgvScript), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	t.Setenv("FFMPEG_PATH", script)
	argvFile := filepath.Join(workDir, "argv")
	t.Setenv("FFMPEG_ARGV", argvFile)
	t.Setenv(EnvNVENCPreset, "p3")

	src := filepath.Join(workDir, "src.mp4")
	require.NoError(t, os.WriteFile(src, []byte("fake-source-bytes"), 0o644))
	d := newTestDownloaderCfg(t, &ytcfg.Config{}, defaultRunner{})
	dest := filepath.Join(workDir, "cut.mp4")
	require.NoError(t, d.cutSectionLocal(context.Background(), src, dest, sectionRange{start: 60, end: 65}))
	argvRaw, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	argv := string(argvRaw)
	assert.Contains(t, argv, "h264_nvenc", "cut must try the NVENC leg first, got:\n%s", argv)
	assert.Contains(t, argv, "-preset\np3\n", "cut must exec the configured NVENC preset, got:\n%s", argv)
}

// TestDownloadSections_ViaMirror_NoSectionReencode pins the October 2026
// GPU-cutover core promise end to end: serving windows from the mirror runs
// exactly one full-source fetch whose argv carries NEITHER
// --download-sections NOR --force-keyframes-at-cuts — i.e. zero yt-dlp-side
// re-encodes — and leaves no .fetch twin behind.
func TestDownloadSections_ViaMirror_NoSectionReencode(t *testing.T) {
	setupTestAllowlist(t)
	mirrorRoot := filepath.Join(t.TempDir(), "mirror")
	t.Setenv(EnvYTDLPMirrorRoot, mirrorRoot)
	t.Setenv(EnvYTDLPMirrorSections, "")
	workDir := t.TempDir()
	t.Setenv("FAKE_YTDLP_COUNT", filepath.Join(workDir, "count"))
	argvFile := filepath.Join(workDir, "argv")
	t.Setenv("FAKE_YTDLP_ARGV", argvFile)
	t.Setenv("FFMPEG_PATH", writeFakeFFMPEG(t, workDir))

	d := NewYTDLP(&ytcfg.Config{})
	d.path = writeFakeYTDLPWithArgv(t, workDir)
	d.runner = defaultRunner{}

	segs, err := d.DownloadSections(context.Background(), &DownloadRequest{
		URL:              "https://www.youtube.com/watch?v=noreencode01",
		OutputPath:       filepath.Join(workDir, "clip.mp4"),
		DownloadSections: []string{"*00:01:00-00:01:05", "*00:02:00-00:02:05"},
		ForceKeyframes:   true,
	})
	require.NoError(t, err)
	require.Len(t, segs, 2)
	argvRaw, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	argv := string(argvRaw)
	assert.NotContains(t, argv, "--download-sections", "mirror fetch must not be a section fetch, got:\n%s", argv)
	assert.NotContains(t, argv, "--force-keyframes-at-cuts", "mirror fetch must not re-encode, got:\n%s", argv)
	leftovers, err := filepath.Glob(filepath.Join(mirrorRoot, ".fetch-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers, "mirror must not leave .fetch twins, got %v", leftovers)
}

// TestCleanupFetchArtifacts pins the hygiene contract: the key's fetch twin
// and part debris go away, the verified entry and other videos' files stay.
func TestCleanupFetchArtifacts(t *testing.T) {
	root := t.TempDir()
	key := "0123456789abcdef0123456789abcdef"
	keep := []string{key + ".mp4", key + ".json", ".fetch-otherkey0123456789abcdef012345.mp4"}
	remove := []string{".fetch-" + key + ".mp4", ".fetch-" + key + ".mp4.part", ".fetch-" + key + ".mp4.ytdl"}
	for _, n := range append(append([]string{}, keep...), remove...) {
		require.NoError(t, os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644))
	}
	cleanupFetchArtifacts(root, key)
	for _, n := range remove {
		if _, err := os.Stat(filepath.Join(root, n)); !os.IsNotExist(err) {
			t.Fatalf("%s still present after cleanup", n)
		}
	}
	for _, n := range keep {
		if _, err := os.Stat(filepath.Join(root, n)); err != nil {
			t.Fatalf("%s wrongly removed by cleanup: %v", n, err)
		}
	}
	// Nil/empty inputs are no-ops, never a panic or a mass delete.
	cleanupFetchArtifacts("", key)
	cleanupFetchArtifacts(root, "")
}

// TestNewYTDLPResolvesVideoPolicy pins that the operator-tuned Video block
// reaches the local-cut CPU fallback: custom codec/preset/CRF propagate,
// and a zero config keeps the canonical defaults.
func TestNewYTDLPResolvesVideoPolicy(t *testing.T) {
	custom := newTestDownloaderCfg(t, &ytcfg.Config{
		Video: ytcfg.VideoConfig{Codec: "libx264", Preset: "fast", CRF: 18},
	}, &scriptedRunner{})
	if custom.videoPolicy.Codec != "libx264" || custom.videoPolicy.Preset != "fast" || custom.videoPolicy.CRF != 18 {
		t.Fatalf("custom video policy = %+v, want libx264/fast/18", custom.videoPolicy)
	}
	def := newTestDownloaderCfg(t, &ytcfg.Config{}, &scriptedRunner{})
	if def.videoPolicy.Codec != "libx264" || def.videoPolicy.Preset != "veryfast" || def.videoPolicy.CRF != 23 {
		t.Fatalf("default video policy = %+v, want libx264/veryfast/23", def.videoPolicy)
	}
}
