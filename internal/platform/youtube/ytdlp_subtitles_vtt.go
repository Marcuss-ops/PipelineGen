// Package youtube — ytdlp_subtitles_vtt.go (PR-SPLIT-YTDLP-SUBTITLES, July 2026).
//
// Owns the 4 VTT parser helpers (stripVTTHeader + parseVTTBlock +
// parseTimestampSeconds + stripXMLTags). Extracted from the former
// application adapter per godlike/06 SSOT one-canonical-owner-per-fact:
// this file is the SOLE canonical owner of the WebVTT parsing surface.
//
// Sibling files in the ytdlp_subtitles family (post-split canonical layout):
//   - ytdlp_subtitles.go — Deps, adapter construction, and plain-text helpers
//   - ytdlp_subtitles_fetch.go — document assembly and timeout ownership
//   - ytdlp_subtitles_exec.go — filesystem/process execution
//   - ytdlp_subtitles_vtt.go (this file) — the 4 VTT parser helpers.
package youtube

import (
	"fmt"
	"strings"
)

// stripVTTHeader removes everything before the first blank line after
// the WEBVTT marker. Pre-Step-9 lived in monitor/vtt_helpers.go as
// regexRemoveVTTHeader; migrated here unchanged.
func stripVTTHeader(content string) string {
	if idx := strings.Index(content, "\n\n"); idx > 0 {
		before := strings.TrimSpace(content[:idx])
		if strings.HasPrefix(before, "WEBVTT") {
			return content[idx+2:]
		}
	}
	return content
}

// parseVTTBlock parses one VTT block (timestamp line + text lines) into
// (start_seconds, end_seconds, joined_text, ok). Returns ok=false on
// malformed blocks (no timestamp line, no text, parse failure on the
// timestamps). align:/position: lines are stripped from the text side.
func parseVTTBlock(block string) (start, end float64, text string, ok bool) {
	lines := strings.Split(block, "\n")
	var timeLine string
	var textLines []string
	for _, line := range lines {
		if strings.Contains(line, "-->") {
			timeLine = line
		} else if timeLine != "" {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "align:") || strings.HasPrefix(line, "position:") {
				continue
			}
			textLines = append(textLines, stripXMLTags(line))
		}
	}
	if timeLine == "" || len(textLines) == 0 {
		return 0, 0, "", false
	}
	// Parse the two timestamps from "HH:MM:SS.mmm --> HH:MM:SS.mmm".
	parts := strings.Split(timeLine, "-->")
	if len(parts) < 2 {
		return 0, 0, "", false
	}
	// youtube.com auto-subs VTT appends align:/position: style info
	// after the end timestamp (e.g. "00:00:04.309 align:start position:0%").
	// strings.Fields[0] isolates the bare timestamp before the parser.
	startFields := strings.Fields(parts[0])
	endFields := strings.Fields(parts[1])
	if len(startFields) == 0 || len(endFields) == 0 {
		return 0, 0, "", false
	}
	start = parseTimestampSeconds(startFields[0])
	end = parseTimestampSeconds(endFields[0])
	if end <= start {
		return 0, 0, "", false
	}
	return start, end, strings.Join(textLines, " "), true
}

// parseTimestampSeconds parses "HH:MM:SS.mmm" / "MM:SS.mmm" / "SS" into
// float64 seconds. Mirrors pkg/textutil.ParseVTTTimestamp.
func parseTimestampSeconds(ts string) float64 {
	ts = strings.TrimSpace(ts)
	parts := strings.Split(ts, ":")
	if len(parts) == 3 {
		var h, m, s float64
		fmt.Sscanf(parts[0], "%f", &h)
		fmt.Sscanf(parts[1], "%f", &m)
		fmt.Sscanf(parts[2], "%f", &s)
		return h*3600 + m*60 + s
	}
	if len(parts) == 2 {
		var m, s float64
		fmt.Sscanf(parts[0], "%f", &m)
		fmt.Sscanf(parts[1], "%f", &s)
		return m*60 + s
	}
	var s float64
	fmt.Sscanf(ts, "%f", &s)
	return s
}

// stripXMLTags removes HTML/XML tag delimiters from a string via the
// per-rune scanner (handles inline `<c>` / `<i>` VTT cue styling).
func stripXMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				result.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(result.String())
}
