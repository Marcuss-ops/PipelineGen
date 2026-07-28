package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

type shortRenderManifest struct {
	Input    string         `json:"input"`
	Output   string         `json:"output"`
	Font     string         `json:"font"`
	Upload   *shortUpload   `json:"upload,omitempty"`
	Effects  []shortEffect  `json:"effects"`
	Overlays []shortOverlay `json:"overlays"`
}
type shortUpload struct {
	FolderID string `json:"folder_id"`
	Filename string `json:"filename"`
}
type shortEffect struct {
	Path     string  `json:"path"`
	DelayMS  int     `json:"delay_ms"`
	Duration float64 `json:"duration"`
	Volume   string  `json:"volume"`
}
type shortOverlay struct {
	Text  string `json:"text"`
	Start string `json:"start"`
	End   string `json:"end"`
	Size  string `json:"size"`
	Y     string `json:"y"`
	Color string `json:"color"`
}

func runRenderShort(args []string) error {
	fs := flag.NewFlagSet("render-short", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "", "JSON render manifest (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*manifestPath) == "" {
		return fmt.Errorf("--manifest is required")
	}
	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var m shortRenderManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if strings.TrimSpace(m.Input) == "" || strings.TrimSpace(m.Output) == "" || strings.TrimSpace(m.Font) == "" {
		return fmt.Errorf("input, output and font are required")
	}
	if _, err := os.Stat(m.Input); err != nil {
		return fmt.Errorf("input unavailable: %w", err)
	}
	if len(m.Overlays) == 0 {
		return fmt.Errorf("at least one overlay is required")
	}
	if err := os.MkdirAll(filepath.Dir(m.Output), 0o755); err != nil {
		return err
	}

	ff := []string{"-y", "-i", m.Input}
	for _, e := range m.Effects {
		if _, err := os.Stat(e.Path); err != nil {
			return fmt.Errorf("effect unavailable: %w", err)
		}
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "ffmpeg", ff...).CombinedOutput(); err != nil {
		return fmt.Errorf("render short: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if m.Upload != nil {
		return uploadRenderedShort(ctx, m.Output, m.Upload.FolderID, m.Upload.Filename)
	}
	fmt.Printf("Rendered short: %s\n", m.Output)
	return nil
}

func uploadRenderedShort(ctx context.Context, path, folderID, filename string) error {
	if strings.TrimSpace(folderID) == "" || strings.TrimSpace(filename) == "" {
		return fmt.Errorf("upload folder_id and filename are required")
	}
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
	result, err := root.Drive.Admin.UploadFile(ctx, path, folderID, filename)
	if err != nil {
		return fmt.Errorf("upload rendered short: %w", err)
	}
	if result == nil || strings.TrimSpace(result.FileID) == "" {
		return fmt.Errorf("upload completed without a Drive file ID")
	}
	fmt.Printf("Uploaded rendered short: id=%s folder=%s\n", result.FileID, folderID)
	return nil
}
