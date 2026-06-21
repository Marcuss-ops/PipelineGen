package youtube

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// SubtitleFetcherAdapter is the concrete impl of the SubtitleFetcher
// port declared in ports.go. It owns:
//
//   - the VTT rolling-cue parser (loadCues + parseVTTFile + parseVTTEntries)
//     MOVED from internal/application/youtube/subtitles.go;
//   - a process runner for the yt-dlp --write-auto-subs download;
//   - a per-cacheDir store so SliceSubtitles can locate the
//     previously downloaded .vtt for a videoID.
type SubtitleFetcherAdapter struct {
	ytdlpPath string
	cacheDir  string
	langs     string
	runner    ProcessRunnerPort
}

// SubtitleCacheConfig configures the adapter.
type SubtitleCacheConfig struct {
	YTDLPPath    string
	DefaultLangs string
	CacheDir     string
}

// Compile-time assertion: *SubtitleFetcherAdapter satisfies the port.
var _ SubtitleFetcher = (*SubtitleFetcherAdapter)(nil)

// NewSubtitleFetcherAdapter wires the adapter. cacheDir must be set.
func NewSubtitleFetcherAdapter(cfg SubtitleCacheConfig, runner ProcessRunnerPort) *SubtitleFetcherAdapter {
	if runner == nil {
		runner = NewProcessRunnerAdapter()
	}
	if cfg.DefaultLangs == "" {
		cfg.DefaultLangs = "en,en-US"
	}
	return &SubtitleFetcherAdapter{
		ytdlpPath: cfg.YTDLPPath,
		cacheDir:  cfg.CacheDir,
		langs:     cfg.DefaultLangs,
		runner:    runner,
	}
}

// FetchFullVTT downloads the auto-generated transcript for videoURL via
// yt-dlp --write-auto-subs, returns the cached entries as []TimedEntry.
// If no transcript is available the function returns an empty slice
// without error so downstream callers can fall back to Whisper.
func (a *SubtitleFetcherAdapter) FetchFullVTT(ctx context.Context, videoURL string) ([]TimedEntry, error) {
	if a.cacheDir == "" {
		return nil, fmt.Errorf("subtitles: cacheDir is required")
	}
	if videoURL == "" {
		return nil, fmt.Errorf("subtitles: videoURL is required")
	}
	id := extractIDFromURL(videoURL)
	cachedPath := filepath.Join(a.cacheDir, id+".vtt")

	if _, err := os.Stat(cachedPath); err == nil {
		return parseVTTEntries(cachedPath, 0, 0)
	}
	if err := os.MkdirAll(a.cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("subtitles: mkdir cache: %w", err)
	}
	args := []string{
		videoURL,
		"--write-auto-subs",
		"--write-subs",
		"--skip-download",
		"--sub-langs", a.langs,
		"--sub-format", "vtt",
		"--convert-subs", "vtt",
		"--no-warnings",
		"-o", filepath.Join(a.cacheDir, "%(id)s.%(ext)s"),
	}
	// best-effort: no error if yt-dlp can't fetch subs.
	_, _, _ = a.runner.Run(ctx, a.ytdlpPath, args)
	return parseVTTEntries(cachedPath, 0, 0)
}

// SliceSubtitles reads the cached VTT for videoID, applies the rolling
// dedup pass, filters to [startSec, endSec], writes the cleaned text
// to outputPath.
func (a *SubtitleFetcherAdapter) SliceSubtitles(_ context.Context, videoID string, startSec, endSec int, outputPath string) error {
	if a.cacheDir == "" {
		return fmt.Errorf("subtitles: cacheDir is required")
	}
	if outputPath == "" {
		return fmt.Errorf("subtitles: outputPath is required")
	}
	vttPath := filepath.Join(a.cacheDir, videoID+".vtt")
	if _, err := os.Stat(vttPath); err != nil {
		if writeErr := os.WriteFile(outputPath, []byte{}, 0o644); writeErr != nil {
			return fmt.Errorf("subtitles: write empty transcript at %s: %w", outputPath, writeErr)
		}
		if endSec > startSec {
			return fmt.Errorf("subtitles: no cached VTT for %s in %s", videoID, a.cacheDir)
		}
		return nil
	}
	text, err := parseVTTFile(vttPath, float64(startSec), float64(endSec))
	if err != nil {
		return fmt.Errorf("subtitles: parse %s: %w", vttPath, err)
	}
	return os.WriteFile(outputPath, []byte(text), 0o644)
}

// ── vttCue + parsers (MOVED from application/youtube/subtitles.go) ───────

type vttCue struct {
	start float64
	end   float64
	text  string
}

