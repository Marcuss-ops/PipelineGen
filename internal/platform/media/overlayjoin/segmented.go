package overlayjoin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// segmentedInputThreshold is the number of simultaneous overlay inputs above
// which the join stops building one graph with every clip. ffmpeg opens all
// inputs at graph configuration time and an overlay whose secondary input has
// its PTS shifted into the future queues that input's frames until the main
// timeline reaches the window. With 74 certified clips that queue is what took
// ffmpeg to 15.6 GiB in production (11.7 GiB on the 74-input reproduction) and
// got the assembly killed by the kernel.
//
// It is a variable so the frame-identity test can force the segmented path on a
// small fixture.
var segmentedInputThreshold = 16

// segmentDuration is the timeline length of one lossless intermediate. It is a
// whole number of frames at any integer frame rate (6 s = 6*fps frames), so
// segment boundaries stay on the frame grid and the reassembled timeline is
// frame-identical to the single-graph one.
var segmentDuration = 15 * time.Second

// segmented plan: normalize the background once (scale/crop/fps/setpts applied
// exactly as the single graph does), render the timeline in lossless ffv1
// segments with only the clips that overlap each segment, then concatenate the
// segments and run the canonical encoder once. Frames are produced by the same
// filters in the same order, so the final encode sees the same pixels.
func runSegmented(ctx context.Context, spec Spec, ffmpeg string) (Result, error) {
	workDir, err := os.MkdirTemp(filepath.Dir(spec.OutputPath), ".overlayjoin-parts-")
	if err != nil {
		return Result{}, fmt.Errorf("overlayjoin: create parts dir: %w", err)
	}
	if os.Getenv("OVERLAYJOIN_KEEP_PARTS") == "" {
		defer os.RemoveAll(workDir)
	} else {
		fmt.Fprintf(os.Stderr, "overlayjoin: parts kept in %s\n", workDir)
	}

	fps := spec.fps()
	segmentUS := int64(segmentDuration / time.Microsecond)

	backgroundPath := filepath.Join(workDir, "background.mov")
	if err := runFFmpeg(ctx, ffmpeg, 15*time.Minute, backgroundArgs(spec, backgroundPath)); err != nil {
		return Result{}, fmt.Errorf("overlayjoin: normalized background: %w", err)
	}

	type part struct {
		path  string
		durUS int64
	}
	parts := make([]part, 0, spec.DurationUS/segmentUS+1)
	for start := int64(0); start < spec.DurationUS; start += segmentUS {
		end := start + segmentUS
		if end > spec.DurationUS {
			end = spec.DurationUS
		}
		path := filepath.Join(workDir, fmt.Sprintf("part-%03d.mov", len(parts)))
		if err := runFFmpeg(ctx, ffmpeg, 20*time.Minute,
			segmentArgs(spec, backgroundPath, start, end, path)); err != nil {
			return Result{}, fmt.Errorf("overlayjoin: segment %d: %w", len(parts), err)
		}
		parts = append(parts, part{path: path, durUS: end - start})
	}

	// The concat demuxer offsets the next file by the previous file's duration,
	// and a lossless MOV reports its last PTS (5.958333 for 144 frames), not the
	// six-second span. Without the explicit duration the first frame of every
	// segment lands on the previous last frame's timestamp and one frame per
	// boundary is dropped as a non-monotonic DTS.
	listPath := filepath.Join(workDir, "parts.txt")
	var list strings.Builder
	for _, p := range parts {
		fmt.Fprintf(&list, "file '%s'\nduration %s\n", strings.ReplaceAll(p.path, "'", "'\\''"), seconds(p.durUS))
	}
	if err := os.WriteFile(listPath, []byte(list.String()), 0o600); err != nil {
		return Result{}, fmt.Errorf("overlayjoin: write parts list: %w", err)
	}
	if err := runFFmpeg(ctx, ffmpeg, 30*time.Minute, concatArgs(spec, listPath, fps)); err != nil {
		return Result{}, fmt.Errorf("overlayjoin: final encode: %w", err)
	}
	info, err := os.Stat(spec.OutputPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return Result{}, fmt.Errorf("overlayjoin: ffmpeg produced no output at %s", spec.OutputPath)
	}
	return Result{Path: spec.OutputPath, SizeBytes: info.Size(), DurationUS: spec.DurationUS}, nil
}

