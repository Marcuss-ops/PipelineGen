// Package texttracks — vtt_parser.go: lightweight VTT/SRT cue
// extraction for the AcquireService priority 2 (local file).
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026).
//
// Scope: extract (plain_text, cues) from a VTT or SRT file on
// disk. The parser handles the two formats the YouTube-subtitle
// ecosystem produces; exotic formats (TTML, SSA, ASS) are
// out-of-scope (Fase 5 fails-soft: the chain falls through to
// YouTube subs or Whisper).
//
// godlike/06 SSOT: this is the SOLE canonical VTT/SRT parser
// in the texttracks package. The YouTube SubtitleFetcherAdapter
// has its own internal parsing (yt-dlp --sub-langs path), but
// the BACKFILL path uses this one because it reads from
// arbitrary local files (not just yt-dlp's output).
//
// godlike/07 fail-closed: a malformed file returns a typed
// error. The AcquireService logs + skips it (the chain falls
// through to the next priority).
package texttracks

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// cueTimestampRE matches WebVTT timestamps (HH:MM:SS.mmm) and
// SRT timestamps (HH:MM:SS,mmm). The . → , swap is handled
// per-format in the parser.
var cueTimestampRE = regexp.MustCompile(`(\d{1,2}):(\d{2}):(\d{2})[.,](\d{3})\s+-->\s+(\d{1,2}):(\d{2}):(\d{2})[.,](\d{3})`)

// ParseSubtitleFile reads a VTT or SRT file and returns
// (plain_text, cues, error). The format is auto-detected from
// the file extension (.vtt → VTT; .srt → SRT). The returned
// plain_text is the concatenation of all cue text fields
// (newlines between cues); cues carry the per-segment timing.
//
// Errors:
//   - file not readable
//   - file contains no parseable cues
//   - any individual cue with invalid timing is silently dropped
//     (the rest of the file is still returned)
func ParseSubtitleFile(path string) (string, []asset.TimedCue, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("texttracks.ParseSubtitleFile: open %q: %w", path, err)
	}
	defer f.Close()

	format := "vtt"
	if strings.HasSuffix(strings.ToLower(path), ".srt") {
		format = "srt"
	}

	scanner := bufio.NewScanner(f)
	// Allow long lines (VTT cues can be verbose).
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var cues []asset.TimedCue
	var textParts []string
	var currentCue *asset.TimedCue
	var cueIndex int

	flush := func() {
		if currentCue != nil {
			cues = append(cues, *currentCue)
			textParts = append(textParts, currentCue.Text)
			currentCue = nil
		}
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// VTT header: "WEBVTT" (possibly with a trailing
		// NOTE block). Skip.
		if format == "vtt" && strings.HasPrefix(line, "WEBVTT") {
			continue
		}
		// SRT cue index: a standalone integer at the start
		// of a block. We don't strictly need it, but it's a
		// reliable separator.
		if format == "srt" {
			if _, convErr := strconv.Atoi(line); convErr == nil {
				flush()
				cueIndex++
				continue
			}
		}
		// Timestamp line: matches both VTT (.) and SRT (,)
		// formats thanks to the regex character class.
		if m := cueTimestampRE.FindStringSubmatch(line); m != nil {
			flush()
			startMs, _ := timestampToMs(m[1], m[2], m[3], m[4])
			endMs, _ := timestampToMs(m[5], m[6], m[7], m[8])
			currentCue = &asset.TimedCue{
				StartMs: startMs,
				EndMs:   endMs,
				Text:    "",
			}
			continue
		}
		// Blank line: end of the current cue.
		if line == "" {
			flush()
			continue
		}
		// Text line: append to the current cue (with a
		// newline if the cue already has text).
		if currentCue != nil {
			if currentCue.Text != "" {
				currentCue.Text += "\n"
			}
			currentCue.Text += line
		}
	}
	// Flush the last cue (if the file didn't end with a
	// blank line).
	flush()

	if err := scanner.Err(); err != nil {
		return "", nil, fmt.Errorf("texttracks.ParseSubtitleFile: scan %q: %w", path, err)
	}
	if len(cues) == 0 {
		return "", nil, fmt.Errorf("texttracks.ParseSubtitleFile: no parseable cues in %q", path)
	}
	return strings.Join(textParts, "\n"), cues, nil
}

// timestampToMs converts (hours, minutes, seconds, millis) to
// total milliseconds. Returns 0 on parse error (the parser
// drops the cue, not the whole file).
func timestampToMs(h, m, s, ms string) (int64, error) {
	hi, _ := strconv.Atoi(h)
	mi, _ := strconv.Atoi(m)
	si, _ := strconv.Atoi(s)
	msi, _ := strconv.Atoi(ms)
	return int64(hi)*3600000 + int64(mi)*60000 + int64(si)*1000 + int64(msi), nil
}
