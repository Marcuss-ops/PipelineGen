package youtube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.uber.org/zap"

	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// SubtitleFetcher downloads and parses YouTube subtitle tracks via yt-dlp.
type SubtitleFetcher struct {
	cfg *ytcfg.Config
	log *zap.Logger
}

// NewSubtitleFetcher constructs the adapter. cfg.External.ResolvedYtdlpPath()
// must be set (composition root guarantees it).
func NewSubtitleFetcher(cfg *ytcfg.Config, log *zap.Logger) *SubtitleFetcher {
	return &SubtitleFetcher{cfg: cfg, log: log}
}

// FetchFullVTT downloads the VTT file for videoURL and parses it into
// a list of timed entries. Returns (entries, nil) when successful.
func (f *SubtitleFetcher) FetchFullVTT(ctx context.Context, videoURL string) ([]TimedEntry, error) {
	tempDir, err := os.MkdirTemp("", "yt_subs_full_*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	subPrefix := filepath.Join(tempDir, "subs")
	ytdlpPath := f.cfg.External.ResolvedYtdlpPath()
	cookiesPath := f.cfg.External.YouTubeCookiesPath
	if cookiesPath == "" {
		cookiesPath = "config/youtube_cookies.txt"
	}

	args := []string{
		"--write-auto-subs", "--write-subs", "--skip-download",
		"--sub-langs", "en,it", "--sub-format", "vtt",
		"--no-warnings",
	}
	if _, err := os.Stat(cookiesPath); err == nil {
		args = append(args, "--cookies", cookiesPath)
	}
	if jsRuntime := f.cfg.External.YouTubeJSRuntimePath; jsRuntime != "" {
		args = append(args, "--js-runtime", jsRuntime)
		args = append(args, "--remote-components", "ejs:github")
	}
	args = append(args, "-o", subPrefix, videoURL)

	runner := NewProcessRunner(f.log)
	if _, _, err := runner.Run(ctx, ytdlpPath, args); err != nil {
		f.log.Warn("subtitle download had issues, checking for partial results",
			zap.String("url", videoURL), zap.Error(err))
	}

	var vttPath string
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return nil, fmt.Errorf("read temp dir: %w", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "subs.") && strings.HasSuffix(e.Name(), ".vtt") {
			vttPath = filepath.Join(tempDir, e.Name())
			break
		}
	}
	if vttPath == "" {
		return nil, fmt.Errorf("no subtitles file found")
	}

	return parseVTTFileEntries(vttPath)
}

// SliceSubtitles writes a transcript text file at outputPath with only
// the cues overlapping [startSec, endSec].
func (f *SubtitleFetcher) SliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error {
	tempDir, err := os.MkdirTemp("", "yt_subs_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	subPrefix := filepath.Join(tempDir, "subs")
	ytdlpPath := f.cfg.External.ResolvedYtdlpPath()
	cookiesPath := f.cfg.External.YouTubeCookiesPath
	if cookiesPath == "" {
		cookiesPath = "config/youtube_cookies.txt"
	}

	args := []string{
		"--write-auto-subs", "--write-subs", "--skip-download",
		"--sub-langs", "en,it", "--sub-format", "vtt",
		"--no-warnings",
	}
	if _, err := os.Stat(cookiesPath); err == nil {
		args = append(args, "--cookies", cookiesPath)
	}
	if jsRuntime := f.cfg.External.YouTubeJSRuntimePath; jsRuntime != "" {
		args = append(args, "--js-runtime", jsRuntime)
		args = append(args, "--remote-components", "ejs:github")
	}
	args = append(args, "-o", subPrefix, fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID))

	runner := NewProcessRunner(f.log)
	f.log.Info("Downloading subtitles for slicing", zap.String("video_id", videoID))
	if _, _, err := runner.Run(ctx, ytdlpPath, args); err != nil {
		f.log.Warn("Failed to download all subtitles (some might still have downloaded)",
			zap.String("video_id", videoID), zap.Error(err))
	}

	// Scan tempDir for VTT files
	files, err := os.ReadDir(tempDir)
	if err != nil {
		return err
	}

	var vttPath string
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "subs.") && strings.HasSuffix(file.Name(), ".vtt") {
			vttPath = filepath.Join(tempDir, file.Name())
			break
		}
	}
	if vttPath == "" {
		return fmt.Errorf("no subtitles file found for video %s", videoID)
	}

	f.log.Info("Parsing subtitle VTT file", zap.String("path", vttPath))
	transcript, err := parseVTTFile(vttPath, float64(startSec), float64(endSec))
	if err != nil {
		return err
	}
	if transcript == "" {
		return fmt.Errorf("no subtitles found in the specified time window %d-%d", startSec, endSec)
	}

	txtPath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + ".txt"
	if err := os.WriteFile(txtPath, []byte(transcript), 0644); err != nil {
		return fmt.Errorf("failed to write transcription text file: %w", err)
	}

	f.log.Info("Successfully wrote sliced subtitles to text file", zap.String("path", txtPath))
	return nil
}

// DownloadSubtitles downloads subtitle files to outputDir and returns the path
// to the first VTT file found, or error.
func (f *SubtitleFetcher) DownloadSubtitles(ctx context.Context, videoURL string, langs string, outputDir string) (string, error) {
	ytdlpPath := f.cfg.External.ResolvedYtdlpPath()
	subPrefix := filepath.Join(outputDir, "subs")

	args := []string{
		"--write-auto-subs", "--write-subs", "--skip-download",
		"--sub-langs", langs, "--sub-format", "vtt",
		"--no-warnings",
		"-o", subPrefix,
		videoURL,
	}

	runner := NewProcessRunner(f.log)
	if _, _, err := runner.Run(ctx, ytdlpPath, args); err != nil {
		f.log.Info("subtitle download had issues, checking for partial results",
			zap.String("url", videoURL), zap.Error(err))
	}

	// Find VTT file(s)
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return "", err
	}
	for _, de := range entries {
		if strings.HasPrefix(de.Name(), "subs.") && strings.HasSuffix(de.Name(), ".vtt") {
			return filepath.Join(outputDir, de.Name()), nil
		}
	}
	return "", nil
}

