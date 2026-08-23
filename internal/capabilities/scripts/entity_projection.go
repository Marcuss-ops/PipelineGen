// Package scriptgeneration — entity_projection.go owns the deterministic
// projection of the incremental VidRush enrichment results onto the durable
// result's typed entity aggregate (persons / places / concepts). The durable
// runner consumes this helper after the final barrier so a SUCCEEDED run
// exposes the entities its extraction backend actually produced — the same
// typed buckets the batch flow projects into Artifacts.Entities.
package scriptgeneration

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// aggregateEntityResult merges the fenced per-scene VidRush segment results
// into one canonical EntityResult. Classification is deterministic and matches
// the legacy batch projection: PERSON → persons; LOCATION/PLACE/COUNTRY/CITY →
// places; every other type → concepts. Returns nil when no segment produced
// any entity (or no segments were passed), so the durable surface omits the
// block instead of exposing an empty aggregate.
func aggregateEntityResult(segments []scriptpkg.VidRushSegmentResult) *scriptpkg.EntityResult {
	agg := &scriptpkg.EntityResult{}
	for _, seg := range segments {
		for _, ent := range seg.Insights.Entities {
			value := strings.TrimSpace(ent.Value)
			if value == "" {
				continue
			}
			entity := scriptpkg.Entity{Value: value, Type: ent.Type, Score: float32(ent.Confidence)}
			switch strings.ToUpper(strings.TrimSpace(ent.Type)) {
			case "PERSON":
				agg.Persons = append(agg.Persons, entity)
			case "LOCATION", "PLACE", "COUNTRY", "CITY":
				agg.Places = append(agg.Places, entity)
			default:
				agg.Concepts = append(agg.Concepts, entity)
			}
		}
	}
	if len(agg.Persons)+len(agg.Places)+len(agg.Concepts) == 0 {
		return nil
	}
	return agg
}
