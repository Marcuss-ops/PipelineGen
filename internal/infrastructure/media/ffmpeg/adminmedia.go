package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/adminmedia"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// AdminMediaProcessor adapts the canonical FFmpeg Processor to admin-media
// application ports. Processor is optional for backwards-compatible zero
// construction; production should use NewAdminMediaProcessor.
type AdminMediaProcessor struct {
	Processor *Processor
}

func NewAdminMediaProcessor(ffmpegPath string) AdminMediaProcessor {
	if strings.TrimSpace(ffmpegPath) == "" {
		ffmpegPath = "ffmpeg"
	}
	return AdminMediaProcessor{Processor: NewProcessor(ffmpegPath)}
}

func (p AdminMediaProcessor) processor() *Processor {
	if p.Processor != nil {
		return p.Processor
	}
	return NewProcessor("ffmpeg")
}

func (p AdminMediaProcessor) Probe(ctx context.Context, path string) (time.Duration, error) {
	info, err := p.processor().Probe(ctx, path)
	if err != nil {
		return 0, err
	}
	return info.Duration, nil
}

func (p AdminMediaProcessor) Trim(ctx context.Context, inputPath string, maxSeconds float64) error {
	proc := p.processor()
	ext := strings.ToLower(filepath.Ext(inputPath))
	tmpPath := inputPath + ".trim.tmp" + ext
	defer os.Remove(tmpPath)
	args := []string{"-y", "-i", inputPath, "-t", fmt.Sprintf("%.3f", maxSeconds)}
	switch ext {
	case ".mp4", ".mov", ".mkv":
		args = append(args, "-map", "0:v:0?", "-map", "0:a:0?", "-c:v", "libx264", "-preset", "medium", "-crf", "18", "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart")
	case ".wav":
		args = append(args, "-vn", "-c:a", "pcm_s16le")
	default:
		args = append(args, "-vn", "-c:a", "libmp3lame", "-q:a", "2")
	}
	args = append(args, tmpPath)
	if _, err := proc.runner.Run(ctx, proc.path, args, process.Options{Timeout: 10 * time.Minute, CombinedOutput: true}); err != nil {
		return fmt.Errorf("ffmpeg trim: %w", err)
	}
	return os.Rename(tmpPath, inputPath)
}

func (p AdminMediaProcessor) Render(ctx context.Context, m adminmedia.RenderManifest) error {
	ff := []string{"-y", "-i", m.Input}
	for _, e := range m.Effects {
		ff = append(ff, "-i", e.Path)
	}
	video := "[0:v]"
	for _, o := range m.Overlays {
		color, y := o.Color, o.Y
		if color == "" {
			color = "white"
		}
		if y == "" {
			y = "h*0.50"
		}
		text := strings.ReplaceAll(o.Text, "'", "\\\\'")
		video += fmt.Sprintf("drawtext=fontfile=%s:text='%s':fontcolor=%s:fontsize=%s:borderw=3:bordercolor=black:x=(w-text_w)/2:y=%s:enable='between(t\\,%s\\,%s)',", m.Font, text, color, o.Size, y, o.Start, o.End)
	}
	video = strings.TrimSuffix(video, ",") + "[vout]"
	filter := video + ";[0:a]aresample=48000,volume=0.78[base]"
	labels := "[base]"
	for i, e := range m.Effects {
		duration := e.Duration
		if duration <= 0 {
			duration = 1
		}
		volume := e.Volume
		if volume == "" {
			volume = "1"
		}
		filter += fmt.Sprintf(";[%d:a]aresample=48000,atrim=duration=%.3f,asetpts=PTS-STARTPTS,volume=%s,adelay=%d|%d[sfx%d]", i+1, duration, volume, e.DelayMS, e.DelayMS, i)
		labels += fmt.Sprintf("[sfx%d]", i)
	}
	filter += fmt.Sprintf(";%samix=inputs=%d:duration=first:dropout_transition=0:normalize=0,alimiter=limit=0.95[aout]", labels, len(m.Effects)+1)
	ff = append(ff, "-filter_complex", filter, "-map", "[vout]", "-map", "[aout]", "-c:v", "libx264", "-preset", "medium", "-crf", "18", "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", "-shortest", m.Output)
	proc := p.processor()
	_, err := proc.runner.Run(ctx, proc.path, ff, process.Options{Timeout: 20 * time.Minute, CombinedOutput: true})
	return err
}

var _ adminmedia.AudioEditor = AdminMediaProcessor{}
var _ adminmedia.ShortRenderer = AdminMediaProcessor{}
