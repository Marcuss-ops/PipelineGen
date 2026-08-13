package audio

import (
	"errors"
	"fmt"
)

// AudioEdit describes ONE silence-removal edit on the original audio
// timeline. SourceStartUS→SourceEndUS is the removed silence interval in
// the ORIGINAL (pre-clean) timeline; OutputStartUS→OutputEndUS is where
// that cut lands in the CLEANED output timeline. A removed interval is
// absent from the output, so its output interval is degenerate
// (OutputStartUS == OutputEndUS at the cut position). Edits are
// ordered by SourceStartUS and never overlap.
type AudioEdit struct {
	SourceStartUS int64 `json:"source_start_us"`
	SourceEndUS   int64 `json:"source_end_us"`

	OutputStartUS int64 `json:"output_start_us"`
	OutputEndUS   int64 `json:"output_end_us"`
}

var (
	// ErrInvalidEditRange is returned when an edit has a negative or
	// inverted interval (fail-closed: never derive timestamps from a
	// malformed edit).
	ErrInvalidEditRange = errors.New("invalid audio edit interval")
	// ErrOverlappingEdits is returned when two edits overlap on the
	// source timeline (the remap would double-count removed duration).
	ErrOverlappingEdits = errors.New("overlapping audio edits")
	// ErrInconsistentEditMap is returned when an edit's output interval
	// does not match the position derivable from its source interval and
	// the preceding edits (the map would produce fake timestamps).
	ErrInconsistentEditMap = errors.New("audio edit map inconsistent with source intervals")
)

// remapPoint maps one point on the ORIGINAL timeline onto the CLEANED
// timeline: every fully-removed interval before the point shifts it left
// by the removed duration; a point inside a removed interval clamps to
// the start of that cut (the silence itself does not survive).
func remapPoint(t int64, edits []AudioEdit) int64 {
	var removed int64
	for _, e := range edits {
		if t >= e.SourceEndUS {
			removed += e.SourceEndUS - e.SourceStartUS
			continue
		}
		if t > e.SourceStartUS {
			t = e.SourceStartUS
		}
		break
	}
	return t - removed
}

// validateEditMap enforces the canonical edit-map contract: ordered,
// non-overlapping, non-negative source intervals with consistent output
// anchors. Output anchors are cross-checked against the source-derived
// positions so a broken edit map can never produce plausible-but-wrong
// timestamps (godlike/07 no-fake-availability).
func validateEditMap(edits []AudioEdit) error {
	var prevSourceEnd int64
	for i, e := range edits {
		if e.SourceStartUS < 0 || e.SourceEndUS < e.SourceStartUS {
			return fmt.Errorf("%w: edit %d source [%d,%d)", ErrInvalidEditRange, i, e.SourceStartUS, e.SourceEndUS)
		}
		if e.OutputStartUS < 0 || e.OutputEndUS < e.OutputStartUS {
			return fmt.Errorf("%w: edit %d output [%d,%d)", ErrInvalidEditRange, i, e.OutputStartUS, e.OutputEndUS)
		}
		if i > 0 && e.SourceStartUS < prevSourceEnd {
			return fmt.Errorf("%w: edit %d starts at %d before previous edit ends at %d",
				ErrOverlappingEdits, i, e.SourceStartUS, prevSourceEnd)
		}
		prevSourceEnd = e.SourceEndUS
	}
	for i, e := range edits {
		if wantStart, wantEnd := remapPoint(e.SourceStartUS, edits), remapPoint(e.SourceEndUS, edits); wantStart != e.OutputStartUS || wantEnd != e.OutputEndUS {
			return fmt.Errorf("%w: edit %d output [%d,%d) does not match source-derived [%d,%d)",
				ErrInconsistentEditMap, i, e.OutputStartUS, e.OutputEndUS, wantStart, wantEnd)
		}
	}
	return nil
}

// RemapSpeechTiming maps word boundaries that refer to the ORIGINAL
// (pre-clean) audio timeline onto the CLEANED output timeline using the
// silence-removal edit map. It is PURE: the caller computes the hashes
// and final audio duration; this function only rewrites timestamps.
//
// An empty edit map is the identity mapping (returns a copy, never an
// alias). Words whose timing falls entirely inside a removed silence
// interval collapse to a zero-length interval at the cut position (real
// speech never lives in pure silence, but a boundary straddling a cut
// must still land on the surviving timeline). Fail-closed validation:
// overlapping, malformed, or self-inconsistent edit maps produce a typed
// error instead of fake timestamps.
func RemapSpeechTiming(words []SpeechWordTiming, edits []AudioEdit) ([]SpeechWordTiming, error) {
	if err := validateEditMap(edits); err != nil {
		return nil, err
	}
	out := make([]SpeechWordTiming, len(words))
	for i, w := range words {
		out[i] = w
		out[i].StartUS = remapPoint(w.StartUS, edits)
		out[i].EndUS = remapPoint(w.EndUS, edits)
		if out[i].EndUS < out[i].StartUS {
			return nil, fmt.Errorf("remap speech timing: word %d interval inverted after remap [%d,%d)",
				w.Index, out[i].StartUS, out[i].EndUS)
		}
	}
	return out, nil
}
