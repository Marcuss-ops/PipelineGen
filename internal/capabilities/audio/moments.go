package audio

import (
	"strings"
)

// MomentKind classifies a timestamped moment by the extraction surface that
// produced it. The renderer consumes only kind + value + start/end; it never
// learns which TTS provider synthesized the audio or how the timing was
// captured.
type MomentKind string

const (
	// MomentEntity anchors a named entity (person, org, place) in time.
	MomentEntity MomentKind = "entity"
	// MomentPhrase anchors an important phrase in time.
	MomentPhrase MomentKind = "phrase"
	// MomentKeyword anchors an important keyword in time.
	MomentKeyword MomentKind = "keyword"
)

// MomentQuery is one LLM-produced semantic item to anchor in time. The LLM
// contributes ONLY the kind and the surface value (an entity, an important
// phrase, or a keyword); it never produces timestamps — those are derived
// deterministically from the canonical word timing via PhraseLocator.
type MomentQuery struct {
	Kind  MomentKind `json:"kind"`
	Value string     `json:"value"`
}

// Moment is the deterministic timestamped projection of a MomentQuery.
// StartUS/EndUS come exclusively from the canonical word timing via
// Rust aligner (first matched word start → last matched word end), so a
// moment exists only when its value occurs verbatim in the speech — never
// approximated and never invented by a model.
type Moment struct {
	Kind  MomentKind `json:"kind"`
	Value string     `json:"value"`

	// StartUS/EndUS are the exact microsecond span of the first matched
	// word's start to the last matched word's end.
	StartUS int64 `json:"start_us"`
	EndUS   int64 `json:"end_us"`

	// WordStart/WordEnd reference the artifact word indices of the span.
	WordStart int `json:"word_start,omitempty"`
	WordEnd   int `json:"word_end,omitempty"`

	// Occurrence is 1-based among matches of the same value in document
	// order.
	Occurrence int `json:"occurrence,omitempty"`
}

// ProjectMoments projects the alignments returned by the Rust worker onto the
// caller's moment queries. It is deterministic and fail-closed:
//
//   - an invalid timing artifact returns an error (never timestamps);
//   - an empty query value or a value the worker did not align is skipped
//     (never a fabricated timestamp);
//   - duplicate (kind, value) queries are collapsed to one set of moments;
//   - output order is query order, then occurrence order within a query.
//
// Alignment is owned by the Rust worker; this function only joins its spans to
// the query list and never matches words itself.
func ProjectMoments(timing SpeechTimingArtifact, queries []MomentQuery, alignments []LocatedPhrase) ([]Moment, error) {
	if err := timing.Validate(); err != nil {
		return nil, err
	}
	byText := make(map[string][]LocatedPhrase, len(alignments))
	for _, alignment := range alignments {
		key := normalizeMomentText(alignment.Text)
		if key == "" {
			continue
		}
		byText[key] = append(byText[key], alignment)
	}
	var moments []Moment
	seen := make(map[string]struct{}, len(queries))
	for _, q := range queries {
		value := strings.TrimSpace(q.Value)
		if value == "" {
			continue
		}
		key := string(q.Kind) + "\x00" + normalizeMomentText(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		for _, lp := range byText[normalizeMomentText(value)] {
			moments = append(moments, Moment{
				Kind:       q.Kind,
				Value:      lp.Text,
				StartUS:    lp.StartUS,
				EndUS:      lp.EndUS,
				WordStart:  lp.WordStart,
				WordEnd:    lp.WordEnd,
				Occurrence: lp.Occurrence,
			})
		}
	}
	return moments, nil
}

func normalizeMomentText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}
