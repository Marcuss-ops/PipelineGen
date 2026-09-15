package texttracks

// shortform_cues.go owns the short-form reading contract for burned captions.
//
// Canonical transcripts come from two very different producers: YouTube VTT
// (1-2s chunks, one short line) and Whisper (segments up to ~7.5s carrying
// 3-4 wrapped lines). Feeding either straight into an ASS track makes the same
// pipeline produce wildly different looking clips: one clip flickers a new
// caption every second, another holds a 4-line wall for seven seconds.
//
// NormalizeShortFormCues rewrites the cue windows into readable short-form
// captions:
//
//   - at most MaxLines wrapped lines and MaxCharsPerLine runes per line,
//   - at most its reading time on screen when it carries more than one line
//     (split at word boundaries, never mid-word),
//   - at least MinCueMs on screen unless the source segment is shorter,
//   - no caption flicker for sub-MaxGapMergeMs silences,
//   - a SPLIT only redistributes its own source segment's window, so the
//     pieces of one segment always partition that window exactly.
//
// The per-segment window invariant is deliberately relaxed in exactly two
// readability cases, both pinned by the contract tests below:
//
//   - a sub-MinCueMs caption is merged into an adjacent one when the joined
//     text still fits the budget (a 200ms flash is worse than a short
//     caption that starts early);
//   - a sub-MaxGapMergeMs silence between adjacent captions is closed so the
//     caption does not blink off and back on.
//
// Both cases produce a caption whose window is the UNION of the windows it
// absorbed. The invariant that always holds is therefore: captions never
// overlap, never run past the next caption's start, never invent text and
// never invent time outside [first source start, last source end].
//
// It is deterministic and pure: identical cues + policy always produce
// identical output.

