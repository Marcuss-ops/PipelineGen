// Package stockpipeline — render_transitions.go (PR-SPLIT-RENDER-PORTS, August 2026).
//
// Canonical owner of the transition catalog: TransitionSegment enum +
// SegmentEnd / SegmentStart constants + TransitionRenderer func type +
// Transition struct + TransitionRegistry interface. Extracted from
// the 569 LoC render_ports.go monolith per AGENTS.md Pattern 5 +
// godlike/06 SSOT one-canonical-owner-per-fact.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - All transition types (TransitionSegment + 2 constants +
//     TransitionRenderer func + Transition struct + TransitionRegistry
//     interface) live ONLY in this file.
//   - The 2 main ports these transitions support (StockRenderer +
//     VideoCutter) live in render_ports.go (sister file).
//   - The neutral DTOs (RenderRequest / RenderResult / etc.) live in
//     render_dto.go (sister file).
//
// godlike/07 minimum-blast-radius: the transition catalog API is
// STABLE — the composition root's TransitionRegistry implementation
// and the infra renderer's catalog consumption both reference
// these types by name. No surface-contract changes.
package stockpipeline

// ── Transition Registry ────────────────────────────────────────────────

// TransitionSegment is the temporal segment a transition applies to.
// End-side fades the END of a clip; Start-side fades the START of the
// NEXT clip. Together they form the visual handoff at a clip boundary.
type TransitionSegment int

const (
	// SegmentEnd applies the filter to the END portion of the current
	// clip (typically a fade-out or build-up transition like boxblur).
	SegmentEnd TransitionSegment = iota

	// SegmentStart applies the filter to the START portion of the next
	// clip (typically a fade-in or un-build transition).
	SegmentStart
)

// TransitionRenderer is the func-format a single transition uses to emit
// its FFmpeg filter chain. clipDurationSec is the canonical clip length
// (used to position the transition inside the END-side clip).
//
// The closure approach (vs string templates) per PR6 design allows each
// transition to encapsulate its own asymmetric filter syntax — e.g. `fade`
// uses `st=%f:d=%f` whereas `blur` uses `boxblur=15:enable='gt(t,%f)'`.
type TransitionRenderer func(clipDurationSec int) string

// Transition is a single named transition entry in the catalog.
type Transition struct {
	Name string

	// RenderEnd is the FFmpeg filter fragment applied to the END of a
	// clip (typically the fade-INTO the next clip).
	RenderEnd TransitionRenderer

	// RenderStart is the FFmpeg filter fragment applied to the START of
	// the next clip (typically the fade-FROM the previous clip).
	RenderStart TransitionRenderer

	// Description is a one-line human label for telemetry.
	Description string
}

// TransitionRegistry exposes the catalogue of transitions available to
// the StockRenderer port. The application layer composes transitions
// via this interface; the infra implementation reads it during
// filter_complex construction.
//
// Implementations are expected to be effectively read-only (Register*
// is for catalog extension during bootstrap); the infra renderer
// consults All()/Get() during Render().
type TransitionRegistry interface {
	// All returns all registered transitions in stable (insertion) order.
	All() []Transition

	// Get returns the transition registered under the given name, or
	// (Transition{}, false) when missing.
	Get(name string) (Transition, bool)

	// Len returns the number of registered transitions.
	Len() int
}
