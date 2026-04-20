package clipsearch

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"velox/go-master/internal/nlp"
)

const ytDLPFieldDelimiter = "\x1f"

var ytStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "this": true, "that": true,
	"interview": true, "highlights": true, "highlight": true, "official": true, "video": true,
	"short": true, "clip": true, "boxing": true, "fight": true,
}

func (d *ClipDownloader) selectRankedYouTubeCandidates(ctx context.Context, keyword string, authArgs []string) ([]*YouTubeClipMetadata, error) {
	queries := []string{
		keyword,
		keyword + " interview",
		keyword + " highlights",
	}
	candidates := make([]*YouTubeClipMetadata, 0, 40)
	seen := make(map[string]bool)
	for _, q := range queries {
		found, err := d.searchYouTubeCandidates(ctx, q, authArgs)
		if err != nil {
			continue
		}
		for _, c := range found {
			if c == nil || strings.TrimSpace(c.VideoID) == "" {
				continue
			}
			if seen[c.VideoID] {
				continue
			}
			seen[c.VideoID] = true
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no youtube candidates found for keyword %q", keyword)
	}

	tokens := keywordSearchTokens(keyword)
	filtered := make([]*YouTubeClipMetadata, 0, len(candidates))
	for _, c := range candidates {
		score := scoreYouTubeCandidate(c, keyword, tokens)
		c.Relevance = score
		if passesYouTubeHardFilter(c, keyword, tokens, d.ytMaxAgeDays) {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no youtube candidates passed relevance filter for %q", keyword)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Relevance == filtered[j].Relevance {
			return filtered[i].ViewCount > filtered[j].ViewCount
		}
		return filtered[i].Relevance > filtered[j].Relevance
	})
	return filtered, nil
}

func (d *ClipDownloader) searchYouTubeCandidates(ctx context.Context, query string, authArgs []string) ([]*YouTubeClipMetadata, error) {
	args := []string{
		"--flat-playlist",
		"--ignore-errors",
		"--no-warnings",
		"--print", "%(id)s\x1f%(title)s\x1f%(channel)s\x1f%(uploader)s\x1f%(view_count)s\x1f%(duration)s\x1f%(upload_date)s\x1f%(description)s",
		fmt.Sprintf("ytsearch25:%s", query),
	}
	if d.ytMaxAgeDays > 0 {
		dateAfter := time.Now().UTC().AddDate(0, 0, -d.ytMaxAgeDays).Format("20060102")
		queryArg := args[len(args)-1]
		args = append(args[:len(args)-1], "--dateafter", dateAfter, queryArg)
	}
	if len(authArgs) > 0 {
		base := append([]string{}, args[:len(args)-1]...)
		base = append(base, authArgs...)
		base = append(base, args[len(args)-1])
		args = base
	}
	cmd := exec.CommandContext(ctx, d.ytDlpPath, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")
	results := make([]*YouTubeClipMetadata, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ytDLPFieldDelimiter) {
			continue
		}
		fields := strings.Split(line, ytDLPFieldDelimiter)
		if len(fields) < 7 {
			continue
		}
		videoID := strings.TrimSpace(fields[0])
		if videoID == "" {
			continue
		}
		desc := ""
		if len(fields) >= 8 {
			desc = strings.TrimSpace(strings.Join(fields[7:], ytDLPFieldDelimiter))
		}
		viewCount, _ := strconv.ParseInt(strings.TrimSpace(fields[4]), 10, 64)
		durVal, _ := strconv.ParseFloat(strings.TrimSpace(fields[5]), 64)
		meta := &YouTubeClipMetadata{
			VideoID:     videoID,
			VideoURL:    "https://www.youtube.com/watch?v=" + videoID,
			Title:       strings.TrimSpace(fields[1]),
			Channel:     strings.TrimSpace(fields[2]),
			Uploader:    strings.TrimSpace(fields[3]),
			ViewCount:   viewCount,
			DurationSec: durVal,
			UploadDate:  normalizeYtUploadDate(strings.TrimSpace(fields[6])),
			Description: desc,
			SearchQuery: query,
		}
		results = append(results, meta)
	}
	return results, nil
}

