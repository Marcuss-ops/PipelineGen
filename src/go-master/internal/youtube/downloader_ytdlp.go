package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Download scarica un video
func (d *Downloader) Download(ctx context.Context, url, filename string) (string, error) {
	outputPath := filepath.Join(d.outputDir, filename+".%(ext)s")

	args := []string{
		"-f", d.format,
		"-o", outputPath,
		"--no-playlist",
		"--newline",
		"--progress",
		url,
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("yt-dlp failed: %w, output: %s", err, string(output))
	}

	// Il file sarà .mp4 (o altro formato se mp4 non disponibile)
	mp4Path := filepath.Join(d.outputDir, filename+".mp4")

	logger.Info("Downloaded YouTube video", zap.String("url", url), zap.String("output", mp4Path))
	return mp4Path, nil
}

// DownloadAudio scarica solo l'audio
func (d *Downloader) DownloadAudio(ctx context.Context, url, filename string) (string, error) {
	outputPath := filepath.Join(d.outputDir, filename+".%(ext)s")

	args := []string{
		"-f", "bestaudio[ext=m4a]/bestaudio",
		"-o", outputPath,
		"--no-playlist",
		"--extract-audio",
		"--audio-format", "mp3",
		url,
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("yt-dlp failed: %w, output: %s", err, string(output))
	}

	mp3Path := filepath.Join(d.outputDir, filename+".mp3")
	logger.Info("Downloaded YouTube audio", zap.String("url", url), zap.String("output", mp3Path))
	return mp3Path, nil
}

// GetInfo ottiene informazioni sul video senza scaricare
func (d *Downloader) GetInfo(ctx context.Context, url string) (*VideoInfo, error) {
	args := []string{
		"--dump-json",
		"--no-playlist",
		url,
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp info failed: %w", err)
	}

	var info VideoInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return nil, fmt.Errorf("failed to parse video info: %w", err)
	}

	return &info, nil
}

// GetThumbnailURL restituisce l'URL della thumbnail
func (d *Downloader) GetThumbnailURL(ctx context.Context, url string) (string, error) {
	args := []string{
		"--get-thumbnail",
		"--no-playlist",
		url,
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("yt-dlp failed: %w", err)
	}

	return string(output), nil
}

// CheckAvailable verifica se yt-dlp è installato
func (d *Downloader) CheckAvailable() bool {
	cmd := exec.Command("yt-dlp", "--version")
	err := cmd.Run()
	return err == nil
}
