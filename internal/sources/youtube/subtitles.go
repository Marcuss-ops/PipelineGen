package youtube

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"

	"go.uber.org/zap"
)

// sliceSubtitles downloads VTT subtitles for the video ID and extracts text matching the clip window.
func (s *Service) sliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error {
	tempDir, err := os.MkdirTemp("", "yt_subs_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	subPrefix := filepath.Join(tempDir, "subs")

	ytdlpPath := s.cfg.External.ResolvedYtdlpPath()
	cookiesPath := s.cfg.External.YouTubeCookiesPath
	if cookiesPath == "" {
		cookiesPath = "config/youtube_cookies.txt"
	}

	// We run yt-dlp to write both automatic and manual subtitles (EN and IT)
	args := []string{
		"--write-auto-subs", "--write-subs", "--skip-download",
		"--sub-langs", "en,it", "--sub-format", "vtt",
		"--no-warnings",
	}
	if _, err := os.Stat(cookiesPath); err == nil {
		args = append(args, "--cookies", cookiesPath)
	}
	if jsRuntime := s.cfg.External.YouTubeJSRuntimePath; jsRuntime != "" {
		args = append(args, "--js-runtime", jsRuntime)
		args = append(args, "--remote-components", "ejs:github")
	}
	args = append(args, "-o", subPrefix, fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID))
	cmd := exec.CommandContext(ctx, ytdlpPath, args...)

	s.log.Info("Downloading subtitles for slicing", zap.String("video_id", videoID))
	if err := cmd.Run(); err != nil {
		s.log.Warn("Failed to download all subtitles for video (some might still have downloaded)", zap.String("video_id", videoID), zap.Error(err))
	}

	// Scan tempDir for VTT files
	files, err := os.ReadDir(tempDir)
	if err != nil {
		return err
	}

	var vttPath string
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "subs.") && strings.HasSuffix(f.Name(), ".vtt") {
			vttPath = filepath.Join(tempDir, f.Name())
			break
		}
	}

	if vttPath == "" {
		return fmt.Errorf("no subtitles file found for video %s", videoID)
	}

	s.log.Info("Parsing subtitle VTT file", zap.String("path", vttPath))
	transcript, err := parseVTTFile(vttPath, float64(startSec), float64(endSec))
	if err != nil {
		return err
	}

	if transcript == "" {
		return fmt.Errorf("no subtitles found in the specified time window %d-%d", startSec, endSec)
	}

	// Write transcript text file
	txtPath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + ".txt"
	if err := os.WriteFile(txtPath, []byte(transcript), 0644); err != nil {
		return fmt.Errorf("failed to write transcription text file: %w", err)
	}

	s.log.Info("Successfully wrote sliced subtitles to text file", zap.String("path", txtPath))
	return nil
}

// VTT cue with parsed text and timing
type vttCue struct {
	start float64
	end   float64
	text  string
}

func parseVTTFile(vttPath string, startSec, endSec float64) (string, error) {
	data, err := os.ReadFile(vttPath)
	if err != nil {
		return "", err
	}

	content := string(data)
	// Remove VTT header and metadata blocks
	content = regexp.MustCompile(`(?s)^WEBVTT.*?\n\n`).ReplaceAllString(content, "")

	// Split by double newline to get VTT blocks/cues
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

		// Use OVERLAP instead of strict containment — include any cue that overlaps
		// the clip window, even partially. This ensures no spoken content is lost
		// at clip boundaries (e.g., a cue starting at 59.9s for a 60s clip).
		if cueEnd > startSec && cueStart < endSec {
			text := textutil.CleanSubtitleText(strings.Join(textLines, " "))
			if text != "" {
				cues = append(cues, vttCue{
					start: cueStart,
					end:   cueEnd,
					text:  text,
				})
			}
		}
	}

	// ── Handle YouTube's "rolling" VTT format ────────────────────────────
	// YouTube auto-generated VTT has a distinctive pattern:
	//   Cue A (short "trigger"):   00:01:00.389 --> 00:01:00.399  "text1"
	//   Cue B (content):           00:01:00.399 --> 00:01:02.310  "text1 text2"
	//   Cue C (short "trigger"):   00:01:02.310 --> 00:01:02.320  "text2"
	//   Cue D (content):           00:01:02.320 --> 00:01:04.630  "text2 text3"
	//
	// Each content cue INCLUDES the text from the previous trigger cue + new text.
	// This creates "rolling" text that repeats across successive cues.
	//
	// Solution: group overlapping cues and keep only the LONGEST text in each group.
	// Since content cues are always longer (more text) than trigger cues,
	// this effectively keeps only the content cues, removing duplicates.
	var dedupedCues []vttCue
	for i := 0; i < len(cues); i++ {
		// Find the longest text among overlapping cues
		longest := cues[i]
		for j := i + 1; j < len(cues); j++ {
			if cues[j].start < longest.end || cues[j].start < longest.start+0.5 {
				// Overlapping — pick the longer text
				if len(cues[j].text) > len(longest.text) {
					longest = cues[j]
				}
				i = j // skip the overlapping cue
			} else {
				break
			}
		}
		dedupedCues = append(dedupedCues, longest)
	}

	// ── Strip suffix-prefix overlap between content cues ────────────────
	// After removing trigger cues, consecutive content cues still overlap:
	//   Content B: "going to be asking why is the smartest man in the"
	//   Content D: "the smartest man in the asking why"
	//             → "asking why" (overlap stripped)
	//
	// This happens because each content cue includes the PREVIOUS trigger
	// cue's text, which was already part of the previous content cue's tail.
	for i := 1; i < len(dedupedCues); i++ {
		dedupedCues[i].text = stripCueOverlap(dedupedCues[i-1].text, dedupedCues[i].text)
	}

	// Build transcript from deduped cues
	var parts []string
	for _, c := range dedupedCues {
		if c.text != "" {
			parts = append(parts, c.text)
		}
	}
	result := strings.Join(parts, " ")

	// Remove repeated >> sections ("text. >> text. >> text." → "text. >> text.")
	result = collapseRepeatedSections(result)

	// ── Final pass: collapse immediate word repetitions ───────────────────
	// After all dedup passes, there may still be isolated within-cue
	// repetitions like "30-day 30-day challenges" → "30-day challenges" or
	// "challenge challenge I" → "challenge I".
	// These are artifact of YouTube's auto-sub generator, not natural speech.
	// Natural repeated words in speech are separated by other text.
	result = collapseImmediateWordRepetitions(result)

	return result, nil
}