func (d *ClipDownloader) downloadYouTubeVideoByURL(ctx context.Context, keyword, outputDir, videoURL, videoID string, authArgs []string) (string, error) {
	baseName := fmt.Sprintf("dynamic_%s_%s", sanitizeFilename(keyword), sanitizeFilename(videoID))
	outputPattern := filepath.Join(outputDir, baseName+".%(ext)s")
	args := []string{
		"--ignore-errors",
		"--no-abort-on-error",
		"--format", "bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
		"--output", outputPattern,
		"--no-playlist",
		"--match-filter", "duration >= 4 & duration < 600",
		videoURL,
	}
	if len(authArgs) > 0 {
		base := append([]string{}, args[:len(args)-1]...)
		base = append(base, authArgs...)
		base = append(base, args[len(args)-1])
		args = base
	}

	cmd := exec.CommandContext(ctx, d.ytDlpPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	files, globErr := filepath.Glob(filepath.Join(outputDir, baseName+".*"))
	if globErr == nil && len(files) > 0 {
		return pickVideoCandidate(files), nil
	}
	if runErr != nil {
		return "", fmt.Errorf("download selected candidate failed: %w (%s)", runErr, strings.TrimSpace(stderr.String()))
	}
	return "", fmt.Errorf("download completed but produced no file")
}

func (d *ClipDownloader) fetchYouTubeTranscript(ctx context.Context, outputDir, videoURL, videoID string, authArgs []string) (string, string, []TranscriptSegment) {
	subsDir := filepath.Join(outputDir, "subs")
	if err := os.MkdirAll(subsDir, 0755); err != nil {
		return "", "", nil
	}
	outputPattern := filepath.Join(subsDir, sanitizeFilename(videoID))
	args := []string{
		"--skip-download",
		"--write-auto-subs",
		"--write-subs",
		"--sub-langs", "en.*,en,-live_chat",
		"--sub-format", "vtt",
		"--output", outputPattern,
		"--no-playlist",
		videoURL,
	}
	if len(authArgs) > 0 {
		base := append([]string{}, args[:len(args)-1]...)
		base = append(base, authArgs...)
		base = append(base, args[len(args)-1])
		args = base
	}
	cmd := exec.CommandContext(ctx, d.ytDlpPath, args...)
	_ = cmd.Run()

	matches, err := filepath.Glob(filepath.Join(subsDir, sanitizeFilename(videoID)+"*.vtt"))
	if err != nil || len(matches) == 0 {
		return "", "", nil
	}
	bestVTT := matches[0]
	data, err := os.ReadFile(bestVTT)
	if err != nil {
		return "", "", nil
	}
	segments := parseTranscriptSegmentsFromVTT(string(data))
	return normalizeTranscriptFromVTT(string(data)), bestVTT, segments
}

func keywordSearchTokens(keyword string) []string {
	n := strings.ToLower(strings.TrimSpace(keyword))
	n = nonWordRe.ReplaceAllString(n, " ")
	parts := strings.Fields(n)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		if ytStopwords[p] {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 && len(parts) > 0 {
		return parts
	}
	return out
}

func scoreYouTubeCandidate(c *YouTubeClipMetadata, keyword string, tokens []string) int {
	if c == nil {
		return -999
	}
	title := strings.ToLower(strings.TrimSpace(c.Title))
	chanBlob := strings.ToLower(strings.TrimSpace(c.Channel + " " + c.Uploader))
	desc := strings.ToLower(strings.TrimSpace(c.Description))
	fullBlob := strings.Join([]string{title, chanBlob, desc}, " ")
	score := 0

	kwNorm := strings.Join(strings.Fields(strings.ToLower(keyword)), " ")
	if kwNorm != "" && strings.Contains(fullBlob, kwNorm) {
		score += 60
	}
	hits := 0
	for _, t := range tokens {
		if t == "" {
			continue
		}
		if strings.Contains(title, t) {
			score += 25
			hits++
			continue
		}
		if strings.Contains(chanBlob, t) {
			score += 14
			hits++
			continue
		}
		if strings.Contains(desc, t) {
			score += 8
			hits++
		}
	}
	if len(tokens) > 0 {
		score += int(10.0 * float64(hits) / float64(len(tokens)))
	}

	if c.ViewCount > 0 {
		score += int(math.Min(25.0, math.Log10(float64(c.ViewCount)+1)*6.0))
	}
	if looksRecent(c.UploadDate) {
		score += 5
	}
	if c.DurationSec >= 8 && c.DurationSec <= 420 {
		score += 8
	}
	return score
}

func passesYouTubeHardFilter(c *YouTubeClipMetadata, keyword string, tokens []string, maxAgeDays int) bool {
	if c == nil {
		return false
	}
	if d := strings.TrimSpace(c.UploadDate); d != "" && isOlderThanDays(d, maxAgeDays) {
		return false
	}
	fullBlob := strings.ToLower(strings.TrimSpace(c.Title + " " + c.Channel + " " + c.Uploader + " " + c.Description))
	kwNorm := strings.Join(strings.Fields(strings.ToLower(keyword)), " ")
	if kwNorm != "" && strings.Contains(fullBlob, kwNorm) {
		return true
	}
	if len(tokens) == 0 {
		return true
	}
	matches := 0
	for _, t := range tokens {
		if strings.Contains(fullBlob, t) {
			matches++
		}
	}
	if len(tokens) == 1 {
		return matches >= 1
	}
	return matches >= 2
}

func isOlderThanDays(uploadDate string, days int) bool {
	if days <= 0 {
		return false
	}
	d := strings.TrimSpace(uploadDate)
	if d == "" {
		return false
	}
	parsed, err := time.Parse("2006-01-02", d)
	if err != nil {
		return false
	}
	return time.Since(parsed) > time.Duration(days)*24*time.Hour
}

func normalizeYtUploadDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) != 8 {
		return raw
	}
	return fmt.Sprintf("%s-%s-%s", raw[0:4], raw[4:6], raw[6:8])
}

