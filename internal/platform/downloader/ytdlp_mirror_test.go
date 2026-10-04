// internal/platform/downloader/ytdlp_mirror_test.go —
//
// Wire tests for the F1 per-video-ID mirror (TODO-pipeline-100x-velocita):
// the same YouTube source must never be re-downloaded. A fake yt-dlp binary
// counts invocations; the mirror contract is pinned end to end through the
// real Download() path.
package downloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// fakeYTDLPScript is a yt-dlp stand-in: it writes a non-empty file next to
// the requested output template (the ext resolution contract) and increments
// a call counter file, so tests can pin "how many network downloads ran".
const fakeYTDLPScript = `#!/usr/bin/env bash
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
resolved="${out/\%\(ext\)s/mp4}"
printf 'fake-bytes-%s' "$count" > "$resolved"
`

func writeFakeYTDLP(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(path, []byte(fakeYTDLPScript), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	return path
}

func newMirrorTestDownloader(t *testing.T, workDir string) *YTDLPDownloader {
	t.Helper()
	setupTestAllowlist(t)
	cfg := &ytcfg.Config{}
	d := NewYTDLP(cfg)
	d.path = writeFakeYTDLP(t, workDir)
	d.runner = defaultRunner{} // real subprocess: the mirror wire path must produce files
	return d
}

func downloadCount(t *testing.T, countFile string) int {
	t.Helper()
	raw, err := os.ReadFile(countFile)
	if err != nil {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &n); err != nil {
		t.Fatalf("parse count file: %v", err)
	}
	return n
}

// TestMirror_SecondDownloadOfSameVideoServedFromCache pins the F1 core
// contract: two Download() calls for the same YouTube video + format run the
// network download EXACTLY once; the second call is served from the mirror
// and lands at the same resolved output path, verified.
func TestMirror_SecondDownloadOfSameVideoServedFromCache(t *testing.T) {
	t.Setenv(EnvYTDLPMirrorRoot, filepath.Join(t.TempDir(), "mirror"))
	workDir := t.TempDir()
	d := newMirrorTestDownloader(t, workDir)
	countFile := filepath.Join(workDir, "count")
	t.Setenv("FAKE_YTDLP_COUNT", countFile)

	output := filepath.Join(workDir, "source.%(ext)s")
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := d.Download(ctx, &DownloadRequest{
			URL:        "https://www.youtube.com/watch?v=abcVideoID01",
			OutputPath: output,
			NoPlaylist: true,
		}); err != nil {
			t.Fatalf("download %d: %v", i+1, err)
		}
	}

	if got := downloadCount(t, countFile); got != 1 {
		t.Fatalf("network downloads = %d, want 1 (second call must be mirrored)", got)
	}
	if _, err := os.Stat(strings.Replace(output, "%(ext)s", "mp4", 1)); err != nil {
		t.Fatalf("resolved output missing after mirrored call: %v", err)
	}
}

// TestMirror_FormatSignatureIsolatesEntries pins the correctness contract: a
// request with a different caller format must NOT be served the other
// format's cached bytes — it reruns the download.
func TestMirror_FormatSignatureIsolatesEntries(t *testing.T) {
	t.Setenv(EnvYTDLPMirrorRoot, filepath.Join(t.TempDir(), "mirror"))
	workDir := t.TempDir()
	d := newMirrorTestDownloader(t, workDir)
	countFile := filepath.Join(workDir, "count")
	t.Setenv("FAKE_YTDLP_COUNT", countFile)

	ctx := context.Background()
	url := "https://www.youtube.com/watch?v=abcVideoID01"
	if err := d.Download(ctx, &DownloadRequest{URL: url, OutputPath: filepath.Join(workDir, "a.%(ext)s"), NoPlaylist: true}); err != nil {
		t.Fatalf("download A: %v", err)
	}
	if err := d.Download(ctx, &DownloadRequest{URL: url, OutputPath: filepath.Join(workDir, "b.%(ext)s"), NoPlaylist: true, Format: "bestvideo[height<=720]"}); err != nil {
		t.Fatalf("download B: %v", err)
	}
	if got := downloadCount(t, countFile); got != 2 {
		t.Fatalf("network downloads = %d, want 2 (different format signatures must not share an entry)", got)
	}
}

