package youtube

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// sliceSubtitles delegates to SubtitleFetcherPort for downloading and slicing VTT subtitles.
func (s *Service) sliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error {
	if s.subtitleFetcher == nil {
		return fmt.Errorf("youtube: subtitle fetcher not wired")
	}
	return s.subtitleFetcher.SliceSubtitles(ctx, videoID, startSec, endSec, outputPath)
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

	for i := 1; i < len(dedupedCues); i++ {
		dedupedCues[i].text = stripCueOverlap(dedupedCues[i-1].text, dedupedCues[i].text)
	}

	var parts []string
	for _, c := range dedupedCues {
		if c.text != "" {
			parts = append(parts, c.text)
		}
	}
	result := strings.Join(parts, " ")

	result = collapseRepeatedSections(result)
	result = collapseImmediateWordRepetitions(result)

	return result, nil
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
		normalizedTrimmed := strings.ToLower(trimmed)
		normalizedLast := strings.ToLower(last)
		if strings.Contains(normalizedLast, normalizedTrimmed) {
			continue
		}
		if strings.Contains(normalizedTrimmed, normalizedLast) {
			deduped[len(deduped)-1] = trimmed
			continue
		}
		if normalizedTrimmed != normalizedLast {
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
