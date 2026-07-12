// Package youtube — subtitles_parse.go is the VTT-parser leaf of the
// 5-file subtitle split. It owns ONLY the VTT parsing + cue
// timestamp extraction + rolling-cue dedup heuristics; no language
// normalization, no download logic, no priority-chain decision.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The VTT timestamp parser is in pkg/textutil::ParseVTTTimestamp.
//     This leaf DELEGATES to it so the canonical regex / float
//     conversion is preserved verbatim across subtitle consumers.
//   - The text cleanup heuristic (lowercase split + overlap strip)
//     is local; cross-file noise like `collapseImmediateWordRepet`
//     lives in subtitles_normalize.go so this leaf stays focused on
//     the cue-level structural parse.
//
// The parser is intentionally tolerant: malformed cues are kept out
// of the returned slice (no typed error per cue) so a single bad
// cue does not poison the whole batch. The facade's FetchSegmentSubtitles
// is the canonical owner of the per-cue diagnostic; this leaf stays
// text-only.
package youtube

import (
	"bufio"
	"os"
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// vttCue is the internal cue representation: timestamp pair + the
// raw text after `textutil.CleanSubtitleText`. The parser stays
// in this struct shape; the facade converts vttCue → []TimedEntry
// (per-cue) and []string (concatenated plain) just-in-time.
type vttCue struct {
	start float64
	end   float64
	text  string
}

// loadCues reads vttPath, drops the WEBVTT header, parses every
// cue, and returns them filtered to those overlapping [startSec,
// endSec]. When startSec == 0 && endSec == 0 the window filter is
// skipped. Dedup + collapse are NOT applied here.
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
				cur = nil // force new backing array on next append (avoid shared-memory overwrite)
			}
			continue
		}
		cur = append(cur, t)
	}

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

// ParseVTTFile applies the YouTube rolling-cue dedup algorithm on
// loadCues' output and returns the cleaned, concatenated transcript
// text (post-dedup + collapse). Exported because the slice-only
// consumer path (legacy pre-Fase-2 cut) and the per-segment
// asset_text_track_segments consumer (Fase 2) both reuse it.
//
// godlike/06 SSOT: cross-file heuristic helpers
// (collapseRepeatedSections, collapseImmediateWordRepetitions) live
// in subtitles_normalize.go so this leaf stays focused on the cue-
// level structural parse.
func ParseVTTFile(vttPath string, startSec, endSec float64) (string, error) {
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

// ParseVTTEntries returns the filtered cues as []TimedEntry
// (structured form). Useful when the consumer wants per-cue timing
// (e.g. search window alignment). Exported as of
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.a so FetchSegmentSubtitles
// and Fase 2 (asset_text_track_segments) callers can reuse the
// canonical parser.
func ParseVTTEntries(vttPath string, startSec, endSec float64) ([]TimedEntry, error) {
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

// stripCueOverlap removes the suffix/prefix overlap between
// consecutive cues (YouTube rolling VTT). Local to this leaf.
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
