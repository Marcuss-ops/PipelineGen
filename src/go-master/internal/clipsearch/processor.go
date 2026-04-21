package clipsearch

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type ClipProcessor struct {
	ffmpegPath  string
	ffprobePath string
}

func NewClipProcessor(ffmpegPath, ffprobePath string) *ClipProcessor {
	return &ClipProcessor{
		ffmpegPath:  ffmpegPath,
		ffprobePath: ffprobePath,
	}
}

func (p *ClipProcessor) NormalizeClipToSevenSeconds1080p(ctx context.Context, rawPath string, sourceDuration float64) (string, error) {
	out := strings.TrimSuffix(rawPath, filepath.Ext(rawPath)) + "_7s_1080p.mp4"
	effectiveDuration := sourceDuration
	if probed, err := p.ProbeDurationSeconds(ctx, rawPath); err == nil && probed > 0 {
		effectiveDuration = probed
	}
	start := normalizeStartOffset(effectiveDuration)

	if err := p.runFFmpeg(ctx, buildPrimaryNormalizeArgs(rawPath, out, start)); err != nil {
		if retryErr := p.runFFmpeg(ctx, buildSilentAudioNormalizeArgs(rawPath, out, start)); retryErr != nil {
			return "", fmt.Errorf("ffmpeg normalize failed (with and without audio): %w", err)
		}
	}
	if err := p.VerifyCompatibleMP4(ctx, out); err != nil {
		return "", fmt.Errorf("normalized clip incompatible: %w", err)
	}
	return out, nil
}

func (p *ClipProcessor) VerifyCompatibleMP4(ctx context.Context, filePath string) error {
	if err := p.verifyVideoCompatibility(ctx, filePath); err != nil {
		return err
	}
	return p.verifyPositiveDuration(ctx, filePath)
}

func (p *ClipProcessor) verifyVideoCompatibility(ctx context.Context, filePath string) error {
	meta, err := p.ffprobeStreamMeta(ctx, filePath, "v:0", "stream=codec_name,pix_fmt")
	if err != nil {
		return fmt.Errorf("ffprobe video check failed: %w", err)
	}
	vCodec := strings.ToLower(strings.TrimSpace(meta["codec_name"]))
	vPixFmt := strings.ToLower(strings.TrimSpace(meta["pix_fmt"]))
	if vCodec != "h264" || vPixFmt != "yuv420p" {
		return fmt.Errorf("incompatible video stream: codec=%s pix_fmt=%s", vCodec, vPixFmt)
	}
	return nil
}

func (p *ClipProcessor) verifyPositiveDuration(ctx context.Context, filePath string) error {
	meta, err := p.ffprobeFormatMeta(ctx, filePath, "format=duration")
	if err != nil {
		return fmt.Errorf("ffprobe duration check failed: %w", err)
	}
	duration := strings.TrimSpace(meta["duration"])
	if duration == "" || duration == "0.000000" || duration == "N/A" {
		return fmt.Errorf("invalid duration: %s", duration)
	}
	return nil
}

func (p *ClipProcessor) ffprobeStreamMeta(ctx context.Context, filePath, streamSelector, showEntries string) (map[string]string, error) {
	cmd := exec.CommandContext(
		ctx,
		p.ffprobePath,
		"-v", "error",
		"-select_streams", streamSelector,
		"-show_entries", showEntries,
		"-of", "default=nw=1",
		filePath,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseFFprobeKVOutput(out), nil
}

func (p *ClipProcessor) ffprobeFormatMeta(ctx context.Context, filePath, showEntries string) (map[string]string, error) {
	cmd := exec.CommandContext(
		ctx,
		p.ffprobePath,
		"-v", "error",
		"-show_entries", showEntries,
		"-of", "default=nw=1",
		filePath,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseFFprobeKVOutput(out), nil
}

func (p *ClipProcessor) ProbeDurationSeconds(ctx context.Context, filePath string) (float64, error) {
	meta, err := p.ffprobeFormatMeta(ctx, filePath, "format=duration")
	if err != nil {
		return 0, err
	}
	d := strings.TrimSpace(meta["duration"])
	if d == "" || d == "N/A" {
		return 0, fmt.Errorf("duration unavailable")
	}
	val, err := strconv.ParseFloat(d, 64)
	if err != nil {
		return 0, err
	}
	if val < 0 {
		return 0, fmt.Errorf("negative duration")
	}
	return val, nil
}

func (p *ClipProcessor) runFFmpeg(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, p.ffmpegPath, args...)
	return cmd.Run()
}

// ComputeVisualHash extracts a small grayscale frame fingerprint from the clip.
// It is used as perceptual-like hash for deduplication (stable enough for near-identical clips).
func (p *ClipProcessor) ComputeVisualHash(ctx context.Context, filePath string) (string, error) {
	dur, err := p.ProbeDurationSeconds(ctx, filePath)
	if err != nil || dur <= 0 {
		dur = 7.0
	}
	seek := dur / 2.0
	if seek < 0.5 {
		seek = 0.5
	}
	cmd := exec.CommandContext(
		ctx,
		p.ffmpegPath,
		"-v", "error",
		"-ss", fmt.Sprintf("%.2f", seek),
		"-i", filePath,
		"-frames:v", "1",
		"-vf", "scale=32:32:flags=bilinear,format=gray",
		"-f", "rawvideo",
		"-",
	)
	raw, err := cmd.Output()
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(raw)
	return hex.EncodeToString(sum[:]), nil
}

func normalizeStartOffset(sourceDuration float64) float64 {
	if sourceDuration > 7 {
		return (sourceDuration - 7.0) / 2.0
	}
	return 0.0
}

func buildPrimaryNormalizeArgs(rawPath, out string, start float64) []string {
	return []string{
		"-y",
		"-ss", fmt.Sprintf("%.1f", start),
		"-i", rawPath,
		"-t", "7.0",
		"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black,format=yuv420p",
		"-c:v", "libx264",
		"-profile:v", "high",
		"-level", "4.1",
		"-pix_fmt", "yuv420p",
		"-preset", "fast",
		"-crf", "23",
		"-an",
		"-movflags", "+faststart",
		out,
	}
}

func buildSilentAudioNormalizeArgs(rawPath, out string, start float64) []string {
	return []string{
		"-y",
		"-ss", fmt.Sprintf("%.1f", start),
		"-i", rawPath,
		"-t", "7.0",
		"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black,format=yuv420p",
		"-c:v", "libx264",
		"-profile:v", "high",
		"-level", "4.1",
		"-pix_fmt", "yuv420p",
		"-preset", "fast",
		"-crf", "23",
		"-an",
		"-movflags", "+faststart",
		out,
	}
}
