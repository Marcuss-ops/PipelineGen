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
)

func runRenderFishShort(args []string) error {
	fs := flag.NewFlagSet("render-fish-short", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	input := fs.String("input", "", "source clip (required)")
	output := fs.String("output", "", "rendered output (required)")
	font := fs.String("font", "", "path to TrueType font for overlays (required)")
	dataDir := fs.String("data-dir", "data", "root directory for sound effects and other assets")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*input) == "" {
		return fmt.Errorf("--input is required")
	}
	if strings.TrimSpace(*output) == "" {
		return fmt.Errorf("--output is required")
	}
	if strings.TrimSpace(*font) == "" {
		return fmt.Errorf("--font is required")
	}
	if _, err := os.Stat(*input); err != nil {
		return fmt.Errorf("input clip unavailable: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		return err
	}

	effects := []struct {
		path  string
		delay int
		vol   string
	}{
		{filepath.Join(*dataDir, "media/sound_effects/sfx_ambient_sub_bass_drone_01.wav"), 0, "0.72"},
		{filepath.Join(*dataDir, "media/sound_effects/sfx_ambient_evolve_feedback_01.wav"), 2000, "0.78"},
		{filepath.Join(*dataDir, "media/sound_effects/sfx_cartoon_wtf_reaction_01.mp3"), 4000, "0.95"},
	}
	argsFF := []string{"-y", "-i", *input}
	for _, effect := range effects {
		argsFF = append(argsFF, "-i", effect.path)
	}

	video := "[0:v]"
	texts := []struct {
		text  string
		start string
		end   string
		size  string
	}{
		{"OCEAN’S STRANGEST REVEALS", "0", "8.534", "30"},
		{"Cosa si nasconde qui sotto? 🌊", "0", "2", "38"},
		{"Attenzione... Ci siamo...", "2", "4", "38"},
		{"MA CHE DIAVOLO È?! 😳😂", "4", "8.534", "42"},
	}
	for index, text := range texts {
		y := "h*0.13"
		if index > 0 {
			y = "h*0.48"
		}
		drawText := strings.ReplaceAll(text.text, "'", "\\\\'")
		video += fmt.Sprintf("drawtext=fontfile=%s:text='%s':fontcolor=0xFFFF00:fontsize=%s:borderw=3:bordercolor=black:shadowx=0:shadowy=0:x=(w-text_w)/2:y=%s:enable='between(t\\,%s\\,%s)',", *font, drawText, text.size, y, text.start, text.end)
	}
	video = strings.TrimSuffix(video, ",") + "[vout]"
	baseFilter := video + ";[0:a]aresample=48000,volume=0.78[base]"
	for i, effect := range effects {
		baseFilter += fmt.Sprintf(";[%d:a]aresample=48000,atrim=duration=1,asetpts=PTS-STARTPTS,volume=%s,adelay=%d|%d[sfx%d]", i+1, effect.vol, effect.delay, effect.delay, i)
	}
	labels := "[base]"
	for i := range effects {
		labels += fmt.Sprintf("[sfx%d]", i)
	}
	filter := baseFilter + fmt.Sprintf(";%samix=inputs=%d:duration=first:dropout_transition=0:normalize=0,alimiter=limit=0.95[aout]", labels, len(effects)+1)

	argsFF = append(argsFF, "-filter_complex", filter, "-map", "[vout]", "-map", "[aout]", "-c:v", "libx264", "-preset", "medium", "-crf", "18", "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", "-shortest", *output)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", argsFF...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("render fish short: %w: %s", err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("Fish short rendered with overlays and SFX: %s\n", *output)
	return nil
}
