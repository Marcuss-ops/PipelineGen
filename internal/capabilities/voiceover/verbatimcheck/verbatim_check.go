// Package verbatimcheck certifies the spoken output of a
// `source_text_verbatim` scene. It is the automated half of AUGRENDER-04: the
// chunking and Markdown sanitizer are already covered at the splitter and
// provider boundary, but nothing proved that the produced AUDIO contains the
// authored words without the formatting that surrounds them.
//
// The check consumes a transcript (WebVTT word/utterance cues from the TTS
// aligner or a Whisper pass) plus the authored source text and the audio
// duration, and reports:
//
//   - Markdown markers that survived into the spoken text (`*`, `#`, backtick,
//     underscore emphasis, link syntax);
//   - imperative editorial directives that reached the voiceover
//     ("Ricostruisci …", "Apri con …");
//   - word boundaries that are non-monotonic, empty or outside the audio;
//   - a spoken word count that does not match the authored narration.
//
// It never rewrites anything: a report is evidence, and the caller decides
// whether the take is publishable.
package verbatimcheck

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SchemaVersion is the wire version of the persisted report.
const SchemaVersion = "verbatim-check.v1"

// Issue codes. They are stable so a durable runner can classify a failure.
const (
	CodeMarkdownMarker       = "markdown_marker"
	CodeEditorialDirective   = "editorial_directive"
	CodeBoundaryOrder        = "word_boundary_order"
	CodeBoundaryOutsideAudio = "word_boundary_outside_audio"
	CodeWordCoverage         = "word_coverage"
)

// Word coverage tolerance. A TTS reading a sanitized script must reproduce the
// same words: below 90% the take dropped narration, above 115% it read
// something that was not authored.
const (
	minWordCoverage = 0.90
	maxWordCoverage = 1.15
)

// Cue is one timed transcript span.
type Cue struct {
	Index int
	Start time.Duration
	End   time.Duration
	Text  string
}

// Issue is one failed check, with a stable code and a human detail.
type Issue struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// Report is the persisted verdict for one audio take.
type Report struct {
	SchemaVersion       string  `json:"schema_version"`
	Cues                int     `json:"cues"`
	SpokenWords         int     `json:"spoken_words"`
	ExpectedWords       int     `json:"expected_words"`
	WordCoveragePercent float64 `json:"word_coverage_percent"`
	AudioDurationMS     int64   `json:"audio_duration_ms"`
	Issues              []Issue `json:"issues,omitempty"`
}

// OK reports whether the take passed every check.
func (r Report) OK() bool { return len(r.Issues) == 0 }

// Summary is a one-line operator description.
func (r Report) Summary() string {
	if r.OK() {
		return fmt.Sprintf("verbatim ok: %d cues, %d/%d words", r.Cues, r.SpokenWords, r.ExpectedWords)
	}
	return fmt.Sprintf("verbatim failed: %d issue(s): %s", len(r.Issues), r.Issues[0].Detail)
}

var (
	vttTimingRE = regexp.MustCompile(`^\s*(\d{1,2}:\d{2}:\d{2}[.,]\d{1,3}|\d{1,2}:\d{2}[.,]\d{1,3})\s*-->\s*(\d{1,2}:\d{2}:\d{2}[.,]\d{1,3}|\d{1,2}:\d{2}[.,]\d{1,3})`)
	vttTagRE    = regexp.MustCompile(`<[^>]+>`)
)

// ParseVTT reads a WebVTT document into timed cues. It tolerates cue
// identifiers, cue settings after the timing line, and NOTE/STYLE blocks.
func ParseVTT(reader io.Reader) ([]Cue, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var cues []Cue
	var current *Cue
	var text []string
	flush := func() {
		if current == nil {
			return
		}
		current.Text = strings.TrimSpace(strings.Join(text, " "))
		current.Index = len(cues)
		cues = append(cues, *current)
		current = nil
		text = nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			continue
		}
		if strings.HasPrefix(trimmed, "WEBVTT") || strings.HasPrefix(trimmed, "NOTE") ||
			strings.HasPrefix(trimmed, "STYLE") || strings.HasPrefix(trimmed, "REGION") {
			continue
		}
		if match := vttTimingRE.FindStringSubmatch(trimmed); match != nil {
			flush()
			start, err := parseVTTTime(match[1])
			if err != nil {
				return nil, fmt.Errorf("verbatimcheck: start time %q: %w", match[1], err)
			}
			end, err := parseVTTTime(match[2])
			if err != nil {
				return nil, fmt.Errorf("verbatimcheck: end time %q: %w", match[2], err)
			}
			current = &Cue{Start: start, End: end}
			continue
		}
		if current == nil {
			// Cue identifier line.
			continue
		}
		text = append(text, vttTagRE.ReplaceAllString(line, ""))
	}
	flush()
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("verbatimcheck: read transcript: %w", err)
	}
	return cues, nil
}

