// Package youtube — subtitles_normalize.go is the canonical
// normalization leaf of the 5-file subtitle split. It owns:
//   - BCP-47 language normalization for the configured langs CSV
//     (delegates to internal/domain/asset.Normalize, the SSOT);
//   - the VTT timestamp regex (the canonical time-pattern used by
//     subtitles_parse.go::loadCues);
//   - the cross-file text-collapse heuristics
//     (collapseRepeatedSections + collapseImmediateWordRepetitions)
//     that ParseVTTFile stitches together.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The strict BCP-47 normalization rule lives in
//     internal/kernel/asset/bcp47.go::Normalize — the leaf
//     DELEGATES verbatim so underscore-separators like "pt_BR"
//     propagate the rejection (Fase 1.b "Rifiutare varianti miste
//     tipo pt_br" hard contract).
//   - Per-cue text cleaning delegates to pkg/textutil::CleanSubtitleText
//     so the canonical lowercasing + dedup heuristic is shared
//     across subtitle consumers.
//
// godlike/07 no-fake-availability invariant: empty langs CSV
// collapses to BCP-47 "und" — the infra NEVER substitutes "en"
// for an empty input (the orchestrator at the application layer
// is the SOLE canonical site for "und"-on-empty decisions).
package youtube

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// timeRegex is the canonical VTT cue-time pattern used by
// subtitles_parse.go::loadCues. Defined at package-level in this
// leaf so the regex compilation is shared across calls.
var timeRegex = regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3})\s*-->\s*(\d{1,2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3})`)

// normalizeSubtitleLanguage walks the configured langs CSV (comma-
// separated), BCP-47-normalizes each entry, and returns the first
// entry that resolves to a REAL language (NOT "und"). Empty CSV
// or all-malformed entries collapse to "und" per godlike/07
// no-fake-availability (NEVER silently default to "en").
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): the
// assignment is wrapped in asset.Normalize to enforce strict
// BCP-47 (reject underscore separators like "pt_BR", "en_US").
// Without this wrapper, a malformed CSV entry would propagate to
// the ResolvedTextBundle.LanguageCode and cause
// TextTrackResolver.AcquireSegmentText → languageInList to call
// Normalize (which now rejects underscores) → silently discard
// the valid subtitle track and fall through to Whisper.
func normalizeSubtitleLanguage(langs string) string {
	lang := ""
	if langs != "" {
		for _, l := range strings.Split(langs, ",") {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			// Normalize to enforce strict BCP-47. A malformed
			// entry (e.g. "pt_BR") is rejected and skipped; the
			// loop advances to the next CSV entry.
			norm, nErr := asset.Normalize(l)
			if nErr != nil || norm == "und" {
				// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July
				// 2026): operator-visible Warn to stderr for
				// malformed BCP-47 entries (e.g. "pt_BR" with
				// underscore). The loop advances to the next
				// CSV entry. Future long-term refactor: add a
				// *zap.Logger field to SubtitleFetcherAdapter
				// to match the convention used by
				// TextTrackResolver and other adapters.
				fmt.Fprintf(os.Stderr, "subtitles: skipping malformed BCP-47 entry %q (err=%v, use hyphen not underscore); falling through to next CSV entry\n", l, nErr)
				continue
			}
			lang = norm
			break
		}
	}
	if lang == "" {
		lang = "und"
	}
	return lang
}

// collapseRepeatedSections collapses repeated sections marked with
// ">>" — a YouTube-specific dedup heuristic when the same line
// appears in adjacent windows. Cross-file helper consumed by
// subtitles_parse.go::ParseVTTFile.
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

// collapseImmediateWordRepetitions collapses adjacent identical
// words separated only by whitespace (e.g. "the the" → "the").
// Cross-file helper consumed by subtitles_parse.go::ParseVTTFile.
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
