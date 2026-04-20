package clipsearch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/clip"
)

func (d *ClipDownloader) DownloadFromArtlist(ctx context.Context, keyword string) (string, clip.IndexedClip, error) {
	if d.artlistSrc == nil {
		return "", clip.IndexedClip{}, fmt.Errorf("Artlist source not available")
	}
	candidates, err := d.artlistSrc.SearchClips(keyword, 20)
	if err != nil || len(candidates) == 0 {
		return "", clip.IndexedClip{}, fmt.Errorf("no Artlist clips for keyword %s", keyword)
	}

	outputDir := filepath.Join(d.downloadDir, "dynamic_clips", "artlist")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", clip.IndexedClip{}, err
	}

	ranked := uniqueArtlistCandidates(rankArtlistCandidates(candidates, keyword))
	maxAttempts := 7
	if len(ranked) < maxAttempts {
		maxAttempts = len(ranked)
	}
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		cand := ranked[i]
		sourceURL := resolveArtlistSourceURL(cand)
		if sourceURL == "" {
			lastErr = fmt.Errorf("candidate %s has no download URL", cand.ID)
			continue
		}

		targetPath := filepath.Join(outputDir, fmt.Sprintf("artlist_%s_%s.mp4", sanitizeFilename(keyword), sanitizeFilename(cand.ID)))
		os.Remove(targetPath)

		candidateCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		err = d.downloadCandidateWithRetry(candidateCtx, sourceURL, targetPath)
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("candidate %s failed: %w", cand.ID, err)
			continue
		}
		return targetPath, cand, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all Artlist candidates failed for keyword %s", keyword)
	}
	return "", clip.IndexedClip{}, lastErr
}

func (d *ClipDownloader) downloadCandidateWithRetry(ctx context.Context, sourceURL, targetPath string) error {
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		_ = os.Remove(targetPath)
		if err := d.downloadArtlistWithYTDLP(ctx, sourceURL, targetPath); err == nil && d.isValidDownloadedVideo(ctx, targetPath) {
			return nil
		} else {
			lastErr = err
		}
	}

	if strings.Contains(strings.ToLower(sourceURL), ".m3u8") {
		if lastErr == nil {
			lastErr = fmt.Errorf("hls download failed")
		}
		return fmt.Errorf("Artlist HLS download failed: %w", lastErr)
	}

	_ = os.Remove(targetPath)
	if err := d.downloadArtlistWithCurl(ctx, sourceURL, targetPath); err != nil {
		return fmt.Errorf("curl fallback failed: %w", err)
	}
	if !d.isValidDownloadedVideo(ctx, targetPath) {
		return fmt.Errorf("curl fallback produced invalid video")
	}
	return nil
}

func resolveArtlistSourceURL(c clip.IndexedClip) string {
	if strings.TrimSpace(c.DownloadLink) != "" {
		return c.DownloadLink
	}
	return c.DriveLink
}

func (d *ClipDownloader) downloadArtlistWithYTDLP(ctx context.Context, sourceURL, targetPath string) error {
	if d.ytDlpPath == "" {
		return fmt.Errorf("yt-dlp not configured")
	}
	args := []string{
		"-o", targetPath,
		"--no-playlist",
		"--socket-timeout", "30",
		"--retries", "3",
		"--hls-prefer-ffmpeg",
	}
	args = append(args, ytDLPAuthArgsFromEnv()...)
	args = append(args, sourceURL)
	cmd := exec.CommandContext(ctx, d.ytDlpPath, args...)
	return cmd.Run()
}

func (d *ClipDownloader) downloadArtlistWithCurl(ctx context.Context, sourceURL, targetPath string) error {
	cmd := exec.CommandContext(ctx, "curl", "-L", "-s", "-o", targetPath, sourceURL)
	return cmd.Run()
}

func (d *ClipDownloader) isValidDownloadedVideo(ctx context.Context, targetPath string) bool {
	st, err := os.Stat(targetPath)
	if err != nil || st.Size() == 0 {
		return false
	}
	if !d.hasVideoStream(ctx, targetPath) {
		os.Remove(targetPath)
		return false
	}
	return true
}

func (d *ClipDownloader) hasVideoStream(ctx context.Context, filePath string) bool {
	meta, err := ffprobeStreamMeta(ctx, filePath, "v:0", "stream=codec_name")
	if err != nil {
		return false
	}
	return strings.TrimSpace(meta["codec_name"]) != ""
}

func scoreArtlistCandidate(c clip.IndexedClip, keyword string) int {
	kw := strings.ToLower(keyword)
	score := 0
	if c.Width >= 1920 && c.Height >= 1080 {
		score += 30
	} else if c.Height >= 1080 {
		score += 20
	}
	if c.Duration >= 7 {
		score += 20
	}
	lowerBlob := strings.ToLower(c.Name + " " + c.Filename + " " + c.FolderPath + " " + strings.Join(c.Tags, " "))
	if strings.Contains(lowerBlob, kw) {
		score += 15
	}
	return score
}

func rankArtlistCandidates(candidates []clip.IndexedClip, keyword string) []clip.IndexedClip {
	ranked := append([]clip.IndexedClip(nil), candidates...)
	for i := 0; i < len(ranked)-1; i++ {
		for j := i + 1; j < len(ranked); j++ {
			if scoreArtlistCandidate(ranked[j], keyword) > scoreArtlistCandidate(ranked[i], keyword) {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	return ranked
}

func uniqueArtlistCandidates(candidates []clip.IndexedClip) []clip.IndexedClip {
	seen := make(map[string]bool, len(candidates))
	out := make([]clip.IndexedClip, 0, len(candidates))
	for _, c := range candidates {
		key := strings.TrimSpace(resolveArtlistSourceURL(c))
		if key == "" {
			key = strings.TrimSpace(c.ID)
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}