import (
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ShortFormPolicy is the readability contract applied to cue windows before
// they are compiled into an ASS artifact.
type ShortFormPolicy struct {
	// MaxCPS is the reading load, in characters per second: a caption that
	// carries more than one line is split until its dwell time is no longer
	// than the time its own text needs at this rate. A single line is one
	// glance and keeps its utterance, however slowly it is spoken.
	MaxCPS float64
	// MinCueMs is the shortest a caption should stay on screen; shorter
	// pieces take slack from a neighbour and are merged when their text fits.
	MinCueMs int64
	// MaxCharsPerLine is the word-wrap width used by the ASS compiler.
	MaxCharsPerLine int
	// MaxLines is the number of wrapped lines a caption may occupy.
	MaxLines int
	// MaxGapMergeMs is the largest silence a caption is extended across, so
	// captions do not blink on/off between adjacent segments.
	MaxGapMergeMs int64
}

// DefaultShortFormPolicy is the canonical short-form contract used by
// clip.render. The line width matches the ASS generator's wrap width so a
// normalized cue always renders as at most MaxLines lines. 22 cps is the
// upper end of the broadcast subtitle guideline, appropriate for short-form
// captions watched without pausing.
func DefaultShortFormPolicy() ShortFormPolicy {
	return ShortFormPolicy{
		MaxCPS:          22,
		MinCueMs:        700,
		MaxCharsPerLine: assWrapChars,
		MaxLines:        2,
		MaxGapMergeMs:   400,
	}
}

// readingMs is the time this text needs to be read, floored at MinCueMs.
func (p ShortFormPolicy) readingMs(chars int) int64 {
	need := int64(float64(chars)*1000.0/p.MaxCPS + 0.5)
	if need < p.MinCueMs {
		need = p.MinCueMs
	}
	return need
}

// needsSplit reports whether a caption asks for more dwell time than its text
// needs. A single line is never split: it reads in one glance and must stay
// for as long as it is spoken.
func (p ShortFormPolicy) needsSplit(text string, durationMs int64) bool {
	chars := utf8.RuneCountInString(text)
	if chars <= p.MaxCharsPerLine {
		return false
	}
	return durationMs > p.readingMs(chars)
}

// MaxCharsPerCue is the character budget one caption may carry under this
// policy. Callers that size a render box (RenderingGen's subtitle box) use it
// together with MaxLines to derive the box height.
func (p ShortFormPolicy) MaxCharsPerCue() int {
	return p.MaxCharsPerLine * p.MaxLines
}

func (p ShortFormPolicy) withDefaults() ShortFormPolicy {
	d := DefaultShortFormPolicy()
	if p.MaxCPS <= 0 {
		p.MaxCPS = d.MaxCPS
	}
	if p.MinCueMs <= 0 {
		p.MinCueMs = d.MinCueMs
	}
	if p.MaxCharsPerLine <= 0 {
		p.MaxCharsPerLine = d.MaxCharsPerLine
	}
	if p.MaxLines <= 0 {
		p.MaxLines = d.MaxLines
	}
	if p.MaxGapMergeMs <= 0 {
		p.MaxGapMergeMs = d.MaxGapMergeMs
	}
	return p
}

// NormalizeShortFormCues rewrites cues into short-form captions. An input
// with no usable cue window yields no captions; an input that is usable but
// that normalization cannot improve is returned unchanged (copied): captions
// are never invented and a transcript is never dropped because normalization
// could not help.
func NormalizeShortFormCues(cues []detail.TimedCue, policy ShortFormPolicy) []detail.TimedCue {
	if len(cues) == 0 {
		return nil
	}
	p := policy.withDefaults()
	out := make([]detail.TimedCue, 0, len(cues))
	usable := 0
	for _, cue := range cues {
		if strings.TrimSpace(cue.Text) != "" && cue.EndMs > cue.StartMs {
			usable++
		}
		out = append(out, splitCue(cue, p)...)
	}
	if len(out) == 0 {
		if usable == 0 {
			return nil
		}
		return copyCues(cues)
	}
	out = mergeShortPieces(out, p)
	out = closeSmallGaps(out, p)
	return out
}

func copyCues(in []detail.TimedCue) []detail.TimedCue {
	out := make([]detail.TimedCue, len(in))
	copy(out, in)
	return out
}

// splitCue splits one source cue into caption-sized pieces whose windows
// partition the source window in proportion to each piece's text length.
func splitCue(cue detail.TimedCue, p ShortFormPolicy) []detail.TimedCue {
	text := strings.Join(strings.Fields(cue.Text), " ")
	if text == "" || cue.EndMs <= cue.StartMs {
		return nil
	}
	window := cue.EndMs - cue.StartMs
	pieces := chunkText(text, p)
	if len(pieces) == 0 {
		return nil
	}
	// A piece may still hold more than its share of the utterance (dense
	// speech). Subdivide until every piece is within its reading time or no
	// word boundary is left.
	for {
		total := 0
		for _, s := range pieces {
			total += utf8.RuneCountInString(s)
		}
		if total == 0 {
			return []detail.TimedCue{{StartMs: cue.StartMs, EndMs: cue.EndMs, Text: text}}
		}
		if !anyPieceNeedsSplit(pieces, total, window, p) {
			break
		}
		next, ok := subdivide(pieces)
		if !ok {
			break
		}
		pieces = next
	}
	total := 0
	for _, s := range pieces {
		total += utf8.RuneCountInString(s)
	}
	if total == 0 {
		return []detail.TimedCue{{StartMs: cue.StartMs, EndMs: cue.EndMs, Text: text}}
	}
	out := make([]detail.TimedCue, 0, len(pieces))
	consumed := 0
	for i, piece := range pieces {
		count := utf8.RuneCountInString(piece)
		start := cue.StartMs
		if consumed > 0 {
			start = cue.StartMs + window*int64(consumed)/int64(total)
		}
		end := cue.EndMs
		if i < len(pieces)-1 {
			end = cue.StartMs + window*int64(consumed+count)/int64(total)
		}
		if end <= start {
			end = start + 10
		}
		if end > cue.EndMs {
			end = cue.EndMs
		}
		out = append(out, detail.TimedCue{StartMs: start, EndMs: end, Text: piece})
		consumed += count
	}
	return rebalanceMinDurations(out, p)
}

// rebalanceMinDurations lifts captions below MinCueMs by moving the shared
// boundaries against a neighbour that can spare the time. The source window is
// still partitioned exactly (boundaries move in pairs), so no overlap and no
// invented silence appear, and a neighbour never drops below MinCueMs either.
// When neither neighbour has slack, the short caption stays: a sub-second
// caption is the honest reading of a sub-second utterance.
func rebalanceMinDurations(pieces []detail.TimedCue, p ShortFormPolicy) []detail.TimedCue {
	if len(pieces) < 2 {
		return pieces
	}
	for i := range pieces {
		deficit := p.MinCueMs - (pieces[i].EndMs - pieces[i].StartMs)
		if deficit <= 0 {
			continue
		}
		for _, n := range []int{i + 1, i - 1} {
			if deficit <= 0 || n < 0 || n >= len(pieces) {
				continue
			}
			spare := (pieces[n].EndMs - pieces[n].StartMs) - p.MinCueMs
			if spare <= 0 {
				continue
			}
			take := deficit
			if take > spare {
				take = spare
			}
			if n > i {
				pieces[i].EndMs += take
				pieces[n].StartMs += take
			} else {
				pieces[i].StartMs -= take
				pieces[n].EndMs -= take
			}
			deficit -= take
		}
	}
	return pieces
}

// anyPieceNeedsSplit estimates each piece's share of the source window by
// character count and reports whether any of them is over its reading time.
func anyPieceNeedsSplit(pieces []string, total int, window int64, p ShortFormPolicy) bool {
	if total == 0 {
		return false
	}
	for _, s := range pieces {
		share := window * int64(utf8.RuneCountInString(s)) / int64(total)
		if p.needsSplit(s, share) {
			return true
		}
	}
	return false
}

// chunkText groups words into captions that wrap to at most MaxLines lines.
// Line counting reuses wrapASSText so the ASS output and this budget can
// never disagree.
func chunkText(text string, p ShortFormPolicy) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	maxChars := p.MaxCharsPerCue()
	var pieces []string
	line := ""
	for _, word := range words {
		candidate := word
		if line != "" {
			candidate = line + " " + word
		}
		if line != "" && (utf8.RuneCountInString(candidate) > maxChars || wrappedLines(candidate, p.MaxCharsPerLine) > p.MaxLines) {
			pieces = append(pieces, line)
			line = word
			continue
		}
		line = candidate
	}
	if line != "" {
		pieces = append(pieces, line)
	}
	return pieces
}

