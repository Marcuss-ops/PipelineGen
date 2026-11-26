package scriptgeneration

import (
	"math"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/finaljob"
)

// typedRemoteScenes converts the historical map spelling used by the final-job
// fixtures into the typed scene envelope. It exists only in tests: production
// code builds FinalJobRemoteScene directly (CompositeVideoScene), so a missing
// or misspelled scene field is a compile error there. Fixtures keep the map
// form because a dozen literals across four files read better as data than as
// a struct with every field spelled out.
func typedRemoteScenes(maps []map[string]any) []finaljob.FinalJobRemoteScene {
	out := make([]finaljob.FinalJobRemoteScene, 0, len(maps))
	for _, m := range maps {
		scene := finaljob.FinalJobRemoteScene{}
		if v, ok := m["scene_id"].(string); ok {
			scene.SceneID = v
		}
		switch v := m["index"].(type) {
		case int:
			scene.Index = v
		case float64:
			scene.Index = int(math.Round(v))
		}
		if v, ok := m["kind"].(string); ok {
			scene.Kind = v
		}
		if v, ok := m["duration_seconds"].(float64); ok {
			scene.DurationSeconds = v
		}
		if v, ok := m["stock"].(map[string]any); ok {
			scene.Stock = v
		}
		if v, ok := m["text"].(string); ok {
			scene.Text = v
		}
		if v, ok := m["stock_pool_slot"].(bool); ok {
			scene.StockPoolSlot = v
		}
		out = append(out, scene)
	}
	return out
}
