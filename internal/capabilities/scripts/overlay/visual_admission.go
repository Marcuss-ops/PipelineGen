package overlay

// Visual Admission Resolver (P0, full module — NOT wired into production).
//
// Single owner of admission decisions: priority (requested beats extracted),
// per-scene budget, and temporal collision handling over certified windows.
// Deterministic: same candidates in any input order yield the same set.
// Wiring this into overlay production belongs to the explicit visual
// integration phase; these tests pin the contract in advance.

import (
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VisualKinds admitted by the resolver.
const (
	VisualChart     = "chart"
	VisualImage     = "image"
	VisualHighlight = "highlight"
	VisualBullet    = "bullet"
)

// VisualCandidate is one immutable contender for visual slots.
type VisualCandidate struct {
	CanonicalID string
	Kind        string // chart | image | highlight | bullet
	SceneID     string
	Requested   bool // explicit caller input beats automatic extraction
	Window      *scriptpkg.TimelineWindow
	Score       float64
}

// AdmittedVisual is one selected element.
type AdmittedVisual struct {
	CanonicalID string `json:"canonical_id"`
	Kind        string `json:"kind"`
	SceneID     string `json:"scene_id"`
	Requested   bool   `json:"requested"`
}

// AdmissionBudget bounds the slots. Zero means the certified default.
type AdmissionBudget struct {
	MaxPerScene   int
	MaxCharts     int
	MaxImages     int
	MaxHighlights int
}

func defaultAdmissionBudget() AdmissionBudget {
	return AdmissionBudget{MaxPerScene: 3, MaxCharts: 1, MaxImages: 2, MaxHighlights: 2}
}

// AdmitVisuals resolves admission for one scene. Rules:
//  1. Requested candidates outrank extracted ones (stable: requested first,
//     then score desc, then canonical id).
//  2. Per-kind caps and the per-scene cap bound the set.
//  3. Two admitted windows of the SAME kind must not overlap; the loser is
//     dropped (requested still beats extracted on collision).
//  4. Candidates without a certified window are data only, never admitted.
func AdmitVisuals(candidates []VisualCandidate, budget AdmissionBudget) []AdmittedVisual {
	if budget.MaxPerScene <= 0 {
		budget = defaultAdmissionBudget()
	}
	if budget.MaxCharts <= 0 {
		budget.MaxCharts = 1
	}
	if budget.MaxImages <= 0 {
		budget.MaxImages = 2
	}
	if budget.MaxHighlights <= 0 {
		budget.MaxHighlights = 2
	}
	ordered := append([]VisualCandidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Requested != ordered[j].Requested {
			return ordered[i].Requested
		}
		if ordered[i].Score != ordered[j].Score {
			return ordered[i].Score > ordered[j].Score
		}
		return ordered[i].CanonicalID < ordered[j].CanonicalID
	})
	kindCount := map[string]int{}
	admitted := []AdmittedVisual{}
	windows := map[string][]scriptpkg.TimelineWindow{}
	capFor := func(kind string) int {
		switch kind {
		case VisualChart:
			return budget.MaxCharts
		case VisualImage:
			return budget.MaxImages
		case VisualHighlight:
			return budget.MaxHighlights
		default:
			return budget.MaxPerScene
		}
	}
	for _, c := range ordered {
		if len(admitted) >= budget.MaxPerScene {
			break
		}
		if kindCount[c.Kind] >= capFor(c.Kind) {
			continue
		}
		if c.Window == nil {
			continue // data only, never admitted without certified timing
		}
		collides := false
		for _, w := range windows[c.Kind] {
			if scriptpkg.Overlaps(*c.Window, w) {
				collides = true
				break
			}
		}
		if collides {
			continue
		}
		admitted = append(admitted, AdmittedVisual{
			CanonicalID: c.CanonicalID, Kind: c.Kind, SceneID: c.SceneID, Requested: c.Requested,
		})
		kindCount[c.Kind]++
		windows[c.Kind] = append(windows[c.Kind], *c.Window)
	}
	if admitted == nil {
		admitted = []AdmittedVisual{}
	}
	return admitted
}

// DedupCandidateTexts drops extracted candidates whose text duplicates a
// requested one (case-insensitive), so one admission never shows both.
func DedupCandidateTexts(requestedTexts []string, extracted []VisualCandidate, textOf func(VisualCandidate) string) []VisualCandidate {
	taken := map[string]struct{}{}
	for _, t := range requestedTexts {
		taken[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	out := extracted[:0]
	for _, c := range extracted {
		if _, dup := taken[strings.ToLower(strings.TrimSpace(textOf(c)))]; dup {
			continue
		}
		out = append(out, c)
	}
	return out
}