// TestMirror_NonYouTubeURLNeverMirrored pins the URL scoping: Artlist and
// generic sources keep the exact download path on every call.
func TestMirror_NonYouTubeURLNeverMirrored(t *testing.T) {
	t.Setenv(EnvYTDLPMirrorRoot, filepath.Join(t.TempDir(), "mirror"))
	workDir := t.TempDir()
	d := newMirrorTestDownloader(t, workDir)
	countFile := filepath.Join(workDir, "count")
	t.Setenv("FAKE_YTDLP_COUNT", countFile)

	ctx := context.Background()
	if err := youtubeVideoIDContractChecks(); err != nil {
		t.Fatalf("video id contract: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := d.Download(ctx, &DownloadRequest{
			URL:        "https://www.artlist.io/video/example-source",
			OutputPath: filepath.Join(workDir, "art.%(ext)s"),
			NoPlaylist: true,
		}); err != nil {
			t.Fatalf("artlist download %d: %v", i+1, err)
		}
	}
	if got := downloadCount(t, countFile); got != 2 {
		t.Fatalf("network downloads = %d, want 2 (non-YouTube URLs are never mirrored)", got)
	}
}

// TestMirror_CorruptedEntryEvictedAndRedownloaded pins the fail-closed cache
// contract: a corrupted mirror entry is evicted and the download reruns, so
// a bad artifact can never surface as a successful download.
func TestMirror_CorruptedEntryEvictedAndRedownloaded(t *testing.T) {
	t.Setenv(EnvYTDLPMirrorRoot, filepath.Join(t.TempDir(), "mirror"))
	workDir := t.TempDir()
	d := newMirrorTestDownloader(t, workDir)
	countFile := filepath.Join(workDir, "count")
	t.Setenv("FAKE_YTDLP_COUNT", countFile)

	ctx := context.Background()
	url := "https://youtu.be/abcVideoID01"
	output := filepath.Join(workDir, "src.%(ext)s")
	if err := d.Download(ctx, &DownloadRequest{URL: url, OutputPath: output, NoPlaylist: true}); err != nil {
		t.Fatalf("first download: %v", err)
	}
	// Corrupt the mirror entry (truncate to zero bytes).
	root := os.Getenv(EnvYTDLPMirrorRoot)
	entries, _ := filepath.Glob(filepath.Join(root, "*.mp4"))
	if len(entries) != 1 {
		t.Fatalf("mirror entries = %d, want 1", len(entries))
	}
	if err := os.WriteFile(entries[0], []byte{}, 0o644); err != nil {
		t.Fatalf("corrupt entry: %v", err)
	}
	if err := d.Download(ctx, &DownloadRequest{URL: url, OutputPath: output, NoPlaylist: true}); err != nil {
		t.Fatalf("post-corruption download: %v", err)
	}
	if got := downloadCount(t, countFile); got != 2 {
		t.Fatalf("network downloads = %d, want 2 (corrupted entry must be redownloaded)", got)
	}
}

// TestMirror_ConcurrentSameKeySingleFlight pins the single-flight contract:
// N concurrent downloads of the same source run the network download once.
func TestMirror_ConcurrentSameKeySingleFlight(t *testing.T) {
	t.Setenv(EnvYTDLPMirrorRoot, filepath.Join(t.TempDir(), "mirror"))
	workDir := t.TempDir()
	d := newMirrorTestDownloader(t, workDir)
	countFile := filepath.Join(workDir, "count")
	t.Setenv("FAKE_YTDLP_COUNT", countFile)

	var firstErr atomic.Value
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			output := filepath.Join(workDir, fmt.Sprintf("job%d.%%28ext%%29s", i))
			output = filepath.Join(workDir, fmt.Sprintf("job%d.%%(ext)s", i))
			err := d.Download(context.Background(), &DownloadRequest{
				URL:        "https://www.youtube.com/watch?v=sharedVideoID9",
				OutputPath: output,
				NoPlaylist: true,
			})
			if err != nil && firstErr.Load() == nil {
				firstErr.Store(err)
			}
		}(i)
	}
	wg.Wait()
	if err, ok := firstErr.Load().(*error); ok && err != nil {
		t.Fatalf("concurrent download failed: %v", *err)
	}
	if got := downloadCount(t, countFile); got != 1 {
		t.Fatalf("network downloads = %d, want 1 (same-key requests must single-flight)", got)
	}
}

func youtubeVideoIDContractChecks() error {
	cases := map[string]string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=1s": "dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?si=x":                "dQw4w9WgXcQ",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ":       "dQw4w9WgXcQ",
		"https://www.artlist.io/video/x":                   "",
		"https://example.com/?v=":                          "",
	}
	for url, want := range cases {
		if got := youtubeVideoID(url); got != want {
			return fmt.Errorf("youtubeVideoID(%q) = %q, want %q", url, got, want)
		}
	}
	return nil
}
