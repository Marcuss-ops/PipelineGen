// Package render — canonical TransitionRegistry concrete implementation (PR6, June 2026).
//
// This file OWNS the concrete transition catalog and FFmpeg filter fragments
// that were previously in the application layer (stockpipeline/transitions.go).
// The TransitionRegistry port interface and Transition DTO remain in the
// application package (stockpipeline/ports.go); this infrastructure adapter
// satisfies the port and injects into the composition root.
//
// Import-boundary invariant (AGENTS.md Pattern 0):
//
//	This package MAY import internal/application/assets/providers/stock/stockpipeline
//	for port types (Transition, TransitionRegistry, TransitionRenderer).
//	The application layer MUST NOT import this package.
package render

import (
	"fmt"
	"sync"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
)

// ── Canonical catalog ────────────────────────────────────────────────
//
// The 14 transitions preserved verbatim from the pre-PR6 implementation:
//
//	fadeblack, fadewhite, flash, blur, gray,
//	colorred, colorblue, colorgreen, coloryellow,
//	colorpurple, colororange, colorpink,
//	negate, vignette, fastblur
//
// Mirror semantics are preserved:
//   - RenderEnd applies to the END of a clip (e.g. fade t=out, boxblur enable)
//   - RenderStart applies to the START of the next clip (e.g. fade t=in, boxblur enable='lt(t,D)')
//
// Each closure receives clipDurationSec (canonical clip length) so the
// transition can position itself (e.g. fadeStart = duration - 0.5).

// defaultCatalog holds the insertion-ordered list of canonical transitions.
// Modifying the order shifts the rotation; the existing PR6-aware tests
// reference Names so explicit testing is robust to ordering changes.
var defaultCatalog = []stockpipeline.Transition{
	{
		Name:        "fadeblack",
		Description: "Fade out to black at clip end, fade in from black at next clip start.",
		RenderEnd: func(d int) string {
			dur := 0.5
			st := float64(d) - dur
			return fmt.Sprintf("fade=t=out:st=%f:d=%f", st, dur)
		},
		RenderStart: func(d int) string {
			dur := 0.5
			return fmt.Sprintf("fade=t=in:st=0:d=%f", dur)
		},
	},
	{
		Name:        "fadewhite",
		Description: "Fade to/from white instead of black.",
		RenderEnd: func(d int) string {
			dur := 0.5
			st := float64(d) - dur
			return fmt.Sprintf("fade=t=out:st=%f:d=%f:color=white", st, dur)
		},
		RenderStart: func(d int) string {
			dur := 0.5
			return fmt.Sprintf("fade=t=in:st=0:d=%f:color=white", dur)
		},
	},
	{
		Name:        "flash",
		Description: "Flash-burst white near clip end.",
		RenderEnd: func(d int) string {
			dur := 0.2
			st := float64(d) - dur
			return fmt.Sprintf("fade=t=out:st=%f:d=%f:color=white", st, dur)
		},
		RenderStart: func(d int) string {
			dur := 0.2
			return fmt.Sprintf("fade=t=in:st=0:d=%f:color=white", dur)
		},
	},
	{
		Name:        "blur",
		Description: "Box-blur ramp near clip boundary.",
		RenderEnd: func(d int) string {
			st := float64(d) - 0.5
			return fmt.Sprintf("boxblur=15:enable='gt(t,%f)'", st)
		},
		RenderStart: func(d int) string {
			return "boxblur=15:enable='lt(t,0.5)'"
		},
	},
	{
		Name:        "gray",
		Description: "Fade to/from gray.",
		RenderEnd: func(d int) string {
			dur := 0.5
			st := float64(d) - dur
			return fmt.Sprintf("fade=t=out:st=%f:d=%f:color=gray", st, dur)
		},
		RenderStart: func(d int) string {
			dur := 0.5
			return fmt.Sprintf("fade=t=in:st=0:d=%f:color=gray", dur)
		},
	},
	{
		Name:        "colorred",
		RenderEnd:   fadeEndWithColor("red"),
		RenderStart: fadeStartWithColor("red"),
	},
	{
		Name:        "colorblue",
		RenderEnd:   fadeEndWithColor("blue"),
		RenderStart: fadeStartWithColor("blue"),
	},
	{
		Name:        "colorgreen",
		RenderEnd:   fadeEndWithColor("green"),
		RenderStart: fadeStartWithColor("green"),
	},
	{
		Name:        "coloryellow",
		RenderEnd:   fadeEndWithColor("yellow"),
		RenderStart: fadeStartWithColor("yellow"),
	},
	{
		Name:        "colorpurple",
		RenderEnd:   fadeEndWithColor("purple"),
		RenderStart: fadeStartWithColor("purple"),
	},
	{
		Name:        "colororange",
		RenderEnd:   fadeEndWithColor("orange"),
		RenderStart: fadeStartWithColor("orange"),
	},
	{
		Name:        "colorpink",
		RenderEnd:   fadeEndWithColor("pink"),
		RenderStart: fadeStartWithColor("pink"),
	},
	{
		Name:        "negate",
		Description: "Channel-negate ramp near clip boundary.",
		RenderEnd: func(d int) string {
			st := float64(d) - 0.5
			return fmt.Sprintf("negate=enable='gt(t,%f)'", st)
		},
		RenderStart: func(d int) string {
			return "negate=enable='lt(t,0.5)'"
		},
	},
	{
		Name:        "vignette",
		Description: "Vignette ramp near clip boundary.",
		RenderEnd: func(d int) string {
			st := float64(d) - 0.5
			return fmt.Sprintf("vignette=enable='gt(t,%f)'", st)
		},
		RenderStart: func(d int) string {
			return "vignette=enable='lt(t,0.5)'"
		},
	},
	{
		Name:        "fastblur",
		Description: "Heavier box-blur than `blur`.",
		RenderEnd: func(d int) string {
			st := float64(d) - 0.5
			return fmt.Sprintf("boxblur=30:enable='gt(t,%f)'", st)
		},
		RenderStart: func(d int) string {
			return "boxblur=30:enable='lt(t,0.5)'"
		},
	},
}

