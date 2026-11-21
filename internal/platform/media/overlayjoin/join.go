// Package overlayjoin assembles the job's own final video from the certified
// per-item overlay renders.
//
// The local render lane deliberately produces one short, full-canvas clip per
// semantic overlay item instead of a whole-timeline canvas. Those clips are the
// authoritative pixels — the renderer owns item placement — so the only
// faithful way to hand a job a single MP4 is to place each certified clip on
// the plan's own window over the plan's background and mux the certified final
// audio. Nothing here invents geometry: a clip is composited at 0:0 exactly
// where the renderer laid it out.
package overlayjoin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// Clip is one certified overlay render placed on the timeline.
type Clip struct {
	// Path is the downloaded, digest-verified MP4 for this item.
	Path string
	// StartUS is the item's start on the canonical timeline (plan window).
	StartUS int64
	// DurationUS is the item's window length. Zero falls back to the clip's
	// own duration.
	DurationUS int64
}

// Spec is one final-video assembly.
type Spec struct {
	// BackgroundPath is the resolved background video/image. Empty renders a
	// black canvas of the requested size.
	BackgroundPath string
	// AudioPath is the certified final audio master. Required.
	AudioPath string
	// OutputPath receives the assembled MP4. Required.
	OutputPath string
	Width      int
	Height     int
	FPS        int
	// Encoder is resolved by the application composition root.
	Encoder mediaexec.EncoderPolicy
	// DurationUS bounds the output. Zero uses the audio duration.
	DurationUS int64
	Clips      []Clip
}

// Result reports the produced artifact.
type Result struct {
	Path       string
	SizeBytes  int64
	DurationUS int64
}

// ErrNoAudio is returned when the spec has no certified final audio: a silent
// final video would be a different deliverable, not a degraded one.
var ErrNoAudio = errors.New("overlayjoin: certified final audio is required")

// ErrNoBackground is returned when neither a background nor an explicit
// black canvas was reachable. Callers pass an empty BackgroundPath only when
// they deliberately want the black canvas.
var ErrNoBackground = errors.New("overlayjoin: background path does not exist")

// Validate checks the spec without touching the filesystem beyond stat.
func (s Spec) Validate() error {
	if strings.TrimSpace(s.AudioPath) == "" {
		return ErrNoAudio
	}
	if strings.TrimSpace(s.OutputPath) == "" {
		return fmt.Errorf("overlayjoin: output path is required")
	}
	if s.Width <= 0 || s.Height <= 0 {
		return fmt.Errorf("overlayjoin: canvas %dx%d is invalid", s.Width, s.Height)
	}
	if strings.TrimSpace(s.Encoder.Codec) == "" {
		return fmt.Errorf("overlayjoin: resolved encoder codec is required")
	}
	// The joined video is the delivery artifact of the local overlay lane. It
	// must carry the same certified H.264 identity as every other lane: a
	// nominal H.264 policy with another preset (or NVENC) emits a different
	// avcC and would make the deliverable a one-off. Fail before encoding.
	if !kernelmedia.IsCanonicalVideoEncoder(s.Encoder.Codec, s.Encoder.Preset, s.Encoder.CRF) {
		return fmt.Errorf("overlayjoin: final video requires the canonical %s/%s/%d encoder, got %q/%q/%d",
			kernelmedia.CanonicalVideoCodec, kernelmedia.CanonicalVideoPreset, kernelmedia.CanonicalVideoCRF,
			s.Encoder.Codec, s.Encoder.Preset, s.Encoder.CRF)
	}
	if info, err := os.Stat(s.AudioPath); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("overlayjoin: audio %s is unavailable", s.AudioPath)
	}
	if s.BackgroundPath != "" {
		if info, err := os.Stat(s.BackgroundPath); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("%w: %s", ErrNoBackground, s.BackgroundPath)
		}
	}
	for i, clip := range s.Clips {
		if strings.TrimSpace(clip.Path) == "" {
			return fmt.Errorf("overlayjoin: clip[%d] has no path", i)
		}
		if clip.StartUS < 0 {
			return fmt.Errorf("overlayjoin: clip[%d] starts at negative time %d", i, clip.StartUS)
		}
		// An open-ended overlay is never allowed: once its own stream ends the
		// overlay would hold its last frame over the background for the rest of
		// the timeline. A missing window is a caller bug, not a licence to
		// freeze the frame.
		if clip.DurationUS <= 0 {
			return fmt.Errorf("overlayjoin: clip[%d] has no positive duration", i)
		}
		if info, err := os.Stat(clip.Path); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("overlayjoin: clip[%d] %s is unavailable", i, clip.Path)
		}
	}
	return nil
}

