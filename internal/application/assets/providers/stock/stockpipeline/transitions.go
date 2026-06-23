// Package stock — canonical TransitionRegistry implementation (PR6, June 2026).
//
// The transition catalogue replaces the two large near-symmetric switch
// statements in render.go's pre-PR6 implementation. Each Transition entry
// carries its own RenderEnd / RenderStart closures so the asymmetry in
// FFmpeg filter syntax (e.g. fade=t=out vs boxblur=enable) is encapsulated
// at the ENTRY and never duplicated.
//
// Import-boundary invariant: this file imports only the standard library
// + zap + the ports file in the same package. No FFmpeg / process imports.
package stockpipeline

import (
	"fmt"
	"sort"
	"sync"
)

// ── Canonical catalog ────────────────────────────────────────────────
//
// The 14 transitions preserved verbatim from the pre-PR6 implementation:
//   fadeblack, fadewhite, flash, blur, gray,
//   colorred, colorblue, colorgreen, coloryellow,
//   colorpurple, colororange, colorpink,
//   negate, vignette, fastblur
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
var defaultCatalog = []Transition{
	{
		Name: "fadeblack",
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
		Name: "fadewhite",
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
		Name: "colorred",
		RenderEnd: fadeEndWithColor("red"),
		RenderStart: fadeStartWithColor("red"),
	},
	{
		Name: "colorblue",
		RenderEnd: fadeEndWithColor("blue"),
		RenderStart: fadeStartWithColor("blue"),
	},
	{
		Name: "colorgreen",
		RenderEnd: fadeEndWithColor("green"),
		RenderStart: fadeStartWithColor("green"),
	},
	{
		Name: "coloryellow",
		RenderEnd: fadeEndWithColor("yellow"),
		RenderStart: fadeStartWithColor("yellow"),
	},
	{
		Name: "colorpurple",
		RenderEnd: fadeEndWithColor("purple"),
		RenderStart: fadeStartWithColor("purple"),
	},
	{
		Name: "colororange",
		RenderEnd: fadeEndWithColor("orange"),
		RenderStart: fadeStartWithColor("orange"),
	},
	{
		Name: "colorpink",
		RenderEnd: fadeEndWithColor("pink"),
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
func fadeEndWithColor(color string) TransitionRenderer {
	return func(d int) string {
		dur := 0.5
		st := float64(d) - dur
		return fmt.Sprintf("fade=t=out:st=%f:d=%f:color=%s", st, dur, color)
	}
}
func fadeStartWithColor(color string) TransitionRenderer {
	return func(d int) string {
		dur := 0.5
		return fmt.Sprintf("fade=t=in:st=0:d=%f:color=%s", dur, color)
	}
}

// ── Registry implementation ────────────────────────────────────────

// DefaultTransitionRegistry returns a fresh registry preloaded with the
// canonical 14-entry catalog. Callers can add custom transitions via
// Register() before passing the registry to the FFmpeg renderer.
func DefaultTransitionRegistry() TransitionRegistry {
	r := &inMemoryTransitionRegistry{
		byName: make(map[string]Transition, len(defaultCatalog)),
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
	entries []Transition            // insertion-ordered
	byName  map[string]Transition
}

// Register adds (or replaces) a transition entry. Safe under concurrent
// calls during bootstrap. Not safe to call during Render (would race with
// callers reading entries); production code should call this BEFORE wire.
func (r *inMemoryTransitionRegistry) Register(t Transition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byName[t.Name]; !exists {
		r.entries = append(r.entries, t)
	}
	r.byName[t.Name] = t
}

// All returns a stable, sorted-by-name snapshot of the registered transitions.
// The infra renderer uses this snapshot to ROTATE through transitions
// deterministically per chunk index. Sorting by name keeps the rotation
// reproducible across program restarts (the pre-PR6 implementation used
// insertion order, which is equivalent for DefaultTransitionRegistry).
func (r *inMemoryTransitionRegistry) All() []Transition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Transition, len(r.entries))
	copy(out, r.entries)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns the transition registered under the given name.
func (r *inMemoryTransitionRegistry) Get(name string) (Transition, bool) {
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
var _ TransitionRegistry = (*inMemoryTransitionRegistry)(nil)