func looksRecent(uploadDate string) bool {
	d := strings.TrimSpace(uploadDate)
	if d == "" {
		return false
	}
	parsed, err := time.Parse("2006-01-02", d)
	if err != nil {
		return false
	}
	return time.Since(parsed) <= (120 * 24 * time.Hour)
}

var (
	vttTimeLineRe = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d{3}\s+-->\s+\d{2}:\d{2}:\d{2}\.\d{3}`)
	vttTagRe      = regexp.MustCompile(`<[^>]+>`)
)

func normalizeTranscriptFromVTT(vtt string) string {
	lines := strings.Split(vtt, "\n")
	out := make([]string, 0, len(lines))
	seen := make(map[string]bool, len(lines))
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" || s == "WEBVTT" || strings.HasPrefix(s, "Kind:") || strings.HasPrefix(s, "Language:") {
			continue
		}
		if strings.Contains(s, "-->") || vttTimeLineRe.MatchString(s) {
			continue
		}
		if strings.HasPrefix(s, "NOTE") {
			continue
		}
		s = vttTagRe.ReplaceAllString(s, "")
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

func parseTranscriptSegmentsFromVTT(vttRaw string) []TranscriptSegment {
	parsed, err := nlp.ParseVTT(vttRaw)
	if err != nil || parsed == nil || len(parsed.Segments) == 0 {
		return nil
	}
	out := make([]TranscriptSegment, 0, len(parsed.Segments))
	for _, seg := range parsed.Segments {
		text := strings.TrimSpace(vttTagRe.ReplaceAllString(seg.Text, ""))
		if text == "" {
			continue
		}
		out = append(out, TranscriptSegment{
			StartSec: seg.Start,
			EndSec:   seg.End,
			Text:     text,
		})
	}
	return out
}

func buildYTDLPSearchArgVariants(keyword, outputPattern string, authArgs []string) [][]string {
	variants := [][]string{
		{
			"--ignore-errors",
			"--no-abort-on-error",
			"--format", "bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
			"--max-downloads", "1",
			"--match-filter", "duration >= 4 & duration < 90",
			"--output", outputPattern,
			"--no-playlist",
			fmt.Sprintf("ytsearch5:%s stock footage b-roll", keyword),
		},
		{
			"--ignore-errors",
			"--no-abort-on-error",
			"--format", "bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
			"--max-downloads", "1",
			"--match-filter", "duration >= 4 & duration < 180",
			"--output", outputPattern,
			"--no-playlist",
			fmt.Sprintf("ytsearch8:%s highlights interview short", keyword),
		},
		{
			"--ignore-errors",
			"--no-abort-on-error",
			"--format", "bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
			"--max-downloads", "1",
			"--match-filter", "duration >= 4 & duration < 600",
			"--output", outputPattern,
			"--no-playlist",
			fmt.Sprintf("ytsearch12:%s", keyword),
		},
	}
	if len(authArgs) == 0 {
		return variants
	}
	for i := range variants {
		queryArg := variants[i][len(variants[i])-1]
		base := append([]string{}, variants[i][:len(variants[i])-1]...)
		base = append(base, authArgs...)
		base = append(base, queryArg)
		variants[i] = base
	}
	return variants
}

func pickVideoCandidate(files []string) string {
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".mp4" || ext == ".webm" || ext == ".mkv" {
			return f
		}
	}
	return files[0]
}

func ytDLPAuthArgsFromEnv() []string {
	args := make([]string, 0, 10)

	cookiesFile := firstNonEmptyEnv("VELOX_YTDLP_COOKIES_FILE", "YTDLP_COOKIES_FILE")
	if strings.TrimSpace(cookiesFile) == "" {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			candidates := []string{
				filepath.Join(home, "Downloads", "coo1kies.txt"),
				filepath.Join(home, "Downloads", "cookies.txt"),
			}
			for _, candidate := range candidates {
				if _, err := os.Stat(candidate); err == nil {
					cookiesFile = candidate
					break
				}
			}
		}
	}
	if strings.TrimSpace(cookiesFile) != "" {
		args = append(args, "--cookies", strings.TrimSpace(cookiesFile))
	}

	cookiesFromBrowser := firstNonEmptyEnv("VELOX_YTDLP_COOKIES_FROM_BROWSER", "YTDLP_COOKIES_FROM_BROWSER")
	if strings.TrimSpace(cookiesFromBrowser) != "" {
		args = append(args, "--cookies-from-browser", strings.TrimSpace(cookiesFromBrowser))
	}

	extractorArgs := firstNonEmptyEnv("VELOX_YTDLP_EXTRACTOR_ARGS", "YTDLP_EXTRACTOR_ARGS")
	if strings.TrimSpace(extractorArgs) == "" {
		extractorArgs = "youtube:player_client=mweb"
	}
	if strings.TrimSpace(extractorArgs) != "" {
		args = append(args, "--extractor-args", strings.TrimSpace(extractorArgs))
	}

	jsRuntimes := firstNonEmptyEnv("VELOX_YTDLP_JS_RUNTIMES", "YTDLP_JS_RUNTIMES")
	if strings.TrimSpace(jsRuntimes) == "" {
		if _, err := exec.LookPath("node"); err == nil {
			jsRuntimes = "node"
		}
	}
	if strings.TrimSpace(jsRuntimes) != "" {
		args = append(args, "--js-runtimes", strings.TrimSpace(jsRuntimes))
	}

	remoteComponents := firstNonEmptyEnv("VELOX_YTDLP_REMOTE_COMPONENTS", "YTDLP_REMOTE_COMPONENTS")
	if strings.TrimSpace(remoteComponents) == "" {
		remoteComponents = "ejs:github"
	}
	if strings.TrimSpace(remoteComponents) != "" {
		args = append(args, "--remote-components", strings.TrimSpace(remoteComponents))
	}

	return args
}

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}
