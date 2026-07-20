package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

const belugaOutputFolderID = "1P4iVB9fHRUSa7fHeliJsIteIf2NGWRFs"

func runRenderBelugaShort(args []string) error {
	fs := flag.NewFlagSet("render-beluga-short", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	input := fs.String("input", "data/media/viral_videos/This beluga knew exactly what it was doing to that poor kid rAni.mp4", "source clip")
	output := fs.String("output", "data/media/viral_videos/rendered_beluga_short.mp4", "rendered output")
	font := fs.String("font", "/usr/share/fonts/truetype/msttcorefonts/Impact.ttf", "path to TrueType font for overlays")
	folderID := fs.String("folder-id", belugaOutputFolderID, "Google Drive destination folder ID")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if _, err := os.Stat(*input); err != nil {
		return fmt.Errorf("input clip unavailable: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		return err
	}

	// Paths to our sound effects
	boomSFX := "data/media/sound_effects/sfx_impact_boom_heavy_01.mp3"
	screamSFX := "data/media/sound_effects/sfx_cartoon_anime_villain_scream_01.mp3"
	laughSFX := "data/media/sound_effects/sfx_cartoon_laughter_short_01.mp3"

	effects := []struct {
		path     string
		delayMs  int
		duration float64
		vol      string
	}{
		{boomSFX, 6500, 2.0, "0.90"},   // Play at 6.5s
		{screamSFX, 7000, 2.5, "0.95"}, // Play at 7.0s
		{laughSFX, 7800, 2.5, "0.80"},  // Play at 7.8s
	}

	argsFF := []string{"-y", "-i", *input}
	for _, effect := range effects {
		argsFF = append(argsFF, "-i", effect.path)
	}

	// Texts overlays
	texts := []struct {
		text  string
		start string
		end   string
		size  string
		y     string
		color string
	}{
		{"VELOX EDITING", "0", "9.5", "24", "h*0.05", "white@0.6"}, // Watermark at the top
		{"JUST A CALM DAY AT THE AQUARIUM... \U0001F43B", "0", "4.5", "32", "h*0.50", "white"},
		{"WAIT, WHO IS THAT? \U0001F50D", "4.5", "6.4", "32", "h*0.50", "white"},
		{"OH MY GOD! \U0001F9E3\U0001F480", "6.5", "7.8", "42", "h*0.50", "white"},
		{"NEW CHILDHOOD FEAR UNLOCKED... \U0001F62D\U0001F602", "7.8", "9.5", "34", "h*0.50", "white"},
	}

	video := "[0:v]"
	for _, text := range texts {
		drawText := strings.ReplaceAll(text.text, "'", "\\\\'")
		video += fmt.Sprintf("drawtext=fontfile=%s:text='%s':fontcolor=%s:fontsize=%s:borderw=3:bordercolor=black:shadowx=0:shadowy=0:x=(w-text_w)/2:y=%s:enable='between(t\\,%s\\,%s)',", *font, drawText, text.color, text.size, text.y, text.start, text.end)
	}
	video = strings.TrimSuffix(video, ",") + "[vout]"

	// Mix Audio
	baseFilter := video + ";[0:a]aresample=48000,volume=0.78[base]"
	for i, effect := range effects {
		baseFilter += fmt.Sprintf(";[%d:a]aresample=48000,atrim=duration=%.3f,asetpts=PTS-STARTPTS,volume=%s,adelay=%d|%d[sfx%d]", i+1, effect.duration, effect.vol, effect.delayMs, effect.delayMs, i)
	}
	labels := "[base]"
	for i := range effects {
		labels += fmt.Sprintf("[sfx%d]", i)
	}
	filter := baseFilter + fmt.Sprintf(";%samix=inputs=%d:duration=first:dropout_transition=0:normalize=0,alimiter=limit=0.95[aout]", labels, len(effects)+1)

	argsFF = append(argsFF, "-filter_complex", filter, "-map", "[vout]", "-map", "[aout]", "-c:v", "libx264", "-preset", "medium", "-crf", "18", "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", "-shortest", *output)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	fmt.Printf("Rendering Beluga Short via FFmpeg to: %s\n", *output)
	cmd := exec.CommandContext(ctx, "ffmpeg", argsFF...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("render beluga short: %w: %s", err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("Beluga short rendered successfully.\n")

	// 2. Upload to Google Drive folder
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.Drive == nil || root.Drive.Admin == nil {
		return fmt.Errorf("Drive admin service is required for upload")
	}

	destFilename := "beluga-aquarium-prank-short.mp4"
	fmt.Printf("Uploading rendered clip to Drive folder %s as %s...\n", *folderID, destFilename)
	result, err := root.Drive.Admin.UploadFile(ctx, *output, *folderID, destFilename)
	if err != nil {
		return fmt.Errorf("upload rendered beluga clip: %w", err)
	}
	if result == nil || strings.TrimSpace(result.FileID) == "" {
		return fmt.Errorf("upload completed without a Drive file ID")
	}
	fmt.Printf("Beluga short uploaded: id=%s folder=%s link=%s\n", result.FileID, *folderID, result.WebViewLink)
	return nil
}
