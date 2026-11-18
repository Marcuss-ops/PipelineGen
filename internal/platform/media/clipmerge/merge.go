// Package clipmerge is the local assembler adapter for clip sequences.
//
// It consumes the merge decision owned by kernel/media/clipsequence: one output
// artifact per group, where a group with more than one clip is joined by the
// transitions that linked it and a group with one clip is copied verbatim. The
// adapter never decides WHICH clips belong together — that decision is already
// in the plan — and it never approximates a transition the local renderer
// cannot reproduce: a preset whose renderer is Chronon (light leaks, shakes) is
// refused with ErrPresetNotExecutable instead of being replaced by a lookalike.
package clipmerge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	for _, transition := range group.Transitions {
		spec, ok := clipsequence.LookupPreset(transition.Preset)
		if !ok {
			return Artifact{}, fmt.Errorf("clipmerge: %s: unknown preset %q", group.ID, transition.Preset)
		}
		if !spec.LocallyExecutable() {
			return Artifact{}, fmt.Errorf("%w: preset %q is rendered by Chronon kind %q",
				ErrPresetNotExecutable, transition.Preset, spec.ChrononKind)
		}
	}
	args := mergeArgs(clips, group, opts, outPath)
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

// mergeArgs builds the ffmpeg invocation for one group.
//
// Video joins through xfade, audio through acrossfade with the SAME duration, so
// A/V stay in sync across the overlap. The k-th offset is the running output
// length before that join minus the join itself, which is exactly where xfade
// expects the transition to start.
func mergeArgs(clips []clipsequence.Clip, group clipsequence.Group, opts Options, outPath string) []string {
	args := []string{"-y", "-hide_banner", "-loglevel", "error"}
	for _, clip := range clips {
		args = append(args, "-i", clip.Path)
	}
	var filters []string
	videoIn := "[0:v]"
	outMS := clips[0].DurationMS
	for i := 1; i < len(clips); i++ {
		transition := group.Transitions[i-1]
		seconds := float64(transition.DurationMS) / 1000
		offset := float64(outMS-transition.DurationMS) / 1000
		spec, _ := clipsequence.LookupPreset(transition.Preset)
		label := fmt.Sprintf("[v%d]", i)
		filters = append(filters, fmt.Sprintf("%s[%d:v]xfade=transition=%s:duration=%.3f:offset=%.3f%s",
			videoIn, i, spec.Xfade, seconds, offset, label))
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
	args = append(args,
		"-map", videoIn, "-map", audioIn,
		"-c:v", opts.Encoder.Codec,
	)
	if opts.Encoder.Preset != "" {
		args = append(args, "-preset", opts.Encoder.Preset)
	}
	if opts.Encoder.CRF > 0 {
		args = append(args, "-crf", fmt.Sprintf("%d", opts.Encoder.CRF))
	}
	args = append(args,
		"-pix_fmt", "yuv420p",
		"-c:a", profile.AudioCodec, "-b:a", profile.AudioBitrate,
		"-movflags", "+faststart",
		outPath,
	)
	return args
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