func wrappedLines(text string, maxChars int) int {
	wrapped := wrapASSText(text, maxChars)
	if wrapped == "" {
		return 0
	}
	return strings.Count(wrapped, `\N`) + 1
}

// subdivide splits every piece that carries more than one word, producing a
// finer partition at word boundaries. It reports false when no piece can be
// split further (a single very long word is left alone).
func subdivide(pieces []string) ([]string, bool) {
	out := make([]string, 0, len(pieces)*2)
	changed := false
	for _, piece := range pieces {
		words := strings.Fields(piece)
		if len(words) < 2 {
			out = append(out, piece)
			continue
		}
		half := len(words) / 2
		out = append(out, strings.Join(words[:half], " "), strings.Join(words[half:], " "))
		changed = true
	}
	if !changed {
		return pieces, false
	}
	return out, true
}

// mergeShortPieces folds pieces shorter than MinCueMs into a neighbouring
// piece when the merged text still fits both the character budget and the
// duration budget. Text is only ever joined, never dropped: when neither
// neighbour can absorb the piece it is emitted as-is (a sub-second caption is
// a far smaller defect than a caption that holds for seven seconds).
func mergeShortPieces(pieces []detail.TimedCue, p ShortFormPolicy) []detail.TimedCue {
	if len(pieces) == 0 {
		return nil
	}
	out := make([]detail.TimedCue, 0, len(pieces))
	for _, piece := range pieces {
		if piece.EndMs-piece.StartMs < p.MinCueMs {
			if n := len(out); n > 0 {
				if merged, ok := mergeCaption(out[n-1], piece, p); ok {
					out[n-1] = merged
					continue
				}
			}
		}
		out = append(out, piece)
	}
	// A trailing sub-second caption can only merge forward.
	for i := 0; i < len(out)-1; i++ {
		if out[i].EndMs-out[i].StartMs >= p.MinCueMs {
			continue
		}
		if merged, ok := mergeCaption(out[i], out[i+1], p); ok {
			out[i] = merged
			out = append(out[:i+1], out[i+2:]...)
			i--
		}
	}
	return out
}

func mergeCaption(a, b detail.TimedCue, p ShortFormPolicy) (detail.TimedCue, bool) {
	if gap := b.StartMs - a.EndMs; gap > p.MaxGapMergeMs {
		// A caption is never stretched across a real silence.
		return detail.TimedCue{}, false
	}
	text := a.Text
	if strings.TrimSpace(text) == "" {
		text = b.Text
	} else if strings.TrimSpace(b.Text) != "" {
		text = text + " " + b.Text
	}
	if utf8.RuneCountInString(text) > p.MaxCharsPerCue() {
		return detail.TimedCue{}, false
	}
	if wrappedLines(text, p.MaxCharsPerLine) > p.MaxLines {
		return detail.TimedCue{}, false
	}
	return detail.TimedCue{StartMs: a.StartMs, EndMs: b.EndMs, Text: text}, true
}

// closeSmallGaps removes sub-MaxGapMergeMs silences between adjacent captions
// so a caption cannot blink off and back on for a fraction of a second. A
// hold is bounded by the next caption's start (never an overlap) and by
// MaxGapMergeMs of extra duration; larger silences are preserved so no
// caption is shown while nobody is speaking.
func closeSmallGaps(cues []detail.TimedCue, p ShortFormPolicy) []detail.TimedCue {
	if p.MaxGapMergeMs <= 0 {
		return cues
	}
	for i := 0; i < len(cues)-1; i++ {
		gap := cues[i+1].StartMs - cues[i].EndMs
		if gap <= 0 || gap > p.MaxGapMergeMs {
			continue
		}
		cues[i].EndMs = cues[i+1].StartMs
	}
	return cues
}
