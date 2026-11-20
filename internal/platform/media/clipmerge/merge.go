// Package clipmerge is the local assembler adapter for clip sequences.
//
// It consumes the merge decision owned by kernel/media/clipsequence: one output
// artifact per group, where a group with more than one clip is joined by the
// transitions that linked it and a group with one clip is copied verbatim. The
// adapter never decides WHICH clips belong together — that decision is already
// in the plan — and it never approximates a transition the local renderer
// cannot reproduce: a preset whose look only Chronon renders (shakes) is refused
// with ErrPresetNotExecutable instead of being replaced by a lookalike. Light
// leaks are rendered locally as the sweep program the preset declares.
package clipmerge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media/clipsequence"
)

// ErrPresetNotExecutable means the group needs a transition the local assembler
// cannot render faithfully. The preset's ChrononKind owns that look.
var ErrPresetNotExecutable = errors.New("clipmerge: transition preset is not executable locally")

// Options configures one merge run.
type Options struct {
	// FFmpeg and FFprobe default to the executables on PATH.
	FFmpeg  string
	FFprobe string
	// OutputDir receives one artifact per group. Required.
	OutputDir string
	// Encoder is the RESOLVED video encoder policy. Codec is required: an
	// assembler that picks its own encoder bypasses the policy owner (and is
	// rejected by percheck_video_encoder_policy).
	Encoder media.EncoderPolicy
	// Profile supplies the audio identity of the artifact. Zero uses the
	// canonical assembly contract's audio profile.
	Profile media.VideoProfile
	// CanonicalVideo pins the emitted video stream to the certified H.264
	// recipe. A sequence intended for the packet-copy assembler must set it:
	// the caller's encoder policy alone is not enough, because the same
	// nominal family can emit a different avcC. A single-clip group is copied
	// verbatim and never re-encoded, so the flag only affects merged groups.
	CanonicalVideo bool
	// Timeout bounds one group's ffmpeg run. Zero uses 20 minutes.
	Timeout time.Duration
	// Logf receives one line per group. Optional.
	Logf func(format string, args ...any)
}

// Artifact is one produced output file.
type Artifact struct {
	GroupID     string
	Path        string
	Merged      bool
	DurationMS  int64
	ClipIndexes []int
}

func (o Options) ffmpeg() string {
	if o.FFmpeg != "" {
		return o.FFmpeg
	}
	return "ffmpeg"
}

func (o Options) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return 20 * time.Minute
}

func (o Options) validateEncoder() error {
	if strings.TrimSpace(o.Encoder.Codec) == "" {
		return fmt.Errorf("clipmerge: encoder policy codec is required")
	}
	return nil
}

