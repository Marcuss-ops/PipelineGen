package ytdlp

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
)

type YTDLPCommand struct {
	ytdlpPath string
}

func NewYTDLPCommand(ytdlpPath string) *YTDLPCommand {
	if ytdlpPath == "" {
		ytdlpPath = "yt-dlp"
	}
	return &YTDLPCommand{ytdlpPath: ytdlpPath}
}

func (y *YTDLPCommand) Download(ctx context.Context, input DownloadInput) (*DownloadResult, error) {
	outputTemplate := filepath.Join(input.OutputDir, "%(title)s.%(ext)s")
	if input.Filename != "" {
		outputTemplate = filepath.Join(input.OutputDir, input.Filename)
	}

	args := []string{
		"-o", outputTemplate,
		"--no-playlist",
		input.URL,
	}

	if input.StartTime != nil {
		args = append([]string{"--download-sections", "*" + *input.StartTime + "-" + *input.EndTime}, args...)
	}

	cmd := exec.CommandContext(ctx, y.ytdlpPath, args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("yt-dlp download failed: %w", err)
	}

	return &DownloadResult{
		LocalPath: outputTemplate,
		Title:     input.Filename,
	}, nil
}