// stripCueOverlap removes the overlapping suffix-prefix text between
// consecutive deduped cues from YouTube's rolling VTT format.
//
// After removing trigger cues (short cues that are subsets of content cues),
// consecutive content cues still overlap. For example:
//
//	Content B: "I know people are going to be asking why"
//	Content D: "asking why is the smartest man in the world"
//	          → "is the smartest man in the world" ("asking why" stripped)
//
// Uses WORD-LEVEL matching with a minimum of 2 overlapping words.
// This catches all rolling-cue overlaps (even short ones like "30-day")
// while safely ignoring single-word coincidences in speech.
func stripCueOverlap(prev, curr string) string {
	if prev == "" || curr == "" {
		return curr
	}

	prevWords := strings.Fields(strings.ToLower(prev))
	currWords := strings.Fields(strings.ToLower(curr))

	if len(prevWords) == 0 || len(currWords) == 0 {
		return curr
	}

	// Find the longest suffix of prevWords that matches a prefix of currWords.
	// Minimum 2 words to avoid stripping single-word coincidences ("the", "I", "and").
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
		// Reconstruct curr without the overlapping words,
		// preserving original (non-lowercased) text from curr.
		origFields := strings.Fields(curr)
		if bestMatch >= len(origFields) {
			return curr // Overlap covers everything, keep original
		}
		stripped := strings.Join(origFields[bestMatch:], " ")
		if stripped == "" {
			return curr
		}
		return stripped
	}
	return curr
}

// collapseRepeatedSections removes duplicate >>-delimited sections from VTT text.
// After the rolling-cue dedup, there may still be residual repetitions where
// a content cue's trailing text matches the next trigger cue (e.g.,
// "text1 >> text2 >> text2 text3" → "text1 >> text2 text3").
// collapseImmediateWordRepetitions removes consecutive duplicate words from VTT transcript.
// YouTube auto-generated subtitles sometimes produce within-cue word stutters like
// "30-day 30-day challenges" or "challenge challenge I" where a word is immediately
// repeated. This is an artifact of the subtitle generator, not natural speech.
// Natural repeated words ("no no", "it's it's", "yeah yeah") always have the same
// word immediately adjacent, so this regex removes ALL immediate duplicates.
// The risk of removing legitimately duplicated words is negligible in practice
// because repeated words in speech either have a pause (period/comma) or are
// intentional emphasis (which should be preserved and the user can re-add).
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
		charIsWord := false
		if isWordChar(r) {
			charIsWord = true
		} else if (r == '-' || r == '\'') && i > 0 && i+1 < len(runes) && isWordChar(runes[i-1]) && isWordChar(runes[i+1]) {
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
				isOnlySpace := true
				for _, r := range tokens[i+1].text {
					if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
						isOnlySpace = false
						break
					}
				}
				if isOnlySpace && strings.ToLower(tokens[i].text) == strings.ToLower(tokens[i+2].text) {
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
		// Check if current text is a SUBSET of the previous (contained within it)
		// This handles: "text2" vs "text2 text3" → keep the longer one
		normalizedTrimmed := strings.ToLower(trimmed)
		normalizedLast := strings.ToLower(last)
		if strings.Contains(normalizedLast, normalizedTrimmed) {
			// Current text is subset of previous — skip it
			continue
		}
		if strings.Contains(normalizedTrimmed, normalizedLast) {
			// Previous text is subset of current — replace with current
			deduped[len(deduped)-1] = trimmed
			continue
		}
		if normalizedTrimmed != normalizedLast {
			deduped = append(deduped, trimmed)
		}
	}
	return strings.Join(deduped, " >> ")
}
