package finaljob

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// canonicalizeFinalJobRenderedClipTimeBase makes a Creator-local localized
// render packet-copy compatible with the final-job timeline.
//
// Chronon/x264 may write a 1/12288 MP4 track timebase even when the decoded
// frames and avcC are canonical. cliprender accepts that equivalent, but the
// remote video.prepare.window.v1 packet-copy lane does not: mixing it with the
// 1/90000 stock/intro sources fails with WINDOW_PACKET_COPY_INCOMPATIBLE.
// Rewriting only the container timescale (stream copy, no re-encode) keeps the
// pictures and audio intact while giving every final-job source one timebase.
//
// The function is idempotent: a file already at 1/90000 is hashed and returned
// unchanged. It returns the exact SHA-256/size of the bytes at path after the
// operation, plus whether the file was rewritten.
func canonicalizeFinalJobRenderedClipTimeBase(ctx context.Context, path string) (string, int64, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", 0, false, fmt.Errorf("rendered clip path is required")
	}
	before, err := finalJobVideoTimeBase(ctx, path)
	if err != nil {
		return "", 0, false, err
	}
	want := fmt.Sprintf("1/%d", kernelmedia.CanonicalVideoTimescale)
	if before == want {
		sha, size, err := digest.SHA256File(path)
		if err != nil {
			return "", 0, false, fmt.Errorf("hash rendered clip: %w", err)
		}
		return sha, size, false, nil
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", 0, false, fmt.Errorf("ffmpeg is required to normalize a %s rendered clip: %w", before, err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".final-job-timebase-*.mp4")
	if err != nil {
		return "", 0, false, fmt.Errorf("create normalized rendered clip: %w", err)
	}
	tmpPath := tmp.Name()
	if closeErr := tmp.Close(); closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", 0, false, fmt.Errorf("create normalized rendered clip: %w", closeErr)
	}
	// ffmpeg refuses to overwrite an existing output unless -y is passed; the
	// temporary path must not already exist so the atomic rename cannot leave
	// a half-written artifact behind on failure.
	_ = os.Remove(tmpPath)
	defer os.Remove(tmpPath)

	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", path,
		"-map", "0:v:0", "-map", "0:a:0?", "-c", "copy",
		"-video_track_timescale", strconv.Itoa(kernelmedia.CanonicalVideoTimescale),
		"-movflags", "+faststart",
		tmpPath,
	}
	if output, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
		return "", 0, false, fmt.Errorf("normalize rendered clip timebase %s -> %s: %w: %s", before, want, err, strings.TrimSpace(string(output)))
	}
	after, err := finalJobVideoTimeBase(ctx, tmpPath)
	if err != nil {
		return "", 0, false, fmt.Errorf("verify normalized rendered clip: %w", err)
	}
	if after != want {
		return "", 0, false, fmt.Errorf("normalized rendered clip timebase is %s, want %s", after, want)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", 0, false, fmt.Errorf("publish normalized rendered clip: %w", err)
	}
	sha, size, err := digest.SHA256File(path)
	if err != nil {
		return "", 0, false, fmt.Errorf("hash normalized rendered clip: %w", err)
	}
	return sha, size, true, nil
}

// finalJobVideoTimeBase reads the first video stream's container timebase. It
// is deliberately a probe, not a validation: callers own the policy for which
// values they accept.
func finalJobVideoTimeBase(ctx context.Context, path string) (string, error) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return "", fmt.Errorf("ffprobe is required to read a rendered clip timebase: %w", err)
	}
	out, err := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=time_base",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return "", fmt.Errorf("probe rendered clip timebase: %w", err)
	}
	timeBase := strings.TrimSpace(string(out))
	if timeBase == "" {
		return "", fmt.Errorf("rendered clip has no video timebase")
	}
	return timeBase, nil
}
