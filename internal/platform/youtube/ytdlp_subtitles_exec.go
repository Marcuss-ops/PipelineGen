package youtube

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	transcript "github.com/Marcuss-ops/PipelineGen/internal/domain/transcript"
)

// buildSubtitleArgs appends subtitle-specific arguments to the canonical
// CommandBuilder base arguments.
func (a *YTDLPSubtitleAdapter) buildSubtitleArgs(videoURL, outputTemplate string) []string {
	baseArgs := a.cmdBuilder.BaseArgs(videoURL, a.useCookies)
	args := append([]string{}, baseArgs...)
	args = append(args,
		"--write-auto-subs",
		"--write-subs",
		"--skip-download",
		"--sub-langs", "en",
		"--sub-format", "vtt",
		"-o", outputTemplate,
		videoURL,
	)
	return args
}

// fetchTimedTranscript owns temp-directory lifecycle, yt-dlp execution, VTT
// discovery and conversion into timed transcript entries.
func (a *YTDLPSubtitleAdapter) fetchTimedTranscript(ctx context.Context, videoURL string) ([]transcript.Entry, error) {
	if a.ytdlp == nil {
		return nil, fmt.Errorf("YTDLPSubtitleAdapter: ytdlp not wired (composition bug — call downloader.NewYTDLP(cfg) in lifecycle.go)")
	}

	tempDir, err := os.MkdirTemp("", "ytdlp_subs_*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	subArgs := a.buildSubtitleArgs(videoURL, filepath.Join(tempDir, "subs"))
	subCmd := exec.CommandContext(ctx, a.ytdlp.Path(), subArgs...)
	if out, err := subCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("subtitle download failed: %w, output: %s", err, string(out))
	}

	var vttPath string
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return nil, fmt.Errorf("read temp dir: %w", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "subs.") && strings.HasSuffix(entry.Name(), ".vtt") {
			vttPath = filepath.Join(tempDir, entry.Name())
			break
		}
	}
	if vttPath == "" {
		return nil, fmt.Errorf("no subtitle file found for video: %s", videoURL)
	}

	vttData, err := os.ReadFile(vttPath)
	if err != nil {
		return nil, fmt.Errorf("read vtt file: %w", err)
	}
	content := stripVTTHeader(string(vttData))

	var out []transcript.Entry
	for _, block := range strings.Split(content, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		start, end, text, ok := parseVTTBlock(block)
		if !ok || text == "" {
			continue
		}
		out = append(out, transcript.Entry{
			Start: start,
			End:   end,
			Text:  text,
		})
	}
	return out, nil
}