func parseVTTTime(raw string) (time.Duration, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), ",", ".")
	parts := strings.Split(normalized, ":")
	var hours, minutes int
	var seconds float64
	var err error
	switch len(parts) {
	case 3:
		if hours, err = strconv.Atoi(parts[0]); err != nil {
			return 0, err
		}
		if minutes, err = strconv.Atoi(parts[1]); err != nil {
			return 0, err
		}
		seconds, err = strconv.ParseFloat(parts[2], 64)
	case 2:
		if minutes, err = strconv.Atoi(parts[0]); err != nil {
			return 0, err
		}
		seconds, err = strconv.ParseFloat(parts[1], 64)
	default:
		seconds, err = strconv.ParseFloat(parts[0], 64)
	}
	if err != nil {
		return 0, err
	}
	return time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds*float64(time.Second)), nil
}

// Check compares one transcript against the authored narration.
func Check(sourceText string, cues []Cue, audioDuration time.Duration) Report {
	sanitized := scriptpkg.SanitizeForSpeech(sourceText)
	expectedWords := len(strings.Fields(sanitized))
	report := Report{
		SchemaVersion:   SchemaVersion,
		Cues:            len(cues),
		ExpectedWords:   expectedWords,
		AudioDurationMS: audioDuration.Milliseconds(),
	}
	var spoken []string
	var previousStart, previousEnd time.Duration
	for _, cue := range cues {
		text := strings.TrimSpace(cue.Text)
		spoken = append(spoken, strings.Fields(text)...)
		if cue.End <= cue.Start {
			report.Issues = append(report.Issues, Issue{
				Code:   CodeBoundaryOrder,
				Detail: fmt.Sprintf("cue[%d] has an empty or negative window %s–%s", cue.Index, cue.Start, cue.End),
			})
		}
		if cue.Start < previousStart {
			report.Issues = append(report.Issues, Issue{
				Code:   CodeBoundaryOrder,
				Detail: fmt.Sprintf("cue[%d] starts at %s, before the previous cue at %s", cue.Index, cue.Start, previousStart),
			})
		}
		if cue.Start < previousEnd && cue.Index > 0 {
			report.Issues = append(report.Issues, Issue{
				Code:   CodeBoundaryOrder,
				Detail: fmt.Sprintf("cue[%d] starts at %s before the previous cue ends at %s", cue.Index, cue.Start, previousEnd),
			})
		}
		if audioDuration > 0 && cue.End > audioDuration+time.Second {
			report.Issues = append(report.Issues, Issue{
				Code:   CodeBoundaryOutsideAudio,
				Detail: fmt.Sprintf("cue[%d] ends at %s beyond the audio duration %s", cue.Index, cue.End, audioDuration),
			})
		}
		if marker := firstMarkdownMarker(text); marker != "" {
			report.Issues = append(report.Issues, Issue{
				Code:   CodeMarkdownMarker,
				Detail: fmt.Sprintf("cue[%d] speaks Markdown marker %q: %q", cue.Index, marker, clamp(text, 96)),
			})
		}
		for _, directive := range scriptpkg.FindEditorialDirectives(text) {
			report.Issues = append(report.Issues, Issue{
				Code:   CodeEditorialDirective,
				Detail: fmt.Sprintf("cue[%d] speaks an editorial directive: %q", cue.Index, clamp(directive.Phrase, 96)),
			})
		}
		previousStart = cue.Start
		if cue.End > previousEnd {
			previousEnd = cue.End
		}
	}
	report.SpokenWords = len(spoken)
	if expectedWords > 0 {
		report.WordCoveragePercent = round2(float64(report.SpokenWords) / float64(expectedWords) * 100)
		ratio := float64(report.SpokenWords) / float64(expectedWords)
		if ratio < minWordCoverage || ratio > maxWordCoverage {
			report.Issues = append(report.Issues, Issue{
				Code: CodeWordCoverage,
				Detail: fmt.Sprintf(
					"spoken %d words for %d authored words (%.1f%%, allowed %.0f–%.0f%%)",
					report.SpokenWords, expectedWords, report.WordCoveragePercent,
					minWordCoverage*100, maxWordCoverage*100),
			})
		}
	}
	return report
}

// firstMarkdownMarker returns the first formatting marker that a speech
// provider must never pronounce.
func firstMarkdownMarker(text string) string {
	for _, marker := range []string{"**", "__", "~~", "`", "#", "](", "![", "*", "_"} {
		if strings.Contains(text, marker) {
			return marker
		}
	}
	return ""
}

func round2(value float64) float64 {
	return float64(int64(value*100+0.5)) / 100
}

func clamp(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