// backgroundArgs renders the timeline canvas once, at the exact filter chain
// the single graph uses, into a lossless, all-intra intermediate. Every segment
// then reads it with an exact frame seek, so the background frame at segment
// boundaries is the same pixel the continuous graph would have produced.
func backgroundArgs(spec Spec, out string) []string {
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-filter_complex_threads", "1", "-threads", "1"}
	if spec.BackgroundPath != "" {
		args = append(args, "-stream_loop", "-1", "-i", spec.BackgroundPath)
	} else {
		args = append(args, "-f", "lavfi", "-i",
			fmt.Sprintf("color=c=black:s=%dx%d:r=%d", spec.Width, spec.Height, spec.fps()))
	}
	graph := fmt.Sprintf("[0:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1,fps=%d,setpts=PTS-STARTPTS[v]",
		spec.Width, spec.Height, spec.Width, spec.Height, spec.fps())
	args = append(args, "-filter_complex", graph, "-map", "[v]", "-an")
	args = append(args, "-t", seconds(spec.DurationUS))
	return append(args, losslessArgs(out)...)
}

// segmentArgs renders [start,end) with only the clips that intersect it. A clip
// crossing the boundary is trimmed to the part inside the segment on the frame
// grid, which is what `overlay` shows for that part of the window anyway.
func segmentArgs(spec Spec, background string, start, end int64, out string) []string {
	active := make([]int, 0, len(spec.Clips))
	for i, clip := range spec.Clips {
		clipEnd := clip.StartUS + clip.DurationUS
		if clip.StartUS < end && clipEnd > start {
			active = append(active, i)
		}
	}

	args := []string{"-y", "-hide_banner", "-loglevel", "error",
		"-filter_complex_threads", strconv.Itoa(filterThreads(len(active))),
		"-ss", seconds(start), "-threads", "1", "-i", background}
	for _, i := range active {
		args = append(args, "-threads", "1", "-i", spec.Clips[i].Path)
	}

	filters := []string{"[0:v]setpts=PTS-STARTPTS[base]"}
	video := "[base]"
	for j, i := range active {
		clip := spec.Clips[i]
		clipEnd := clip.StartUS + clip.DurationUS
		from := clip.StartUS
		if start > from {
			from = start
		}
		to := clipEnd
		if end < to {
			to = end
		}
		// trim is a hard frame gate: start it on the frame boundary at or below
		// the window offset, so the first frame is the one overlay's floor-based
		// sync shows in the continuous graph.
		trimStart := float64(from-clip.StartUS) / 1e6
		trimStart = floorFrame(trimStart, spec.fps())
		duration := float64(to-from) / 1e6
		shift := float64(from-start) / 1e6
		filters = append(filters, fmt.Sprintf(
			"[%d:v]trim=start=%.6f:duration=%.6f,setpts=PTS-STARTPTS+%.6f/TB[ov%d]",
			j+1, trimStart, duration, shift, j))
		next := fmt.Sprintf("[s%d]", j)
		filters = append(filters, fmt.Sprintf(
			"%s[ov%d]overlay=0:0:format=auto:eof_action=pass:shortest=0:enable='between(t,%.6f,%.6f)'%s",
			video, j, shift, shift+duration, next))
		video = next
	}

	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", video, "-an", "-t", seconds(end-start))
	return append(args, losslessArgs(out)...)
}

// concatArgs joins the lossless segments and runs the certified encoder once,
// with the master audio muxed exactly as the single-graph invocation does.
func concatArgs(spec Spec, listPath string, fps int) []string {
	args := []string{"-y", "-hide_banner", "-loglevel", "error",
		"-f", "concat", "-safe", "0", "-i", listPath,
		"-i", spec.AudioPath,
		"-map", "0:v:0", "-map", "1:a:0", "-t", seconds(spec.DurationUS)}
	args = append(args, kernelmedia.CanonicalVideoEncoderArgs()...)
	return append(args,
		"-r", fmt.Sprint(fps),
		"-c:a", "aac", "-b:a", "192k",
		"-movflags", "+faststart",
		spec.OutputPath,
	)
}

// losslessArgs is the intermediary recipe: all-intra ffv1 in a MOV with the
// canonical 90000 timescale, so 24 fps timestamps are exact (3750 ticks/frame)
// and no re-timing filter can duplicate or drop a frame between passes.
func losslessArgs(out string) []string {
	return []string{
		"-c:v", "ffv1", "-level", "3", "-g", "1", "-pix_fmt", "yuv420p",
		"-video_track_timescale", "90000", "-fps_mode", "passthrough",
		out,
	}
}

func runFFmpeg(ctx context.Context, ffmpeg string, timeout time.Duration, args []string) error {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, ffmpeg, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %w: %s", err, truncate(string(output), 800))
	}
	return nil
}

func seconds(us int64) string {
	return fmt.Sprintf("%.6f", float64(us)/1e6)
}

func floorFrame(seconds float64, fps int) float64 {
	if fps <= 0 || seconds <= 0 {
		return 0
	}
	frames := float64(int(seconds*float64(fps) + 1e-9))
	return frames / float64(fps)
}
