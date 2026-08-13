package audio

import (
	"fmt"
	"strings"
)

// CueOptions controls subtitle cue grouping. Subtitles are a DISPLAY
// projection of the canonical word-level timing: the JSON stays
// word-level, while cues group consecutive words into readable lines.
// Zero values fall back to the canonical defaults.
type CueOptions struct {
	// MaxChars is the soft character ceiling for one cue.
	MaxChars int
	// MaxWords is the hard word ceiling for one cue.
	MaxWords int
	// MaxDurationUS is the soft duration ceiling for one cue.
	MaxDurationUS int64
}

func (o CueOptions) normalized() CueOptions {
	if o.MaxChars <= 0 {
		o.MaxChars = 80
	}
	if o.MaxWords <= 0 {
		o.MaxWords = 15
	}
	if o.MaxDurationUS <= 0 {
		o.MaxDurationUS = 6_000_000 // 6s
	}
	return o
}

// SubtitleCue is one rendered subtitle line.
type SubtitleCue struct {
	Text    string
	StartUS int64
	EndUS   int64
}

// BuildCues groups the artifact's word boundaries into readable cues.
// A cue breaks after a sentence-ending token (., !, ?, :, ;), when the
// word ceiling is reached, or when the soft char/duration ceilings are
// exceeded. Word precision is never lost — cues only repackage the
// existing word timing.
func BuildCues(timing SpeechTimingArtifact, opts CueOptions) ([]SubtitleCue, error) {
	if err := timing.Validate(); err != nil {
		return nil, err
	}
	if len(timing.Words) == 0 {
		return nil, nil
	}
	o := opts.normalized()
	var cues []SubtitleCue
	start := 0
	for i := 0; i < len(timing.Words); i++ {
		word := timing.Words[i]
		count := i - start + 1
		overWords := count >= o.MaxWords
		overChars := cueChars(timing.Words[start:i+1]) >= o.MaxChars
		overDuration := word.EndUS-timing.Words[start].StartUS >= o.MaxDurationUS
		if (isSentenceEnd(word.Text) || overWords || overChars || overDuration) && count > 0 {
			cues = append(cues, makeCue(timing.Words[start:i+1]))
			start = i + 1
		}
	}
	if start < len(timing.Words) {
		cues = append(cues, makeCue(timing.Words[start:]))
	}
	return cues, nil
}

func makeCue(words []SpeechWordTiming) SubtitleCue {
	var b strings.Builder
	for i, word := range words {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strings.TrimSpace(word.Text))
	}
	return SubtitleCue{
		Text:    b.String(),
		StartUS: words[0].StartUS,
		EndUS:   words[len(words)-1].EndUS,
	}
}

func cueChars(words []SpeechWordTiming) int {
	chars := 0
	for i, word := range words {
		if i > 0 {
			chars++
		}
		chars += len([]rune(strings.TrimSpace(word.Text)))
	}
	return chars
}

func isSentenceEnd(text string) bool {
	switch {
	case strings.HasSuffix(text, "."),
		strings.HasSuffix(text, "!"),
		strings.HasSuffix(text, "?"),
		strings.HasSuffix(text, ":"),
		strings.HasSuffix(text, ";"):
		return true
	}
	return false
}

// RenderSRT produces an SRT document exclusively from the canonical
// timing artifact. The artifact is never mutated.
func RenderSRT(timing SpeechTimingArtifact, opts CueOptions) ([]byte, error) {
	cues, err := BuildCues(timing, opts)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for i, cue := range cues {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n",
			i+1, srtTimestamp(cue.StartUS), srtTimestamp(cue.EndUS), cue.Text)
	}
	return []byte(b.String()), nil
}

// RenderVTT produces a WebVTT document exclusively from the canonical
// timing artifact. The artifact is never mutated.
func RenderVTT(timing SpeechTimingArtifact, opts CueOptions) ([]byte, error) {
	cues, err := BuildCues(timing, opts)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, cue := range cues {
		fmt.Fprintf(&b, "%s --> %s\n%s\n\n",
			vttTimestamp(cue.StartUS), vttTimestamp(cue.EndUS), cue.Text)
	}
	return []byte(b.String()), nil
}

func srtTimestamp(us int64) string {
	ms := us / 1000
	h := ms / 3_600_000
	m := (ms % 3_600_000) / 60_000
	s := (ms % 60_000) / 1_000
	millis := ms % 1_000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, millis)
}

func vttTimestamp(us int64) string {
	ms := us / 1000
	h := ms / 3_600_000
	m := (ms % 3_600_000) / 60_000
	s := (ms % 60_000) / 1_000
	millis := ms % 1_000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, millis)
}