// loadCues reads vttPath, drops the WEBVTT header, parses every cue,
// and returns them filtered to those overlapping [startSec, endSec].
// When startSec == 0 && endSec == 0 the window filter is skipped.
// Dedup + collapse are NOT applied here.
func loadCues(vttPath string, startSec, endSec float64) ([]vttCue, error) {
	f, err := os.Open(vttPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := bufio.NewReader(f)

	// Strip WEBVTT header up to the first blank line.
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}

	// Read remaining lines and split into blocks on blank lines.
	var blocks [][]string
	var cur []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if line != "" {
				cur = append(cur, strings.TrimRight(line, "\n"))
			}
			if len(cur) > 0 {
				blocks = append(blocks, cur)
			}
			break
		}
		t := strings.TrimRight(line, "\n")
		if strings.TrimSpace(t) == "" {
			if len(cur) > 0 {
				blocks = append(blocks, cur)
				cur = cur[:0]
			}
			continue
		}
		cur = append(cur, t)
	}

	timeRegex := regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3})\s*-->\s*(\d{1,2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3})`)

	var cues []vttCue
	for _, block := range blocks {
		var timeLine string
		var textLines []string
		timeSeen := false
		for _, line := range block {
			if !timeSeen && timeRegex.MatchString(line) {
				timeLine = line
				timeSeen = true
				continue
			}
			if timeSeen {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "align:") || strings.HasPrefix(trimmed, "position:") {
					continue
				}
				textLines = append(textLines, line)
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
		if !(startSec == 0 && endSec == 0) {
			if cueEnd <= startSec || cueStart >= endSec {
				continue
			}
		}
		text := textutil.CleanSubtitleText(strings.Join(textLines, " "))
		if text != "" {
			cues = append(cues, vttCue{start: cueStart, end: cueEnd, text: text})
		}
	}
	return cues, nil
}

// parseVTTFile applies the YouTube rolling-cue dedup algorithm on
// loadCues' output and returns the cleaned, concatenated transcript
// text (post-dedup + collapse).
func parseVTTFile(vttPath string, startSec, endSec float64) (string, error) {
	cues, err := loadCues(vttPath, startSec, endSec)
	if err != nil {
		return "", err
	}

	// YouTube rolling-cue dedup.
	var deduped []vttCue
	for i := 0; i < len(cues); i++ {
		longest := cues[i]
		j := i + 1
		for ; j < len(cues); j++ {
			if cues[j].start < longest.end || cues[j].start < longest.start+0.5 {
				if len(cues[j].text) > len(longest.text) {
					longest = cues[j]
				}
				continue
			}
			break
		}
		deduped = append(deduped, longest)
		i = j - 1
	}

	for i := 1; i < len(deduped); i++ {
		deduped[i].text = stripCueOverlap(deduped[i-1].text, deduped[i].text)
	}

	parts := make([]string, 0, len(deduped))
	for _, c := range deduped {
		if c.text != "" {
			parts = append(parts, c.text)
		}
	}
	result := strings.Join(parts, " ")
	result = collapseRepeatedSections(result)
	result = collapseImmediateWordRepetitions(result)
	return result, nil
}

// parseVTTEntries returns the filtered cues as []TimedEntry (structured
// form). Useful when the consumer wants per-cue timing (e.g. search
// window alignment).
func parseVTTEntries(vttPath string, startSec, endSec float64) ([]TimedEntry, error) {
	cues, err := loadCues(vttPath, startSec, endSec)
	if err != nil {
		return nil, err
	}
	out := make([]TimedEntry, 0, len(cues))
	for _, c := range cues {
		out = append(out, TimedEntry{Start: c.start, End: c.end, Text: c.text})
	}
	return out, nil
}

// stripCueOverlap removes the suffix/prefix overlap between consecutive
// cues (YouTube rolling VTT).
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
	if bestMatch == 0 {
		return curr
	}
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

func collapseRepeatedSections(text string) string {
	if len(text) < 20 || !strings.Contains(text, ">>") {
		return text
	}
	parts := strings.Split(text, ">>")
	var deduped []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		last := ""
		if len(deduped) > 0 {
			last = deduped[len(deduped)-1]
		}
		normTrimmed := strings.ToLower(trimmed)
		normLast := strings.ToLower(last)
		switch {
		case strings.Contains(normLast, normTrimmed):
			continue
		case strings.Contains(normTrimmed, normLast) && last != "":
			deduped[len(deduped)-1] = trimmed
			continue
		case normTrimmed != normLast:
			deduped = append(deduped, trimmed)
		}
	}
	return strings.Join(deduped, " >> ")
}

func collapseImmediateWordRepetitions(text string) string {
	if len(text) < 5 {
		return text
	}
	isWordChar := func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	type token struct {
		text   string
		isWord bool
	}
	var tokens []token
	var current strings.Builder
	inWord := false

	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		charIsWord := isWordChar(r)
		if !charIsWord && (r == '-' || r == '\'') && i > 0 && i+1 < len(runes) &&
			isWordChar(runes[i-1]) && isWordChar(runes[i+1]) {
			charIsWord = true
		}
		if charIsWord {
			if !inWord && current.Len() > 0 {
				tokens = append(tokens, token{text: current.String(), isWord: false})
				current.Reset()
			}
			inWord = true
		} else {
			if inWord && current.Len() > 0 {
				tokens = append(tokens, token{text: current.String(), isWord: true})
				current.Reset()
			}
			inWord = false
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		tokens = append(tokens, token{text: current.String(), isWord: inWord})
	}
	for {
		changed := false
		var newTokens []token
		for i := 0; i < len(tokens); i++ {
			if tokens[i].isWord && i+2 < len(tokens) && !tokens[i+1].isWord && tokens[i+2].isWord {
				tokensBetween := tokens[i+1].text
				onlySpace := true
				for _, r := range tokensBetween {
					if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
						onlySpace = false
						break
					}
				}
				if onlySpace && strings.EqualFold(tokens[i].text, tokens[i+2].text) {
					newTokens = append(newTokens, tokens[i])
					i += 2
					changed = true
					continue
				}
			}
			newTokens = append(newTokens, tokens[i])
		}
		tokens = newTokens
		if !changed {
			break
		}
	}
	var sb strings.Builder
	for _, t := range tokens {
		sb.WriteString(t.text)
	}
	return sb.String()
}