// fadeEndWithColor + fadeStartWithColor are tiny helpers that produce
// the fade-to-named-color pair. They keep the catalog compact while
// preserving distinct Transition entries (each has its own Name + slot
// in the rotation).
func fadeEndWithColor(color string) stockpipeline.TransitionRenderer {
	return func(d int) string {
		dur := 0.5
		st := float64(d) - dur
		return fmt.Sprintf("fade=t=out:st=%f:d=%f:color=%s", st, dur, color)
	}
}
func fadeStartWithColor(color string) stockpipeline.TransitionRenderer {
	return func(d int) string {
		dur := 0.5
		return fmt.Sprintf("fade=t=in:st=0:d=%f:color=%s", dur, color)
	}
}

// ── Registry implementation ────────────────────────────────────────

// DefaultTransitionRegistry returns a fresh registry preloaded with the
// canonical 15-entry catalog. Callers can add custom transitions via
// Register() before passing the registry to the FFmpeg renderer.
//
// PR6 completion (June 2026): moved from application layer to infrastructure.
// The composition root constructs this and injects it into the renderer.
func DefaultTransitionRegistry() stockpipeline.TransitionRegistry {
	r := &inMemoryTransitionRegistry{
		byName: make(map[string]stockpipeline.Transition, len(defaultCatalog)),
	}
	for _, t := range defaultCatalog {
		r.entries = append(r.entries, t)
		r.byName[t.Name] = t
	}
	return r
}

// inMemoryTransitionRegistry is the canonical exposition of TransitionRegistry.
// It is safe for concurrent reads after construction (Register stores
// under a mutex; All/Get/Len read a snapshot).
type inMemoryTransitionRegistry struct {
	mu      sync.RWMutex
	entries []stockpipeline.Transition // insertion-ordered
	byName  map[string]stockpipeline.Transition
}

// Register adds (or replaces) a transition entry. Safe under concurrent
// calls during bootstrap. Not safe to call during Render (would race with
// callers reading entries); production code should call this BEFORE wire.
func (r *inMemoryTransitionRegistry) Register(t stockpipeline.Transition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byName[t.Name]; !exists {
		r.entries = append(r.entries, t)
	}
	r.byName[t.Name] = t
}

// All returns a stable, insertion-ordered snapshot of the registered
// transitions. The infra renderer uses this snapshot to ROTATE through
// transitions deterministically per chunk index.
//
// PR6 fix (June 2026): removed the alphabetical sort — the port contract
// (stockpipeline.TransitionRegistry.All) specifies insertion order.
// Alphabetical sort was a post-PR6 regression that silently changed the
// historical rotation order.
func (r *inMemoryTransitionRegistry) All() []stockpipeline.Transition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]stockpipeline.Transition, len(r.entries))
	copy(out, r.entries)
	return out
}

// Get returns the transition registered under the given name.
func (r *inMemoryTransitionRegistry) Get(name string) (stockpipeline.Transition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.byName[name]
	return t, ok
}

// Len returns the number of registered transitions.
func (r *inMemoryTransitionRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// Compile-time guarantee that the registry implements the port interface.
var _ stockpipeline.TransitionRegistry = (*inMemoryTransitionRegistry)(nil)