// ── VTT parsing helpers ────────────────────────────────────────────────

// vttCue is a parsed subtitle cue with timing and text.
type vttCue struct {
	start float64
	end   float64
	text  string
}

// parseVTTFile parses a VTT file and returns transcript text for the given time window.
func parseVTTFile(vttPath string, startSec, endSec float64) (string, error) {
	data, err := os.ReadFile(vttPath)
	if err != nil {
		return "", err
	}

	content := string(data)
	content = regexp.MustCompile(`(?s)^WEBVTT.*?\n\n`).ReplaceAllString(content, "")

	blocks := strings.Split(content, "\n\n")
	var cues []vttCue
	timeRegex := regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3})\s*-->\s*(\d{1,2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3})`)

	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 2 {
			continue
		}
		var timeLine string
		var textLines []string
		for _, line := range lines {
			if timeRegex.MatchString(line) {
				timeLine = line
			} else if timeLine != "" {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" && !strings.HasPrefix(trimmed, "align:") && !strings.HasPrefix(trimmed, "position:") {
					textLines = append(textLines, line)
				}
			}
		}
		if timeLine == "" {
			continue
		}
		matches := timeRegex.FindStringSubmatch(timeLine)
		if len(matches) < 3 {
			continue
		}
		cueStart := textutil.ParseVTTTimestamp(matches[1])
		cueEnd := textutil.ParseVTTTimestamp(matches[2])
		if cueEnd > startSec && cueStart < endSec {
			text := textutil.CleanSubtitleText(strings.Join(textLines, " "))
			if text != "" {
				cues = append(cues, vttCue{start: cueStart, end: cueEnd, text: text})
			}
		}
	}

	// Dedup rolling cues
	var dedupedCues []vttCue
	for i := 0; i < len(cues); i++ {
		longest := cues[i]
		for j := i + 1; j < len(cues); j++ {
			if cues[j].start < longest.end || cues[j].start < longest.start+0.5 {
				if len(cues[j].text) > len(longest.text) {
					longest = cues[j]
				}
				i = j
			} else {
				break
			}
		}
		dedupedCues = append(dedupedCues, longest)
	}

	// Strip suffix-prefix overlap
	for i := 1; i < len(dedupedCues); i++ {
		dedupedCues[i].text = stripCueOverlap(dedupedCues[i-1].text, dedupedCues[i].text)
	}

	var parts []string
	for _, c := range dedupedCues {
		if c.text != "" {
			parts = append(parts, c.text)
		}
	}
	return strings.Join(parts, " "), nil
}

// parseVTTFileEntries parses a VTT file into timed entries (used by FetchFullVTT).
func parseVTTFileEntries(vttPath string) ([]TimedEntry, error) {
	data, err := os.ReadFile(vttPath)
	if err != nil {
		return nil, err
	}

	content := string(data)
	content = regexp.MustCompile(`(?s)^WEBVTT.*?\n\n`).ReplaceAllString(content, "")

	timeRegex := regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3})\s*-->\s*(\d{1,2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3})`)

	var entries []TimedEntry
	for _, block := range strings.Split(content, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		var timeLine string
		var textLines []string
		for _, line := range lines {
			if timeRegex.MatchString(line) {
				timeLine = line
			} else if timeLine != "" {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "align:") && !strings.HasPrefix(line, "position:") {
					textLines = append(textLines, line)
				}
			}
		}
		if timeLine == "" || len(textLines) == 0 {
			continue
		}
		matches := timeRegex.FindStringSubmatch(timeLine)
		if len(matches) < 3 {
			continue
		}
		start := textutil.ParseVTTTimestamp(matches[1])
		end := textutil.ParseVTTTimestamp(matches[2])
		text := textutil.CleanSubtitleText(strings.Join(textLines, " "))
		if text == "" {
			continue
		}
		entries = append(entries, TimedEntry{Start: start, End: end, Text: text})
	}
	return entries, nil
}

// stripCueOverlap removes the overlapping suffix-prefix text between
// consecutive deduped cues from YouTube's rolling VTT format.
func stripCueOverlap(prev, curr string) string {
	if prev == "" || curr == "" {
		return curr
	}
	prevWords := strings.Fields(strings.ToLower(prev))
	currWords := strings.Fields(strings.ToLower(curr))
	if len(prevWords) == 0 || len(currWords) == 0 {
		return curr
	}
	maxMatch := len(currWords)
	if maxMatch > len(prevWords) {
		maxMatch = len(prevWords)
	}
	bestMatch := 0
	for i := maxMatch; i >= 2; i-- {
		suffix := prevWords[len(prevWords)-i:]
		prefix := currWords[:i]
		match := true
		for j := 0; j < i; j++ {
			if suffix[j] != prefix[j] {
				match = false
				break
			}
		}
		if match {
			bestMatch = i
			break
		}
	}
	if bestMatch > 0 {
		origFields := strings.Fields(curr)
		if bestMatch >= len(origFields) {
			return curr
		}
		stripped := strings.Join(origFields[bestMatch:], " ")
		if stripped == "" {
			return curr
		}
		return stripped
	}
	return curr
}
