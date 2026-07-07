// Package timeline — sequence.go (FASE A, July 2026).
//
// SequenceSpec and SequenceNode are the canonical temporal mapping
// types. A sequence is NOT a layer — it is a PURE temporal transform
// that maps a parent_frame into a local_frame. Layers and media are
// children of sequences, not peers.
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SINGLE canonical owner of SequenceSpec, TimeMappingResult,
// map_sequence_time(), and SequenceNode. No other package may
// reimplement temporal mapping logic.
//
// godlike/07 typed-error contract: TimeMappingResult.Active is the
// canonical boolean gate; a non-active result means the parent_frame
// is outside the sequence's temporal window (before from, after
// from+duration, or after trim_after).
package timeline

// ── SequenceSpec ───────────────────────────────────────────────────

// SequenceSpec defines the temporal boundaries and behavior of a
// sequence. All fields use the UNIFIED vocabulary from the plan §7:
// from, duration, trim_before, trim_after.
//
// Zero value: from=0, no duration (infinite), no trim, no freeze.
type SequenceSpec struct {
	// From is the frame at which the sequence becomes active in
	// its parent's time space. parent_frame < from → not active.
	From Frame `json:"from"`

	// Duration is the length of the sequence in frames.
	// nil means "infinite" (sequence stays active forever after From).
	// Non-nil: parent_frame >= from + duration → not active.
	Duration *Frame `json:"duration,omitempty"`

	// TrimBefore is the number of frames to skip at the start of
	// the source media. local_frame = (parent_frame - from) + trim_before.
	TrimBefore Frame `json:"trim_before"`

	// TrimAfter is the frame at which the source media ends.
	// nil means "no trim" (media plays to its natural end).
	// Non-nil: local_frame >= trim_after → not active.
	TrimAfter *Frame `json:"trim_after,omitempty"`

	// Freeze when true locks the local_frame to FreezeAt for all
	// active parent frames. Used for still frames / hold segments.
	Freeze bool `json:"freeze"`

	// FreezeAt is the frame to freeze on when Freeze is true.
	FreezeAt Frame `json:"freeze_at"`
}

// DurationValue returns the duration as a value, or 0 if nil.
func (s SequenceSpec) DurationValue() Frame {
	if s.Duration == nil {
		return 0
	}
	return *s.Duration
}

// TrimAfterValue returns trim_after as a value, or 0 if nil.
func (s SequenceSpec) TrimAfterValue() Frame {
	if s.TrimAfter == nil {
		return 0
	}
	return *s.TrimAfter
}

// HasDuration returns true if duration is explicitly set (non-nil and >0).
func (s SequenceSpec) HasDuration() bool {
	return s.Duration != nil && *s.Duration > 0
}

// ── TimeMappingResult ──────────────────────────────────────────────

// TimeMappingResult is the canonical return type of map_sequence_time().
// It answers two questions: (1) is the sequence active at this parent
// frame? (2) if so, what is the local frame?
//
// godlike/07: Active being false is the ONLY valid reason to skip a
// sequence. Processors must not check temporal bounds independently.
type TimeMappingResult struct {
	// Active is true when the parent_frame falls within the
	// sequence's temporal window.
	Active bool `json:"active"`

	// LocalFrame is the resolved frame within the sequence's time
	// space. Only valid when Active is true.
	LocalFrame Frame `json:"local_frame"`
}

// ── map_sequence_time ──────────────────────────────────────────────

// MapSequenceTime resolves a parent_frame against a SequenceSpec and
// returns the TimeMappingResult. This is the canonical temporal mapping
// function — ALL sequence time decisions flow through this single entry
// point.
//
// Algorithm (per the plan §4.3):
//  1. parent_frame < spec.from → not active
//  2. raw = parent_frame - spec.from
//  3. if duration set AND raw >= duration → not active
//  4. local = raw + spec.trim_before
//  5. if trim_after set AND local >= trim_after → not active
//  6. if freeze → local = spec.freeze_at
//  7. return {active=true, local_frame=local}
func MapSequenceTime(spec SequenceSpec, parentFrame Frame) TimeMappingResult {
	if parentFrame < spec.From {
		return TimeMappingResult{Active: false}
	}

	raw := Frame(parentFrame.Value() - spec.From.Value())

	if spec.HasDuration() && raw >= *spec.Duration {
		return TimeMappingResult{Active: false}
	}

	local := Frame(raw.Value() + spec.TrimBefore.Value())

	if spec.TrimAfter != nil && local >= *spec.TrimAfter {
		return TimeMappingResult{Active: false}
	}

	if spec.Freeze {
		local = spec.FreezeAt
	}

	return TimeMappingResult{
		Active:     true,
		LocalFrame: local,
	}
}

// ── SequenceNode ───────────────────────────────────────────────────

// SequenceNode is a named sequence in the timeline tree. It owns a
// SequenceSpec and a list of child TimelineNodes. Sequences can be
// nested: a child may itself be a SequenceNode (FASE A §5 / FASE E).
type SequenceNode struct {
	// Name is the human-readable identifier for this sequence.
	// Used for scope_path construction: "root/intro/title".
	Name string `json:"name"`

	// Spec defines the temporal mapping for this sequence.
	Spec SequenceSpec `json:"spec"`

	// Children is the ordered list of child nodes in this sequence.
	// Children can be SequenceNode (nested), LayerNode, or MediaNode.
	Children []TimelineNode `json:"children,omitempty"`
}

// NewSequence creates a SequenceNode with the given name and spec.
func NewSequence(name string, spec SequenceSpec) SequenceNode {
	return SequenceNode{
		Name:     name,
		Spec:     spec,
		Children: nil,
	}
}

// AddChild appends a TimelineNode as a child of this sequence.
func (s *SequenceNode) AddChild(child TimelineNode) {
	s.Children = append(s.Children, child)
}

// IsEmpty returns true if the sequence has no children.
func (s SequenceNode) IsEmpty() bool {
	return len(s.Children) == 0
}

// ChildCount returns the number of direct children.
func (s SequenceNode) ChildCount() int {
	return len(s.Children)
}

// timelineNodeMarker satisfies the TimelineNode interface.
func (SequenceNode) timelineNodeMarker() {}
