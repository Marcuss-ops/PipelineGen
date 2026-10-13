// Package overlay — recap_placement.go maps the scene sequence to recap
// slots. Moved from the root scripts package (2026-10-13): the root package
// is the compatibility facade and each overlay family migrates into this
// subpackage. The registered scripts hotspot must never gain production
// files, so overlay policy lives here; behaviour is unchanged.
package overlay

import (
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// RecapSceneBrief is one timed scene's recap projection (moved from the root
// scripts package 2026-10-13; behaviour unchanged): its top phrase
// stands in for the chapter bullet, and its canonical window bounds the
// recap cards shown when a later placement scene begins.
type RecapSceneBrief struct {
	ID      string
	Title   string
	Score   float64
	StartUS int64
	EndUS   int64
}

// recapPlacementsForScenes maps the scene sequence to recap slots. Placement
// chapters come from capabilityoverlay.RecapPlacementPoints (R01: after several chapters;
// R02: short runs yield none). A point p displays at the start of scene
// p+1 covering scenes 0..p — never the scene being entered (R03: no leak
// of not-yet-narrated content). Scene ordinals are the narration index
// space, so later scenes always sort after the boundary. A display scene
// already taken by an earlier point is skipped, keeping card windows
// disjoint. Deterministic: same briefs → same placements.
func RecapPlacementsForScenes(briefs []RecapSceneBrief) []capabilityoverlay.RecapPlacement {
	points := capabilityoverlay.RecapPlacementPoints(len(briefs), false)
	usedDisplay := map[int]struct{}{}
	var out []capabilityoverlay.RecapPlacement
	for _, p := range points {
		display := p + 1
		if display > len(briefs)-1 {
			display = len(briefs) - 1
		}
		if display < 1 {
			continue
		}
		if _, taken := usedDisplay[display]; taken {
			continue
		}
		usedDisplay[display] = struct{}{}
		narratedThrough := display - 1
		var sources []capabilityoverlay.RecapSource
		for i := 0; i <= narratedThrough && i < len(briefs); i++ {
			if strings.TrimSpace(briefs[i].Title) == "" {
				continue
			}
			sources = append(sources, capabilityoverlay.RecapSource{
				ID: briefs[i].ID, Title: briefs[i].Title,
				SentenceIDs: []int{i}, Score: briefs[i].Score,
			})
		}
		w := briefs[display]
		if w.StartUS >= w.EndUS {
			continue
		}
		out = append(out, capabilityoverlay.RecapPlacement{
			SceneID: w.ID, WindowStartUS: w.StartUS, WindowEndUS: w.EndUS,
			Sources: sources, NarratedThrough: narratedThrough, MaxCards: 3,
		})
	}
	return out
}