// Merge writes the artifacts of plan into opts.OutputDir and returns them in
// plan order.
func Merge(ctx context.Context, sequence clipsequence.Sequence, plan clipsequence.Plan, opts Options) ([]Artifact, error) {
	if strings.TrimSpace(opts.OutputDir) == "" {
		return nil, fmt.Errorf("clipmerge: output dir is required")
	}
	if err := opts.validateEncoder(); err != nil {
		return nil, err
	}
	if opts.CanonicalVideo && !media.IsCanonicalVideoEncoder(opts.Encoder.Codec, opts.Encoder.Preset, opts.Encoder.CRF) {
		return nil, fmt.Errorf("clipmerge: canonical video requires %s/%s/%d, got %q/%q/%d",
			media.CanonicalVideoCodec, media.CanonicalVideoPreset, media.CanonicalVideoCRF,
			opts.Encoder.Codec, opts.Encoder.Preset, opts.Encoder.CRF)
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("clipmerge: create output dir: %w", err)
	}
	if len(plan.Groups) == 0 {
		return nil, fmt.Errorf("clipmerge: plan has no groups")
	}
	artifacts := make([]Artifact, 0, len(plan.Groups))
	for _, group := range plan.Groups {
		artifact, err := mergeGroup(ctx, sequence, group, opts)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func mergeGroup(ctx context.Context, sequence clipsequence.Sequence, group clipsequence.Group, opts Options) (Artifact, error) {
	clips, err := groupClips(sequence, group)
	if err != nil {
		return Artifact{}, err
	}
	outPath := filepath.Join(opts.OutputDir, group.ID+".mp4")
	if !group.Merged {
		if err := copyFile(clips[0].Path, outPath); err != nil {
			return Artifact{}, fmt.Errorf("clipmerge: %s: %w", group.ID, err)
		}
		if opts.Logf != nil {
			opts.Logf("clipmerge: %s is the standalone clip %s", group.ID, clips[0].AssetID)
		}
		return Artifact{GroupID: group.ID, Path: outPath, Merged: false, DurationMS: group.DurationMS, ClipIndexes: group.ClipIndexes}, nil
	}
	needsSize := false
	for _, transition := range group.Transitions {
		spec, ok := clipsequence.LookupPreset(transition.Preset)
		if !ok {
			return Artifact{}, fmt.Errorf("clipmerge: %s: unknown preset %q", group.ID, transition.Preset)
		}
		if !spec.LocallyExecutable() {
			return Artifact{}, fmt.Errorf("%w: preset %q is rendered by Chronon kind %q",
				ErrPresetNotExecutable, transition.Preset, spec.ChrononKind)
		}
		if transition.Leak != nil {
			needsSize = true
		}
	}
	var size videoSize
	if needsSize {
		var err error
		size, err = probeVideoSize(ctx, opts, clips[0].Path)
		if err != nil {
			return Artifact{}, fmt.Errorf("clipmerge: %s: %w", group.ID, err)
		}
	}
	args, err := mergeArgs(clips, group, opts, size, outPath)
	if err != nil {
		return Artifact{}, fmt.Errorf("clipmerge: %s: %w", group.ID, err)
	}
	runCtx, cancel := context.WithTimeout(ctx, opts.timeout())
	defer cancel()
	cmd := exec.CommandContext(runCtx, opts.ffmpeg(), args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Artifact{}, fmt.Errorf("clipmerge: %s: ffmpeg failed: %w: %s", group.ID, err, strings.TrimSpace(stderr.String()))
	}
	if opts.Logf != nil {
		opts.Logf("clipmerge: %s merged %d clips with %d transitions", group.ID, len(clips), len(group.Transitions))
	}
	return Artifact{GroupID: group.ID, Path: outPath, Merged: true, DurationMS: group.DurationMS, ClipIndexes: group.ClipIndexes}, nil
}

func groupClips(sequence clipsequence.Sequence, group clipsequence.Group) ([]clipsequence.Clip, error) {
	clips := make([]clipsequence.Clip, 0, len(group.ClipIndexes))
	for _, index := range group.ClipIndexes {
		if index < 0 || index >= len(sequence.Clips) {
			return nil, fmt.Errorf("clipmerge: group %s references clip %d outside the sequence", group.ID, index)
		}
		clip := sequence.Clips[index]
		if strings.TrimSpace(clip.Path) == "" {
			return nil, fmt.Errorf("clipmerge: group %s clip %d (%s) has no artifact path", group.ID, index, clip.AssetID)
		}
		if _, err := os.Stat(clip.Path); err != nil {
			return nil, fmt.Errorf("clipmerge: group %s clip %d (%s): %w", group.ID, index, clip.AssetID, err)
		}
		clips = append(clips, clip)
	}
	return clips, nil
}

// videoSize is the delivered frame size, needed to build a full-frame light
// leak plate without a scale2ref hop (which truncates the filtergraph at the
// plate's EOF).
type videoSize struct{ width, height int }

// probeVideoSize reads the first video stream of an artifact. Only groups that
// contain a light leak call it: an xfade-only merge never needs the size.
func probeVideoSize(ctx context.Context, opts Options, path string) (videoSize, error) {
	ffprobe := opts.FFprobe
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	out, err := exec.CommandContext(ctx, ffprobe,
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=p=0:s=x", path).Output()
	if err != nil {
		return videoSize{}, fmt.Errorf("probe frame size: %w", err)
	}
	var size videoSize
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%dx%d", &size.width, &size.height); err != nil {
		return videoSize{}, fmt.Errorf("probe frame size: unexpected output %q", strings.TrimSpace(string(out)))
	}
	if size.width <= 0 || size.height <= 0 {
		return videoSize{}, fmt.Errorf("probe frame size: invalid size %dx%d", size.width, size.height)
	}
	return size, nil
}

// leakPlateFPS is the rate the leak plate is generated at. The overlay samples
// it by timestamp, so the output keeps the clips' own rate; a higher plate rate
// only makes the sweep smoother on sources that run faster than 24 fps.
const leakPlateFPS = 60

// hexComponent parses one #RRGGBB component into its 0-255 value.
func hexComponent(hex string, offset int) (int, error) {
	var value int
	if _, err := fmt.Sscanf(hex[offset:offset+2], "%02x", &value); err != nil {
		return 0, fmt.Errorf("clipmerge: invalid light-leak colour %q", hex)
	}
	return value, nil
}

// leakAlphaExpression is the geq alpha of the sweep band: a diagonal Gaussian
// centred at a point that travels across the frame for the plate's duration,
// faded in and out with a half sine so the leak never starts or stops on a step.
// X, Y, W, H and T are geq's frame variables (T is plate-relative seconds).
func leakAlphaExpression(leak clipsequence.LeakSpec, seconds float64) string {
	duration := strconv.FormatFloat(seconds, 'f', 3, 64)
	peak := strconv.FormatFloat(leak.PeakAlpha, 'f', 3, 64)
	band := strconv.FormatFloat(leak.BandWidth, 'f', 3, 64)
	coord := "((X/W+Y/H)/2)"
	if leak.Direction == "left" {
		coord = "((1-X/W+Y/H)/2)"
	}
	return fmt.Sprintf("255*%s*exp(-pow((%s-(T/%s*1.4-0.2))/%s,2))*sin(PI*T/%s)",
		peak, coord, duration, band, duration)
}

// leakFilters builds the light-leak program for one join: a fast dissolve under
// a warm band that sweeps across the frame exactly for the overlap. The plate is
// shifted onto the join's timestamp so it covers only the transition.
func leakFilters(baseLabel, incomingLabel, outLabel string, index int, preset clipsequence.Preset, leakSpec *clipsequence.LeakSpec, seconds, offset float64, size videoSize) ([]string, error) {
	if leakSpec == nil {
		return nil, fmt.Errorf("clipmerge: preset %q has no light-leak program", preset)
	}
	leak := *leakSpec
	if len(leak.Color) != 7 || leak.Color[0] != '#' {
		return nil, fmt.Errorf("clipmerge: light-leak colour %q must be #RRGGBB", leak.Color)
	}
	if leak.PeakAlpha <= 0 || leak.PeakAlpha > 1 {
		return nil, fmt.Errorf("clipmerge: light-leak peak alpha %v must be in (0,1]", leak.PeakAlpha)
	}
	if leak.BandWidth <= 0 || leak.BandWidth > 1 {
		return nil, fmt.Errorf("clipmerge: light-leak band width %v must be in (0,1]", leak.BandWidth)
	}
	if leak.Direction != "right" && leak.Direction != "left" {
		return nil, fmt.Errorf("clipmerge: light-leak direction %q must be right or left", leak.Direction)
	}
	r, err := hexComponent(leak.Color, 1)
	if err != nil {
		return nil, err
	}
	g, err := hexComponent(leak.Color, 3)
	if err != nil {
		return nil, err
	}
	b, err := hexComponent(leak.Color, 5)
	if err != nil {
		return nil, err
	}
	return []string{
		// The dissolve keeps the cut from stepping where the band is thin;
		// the leak is what the viewer reads as the transition.
		fmt.Sprintf("%s%sxfade=transition=fade:duration=%.3f:offset=%.3f[leakbase%d]", baseLabel, incomingLabel, seconds, offset, index),
		fmt.Sprintf("color=c=0x%02X%02X%02X:s=%dx%d:r=%d:d=%.3f,format=rgba,"+
			"geq=r='%d':g='%d':b='%d':a='%s',setpts=PTS+%.3f/TB[leak%d]",
			r, g, b, size.width, size.height, leakPlateFPS, seconds,
			r, g, b, leakAlphaExpression(leak, seconds), offset, index),
		fmt.Sprintf("[leakbase%d][leak%d]overlay=0:0:format=auto:eof_action=pass:shortest=0%s",
			index, index, outLabel),
	}, nil
}

// mergeArgs builds the ffmpeg invocation for one group.
//
// Video joins through xfade (or the leak program, which is a dissolve plus a
// sweep), audio through acrossfade with the SAME duration, so A/V stay in sync
// across the overlap. The k-th offset is the running output length before that
// join minus the join itself, which is exactly where xfade expects the
// transition to start.
func mergeArgs(clips []clipsequence.Clip, group clipsequence.Group, opts Options, size videoSize, outPath string) ([]string, error) {
	args := []string{"-y", "-hide_banner", "-loglevel", "error"}
	for _, clip := range clips {
		args = append(args, "-i", clip.Path)
	}
	var filters []string
	// Every join filter refuses inputs whose timebases differ, and delivered
	// artifacts do not share one (a stream-copied clip can be 1/90000 while a
	// re-encoded one is 1/12288). Normalising to AVTB before joining is what
	// makes a merge work across differently produced clips. Only settb is
	// applied: setpts would leave xfade unable to detect a frame rate.
	for i := range clips {
		filters = append(filters, fmt.Sprintf("[%d:v]settb=AVTB[clipv%d]", i, i))
	}
	videoIn := "[clipv0]"
	outMS := clips[0].DurationMS
	for i := 1; i < len(clips); i++ {
		transition := group.Transitions[i-1]
		seconds := float64(transition.DurationMS) / 1000
		offset := float64(outMS-transition.DurationMS) / 1000
		spec, _ := clipsequence.LookupPreset(transition.Preset)
		label := fmt.Sprintf("[v%d]", i)
		if transition.Leak != nil {
			leak, err := leakFilters(videoIn, fmt.Sprintf("[clipv%d]", i), label, i, transition.Preset, transition.Leak, seconds, offset, size)
			if err != nil {
				return nil, err
			}
			filters = append(filters, leak...)
		} else {
			filters = append(filters, fmt.Sprintf("%s[clipv%d]xfade=transition=%s:duration=%.3f:offset=%.3f%s",
				videoIn, i, spec.Xfade, seconds, offset, label))
		}
		videoIn = label
		outMS += clips[i].DurationMS - transition.DurationMS
	}
	audioIn := "[0:a]"
	for i := 1; i < len(clips); i++ {
		transition := group.Transitions[i-1]
		seconds := float64(transition.DurationMS) / 1000
		label := fmt.Sprintf("[a%d]", i)
		filters = append(filters, fmt.Sprintf("%s[%d:a]acrossfade=d=%.3f:c1=tri:c2=tri%s",
			audioIn, i, seconds, label))
		audioIn = label
	}
	profile := opts.Profile.WithDefaults()
	args = append(args, "-filter_complex", strings.Join(filters, ";"))
	args = append(args, "-map", videoIn, "-map", audioIn)
	if opts.CanonicalVideo {
		// ONE recipe shared with the fragment certifier and the final join:
		// profile/level, refs, B-frames, GOP and the bt709/tv VUI all land in
		// the avcC, which is the identity the copy-only assembler compares.
		args = append(args, media.CanonicalVideoEncoderArgs()...)
		args = append(args, "-r", strconv.Itoa(media.CanonicalFPSNum))
	} else {
		args = append(args, "-c:v", opts.Encoder.Codec)
		if opts.Encoder.Preset != "" {
			args = append(args, "-preset", opts.Encoder.Preset)
		}
		if opts.Encoder.CRF > 0 {
			args = append(args, "-crf", fmt.Sprintf("%d", opts.Encoder.CRF))
		}
		args = append(args, "-pix_fmt", "yuv420p")
	}
	args = append(args,
		"-c:a", profile.AudioCodec, "-b:a", profile.AudioBitrate,
		"-movflags", "+faststart",
		outPath,
	)
	return args, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