// Args builds the deterministic ffmpeg invocation for one assembly.
//
// Shape: [background] -> [clip 0 overlaid at 0:0 while t is in its window] ->
// ... -> mux the certified audio, trimmed to the timeline duration. A clip is
// time-shifted with setpts so its own PTS start is the plan window; `enable`
// makes the overlay visible only inside that window. Gaps therefore show the
// background, never black — the same contract the renderer holds.
func Args(spec Spec) ([]string, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	// One decoder thread per input bounds the per-input frame pools. A golden
	// assembly carries 70+ certified clips, and ffmpeg's default frame-threaded
	// decoders reserve a reference pool per input: with the defaults this graph
	// reached 11.7 GiB on 74 inputs and was killed by the kernel in production
	// at 15.6 GiB. The filter graph itself is NOT pinned any more: see
	// filterThreads for the measured budget and its memory headroom.
	args := []string{"-y", "-hide_banner", "-loglevel", "error",
		"-filter_complex_threads", strconv.Itoa(filterThreads(len(spec.Clips)))}
	if spec.BackgroundPath != "" {
		args = append(args, "-threads", "1", "-stream_loop", "-1", "-i", spec.BackgroundPath)
	} else {
		args = append(args, "-f", "lavfi", "-i",
			fmt.Sprintf("color=c=black:s=%dx%d:r=%d", spec.Width, spec.Height, spec.fps()))
	}
	for _, clip := range spec.Clips {
		args = append(args, "-threads", "1", "-i", clip.Path)
	}
	args = append(args, "-i", spec.AudioPath)

	filters := make([]string, 0, len(spec.Clips)+2)
	filters = append(filters, fmt.Sprintf("[0:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1,fps=%d,setpts=PTS-STARTPTS[base]",
		spec.Width, spec.Height, spec.Width, spec.Height, spec.fps()))
	video := "[base]"
	for i, clip := range spec.Clips {
		start := float64(clip.StartUS) / 1e6
		end := start + float64(clip.DurationUS)/1e6
		label := fmt.Sprintf("[v%d]", i)
		next := fmt.Sprintf("[ov%d]", i)
		filters = append(filters, fmt.Sprintf("[%d:v]setpts=PTS-STARTPTS+%.6f/TB%s",
			i+1, start, next))
		filters = append(filters, fmt.Sprintf("%s%soverlay=0:0:format=auto:eof_action=pass:shortest=0:enable='between(t,%.6f,%.6f)'%s",
			video, next, start, end, label))
		video = label
	}
	durationMS := float64(spec.DurationUS) / 1000
	if durationMS <= 0 {
		durationMS = 0
	}
	args = append(args, "-filter_complex", strings.Join(filters, ";"))
	args = append(args, "-map", video, "-map", fmt.Sprintf("%d:a", len(spec.Clips)+1))
	if durationMS > 0 {
		args = append(args, "-t", strconv.FormatFloat(durationMS/1000, 'f', 3, 64))
	}
	args = append(args, kernelmedia.CanonicalVideoEncoderArgs()...)
	args = append(args,
		"-r", strconv.Itoa(spec.fps()),
		"-c:a", "aac", "-b:a", "192k",
		"-movflags", "+faststart",
		spec.OutputPath,
	)
	return args, nil
}

func (s Spec) fps() int {
	if s.FPS > 0 {
		return s.FPS
	}
	return 24
}

// Run executes the assembly and returns the produced artifact facts.
//
// Up to segmentedInputThreshold clips the join is a single ffmpeg graph, which
// is the cheapest shape. Above it the graph would open every certified clip at
// once and each future-PTS overlay queues its input: that is the shape that
// reached 15.6 GiB and was killed in production, so the timeline is rendered in
// lossless segments instead (see segmented.go).
func Run(ctx context.Context, spec Spec, ffmpeg string) (Result, error) {
	args, err := Args(spec)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(ffmpeg) == "" {
		ffmpeg = "ffmpeg"
	}
	if err := os.MkdirAll(filepath.Dir(spec.OutputPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("overlayjoin: mkdir output: %w", err)
	}
	if len(spec.Clips) > segmentedInputThreshold && spec.DurationUS > 0 {
		return runSegmented(ctx, spec, ffmpeg)
	}
	timeout := 20 * time.Minute
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, ffmpeg, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("overlayjoin: ffmpeg failed: %w: %s", err, truncate(string(output), 800))
	}
	info, err := os.Stat(spec.OutputPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return Result{}, fmt.Errorf("overlayjoin: ffmpeg produced no output at %s", spec.OutputPath)
	}
	return Result{
		Path:       spec.OutputPath,
		SizeBytes:  info.Size(),
		DurationUS: spec.DurationUS,
	}, nil
}

// ProbeDurationUS reads a local clip's own duration. The join uses it when the
// plan window carries no length: without a bound the overlay would hold its
// last frame over the rest of the timeline, which is a freeze, not a join.
func ProbeDurationUS(ctx context.Context, path, ffprobe string) (int64, error) {
	if strings.TrimSpace(ffprobe) == "" {
		ffprobe = "ffprobe"
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, ffprobe,
		"-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("overlayjoin: probe %s: %w", path, err)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("overlayjoin: probe %s: unreadable duration %q", path, strings.TrimSpace(string(output)))
	}
	return int64(seconds * 1e6), nil
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
