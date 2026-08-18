package entities

// EntityRankInput carries the five normalized [0,1] components of the
// canonical entity importance score (the plan's ranking formula):
//
//	importance =
//	    0.30 * entity_type_weight
//	  + 0.25 * frequency
//	  + 0.20 * scene_relevance
//	  + 0.15 * novelty
//	  + 0.10 * asset_quality
//
// Every component is normalized to [0,1]; the weighted sum is itself in
// [0,1]. The score is a pure function of the input — no wall clock, no
// randomness — so the same entity in the same context always ranks the same.
type EntityRankInput struct {
	// EntityTypeWeight is the editorial weight of the entity type
	// (PERSON > ORGANIZATION > LOCATION > CONCEPT...), in [0,1].
	EntityTypeWeight float64
	// Frequency is how often the entity occurs across the video, normalized
	// to [0,1] (1.0 = the most frequent entity in the run).
	Frequency float64
	// SceneRelevance is how central the entity is to THIS scene, in [0,1].
	SceneRelevance float64
	// Novelty is how new the entity is to the video (a single fresh mention
	// = 1.0; recurring entities decay toward 0), in [0,1].
	Novelty float64
	// AssetQuality is the quality of the best bound asset, in [0,1] (0 when
	// the entity has no asset).
	AssetQuality float64
}

// Importance weights of the canonical ranking formula (sum = 1.0).
const (
	WeightEntityType     = 0.30
	WeightFrequency      = 0.25
	WeightSceneRelevance = 0.20
	WeightNovelty        = 0.15
	WeightAssetQuality   = 0.10
)

// ImportanceScore computes the canonical entity importance score in [0,1]
// from the five normalized components. Every component is clamped to [0,1]
// so a caller can never push the score out of range.
func ImportanceScore(in EntityRankInput) float64 {
	clamp := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	return WeightEntityType*clamp(in.EntityTypeWeight) +
		WeightFrequency*clamp(in.Frequency) +
		WeightSceneRelevance*clamp(in.SceneRelevance) +
		WeightNovelty*clamp(in.Novelty) +
		WeightAssetQuality*clamp(in.AssetQuality)
}

// EntityTypeWeight returns the editorial type weight of an entity type for
// ranking purposes, in [0,1]. People are the most valuable overlay subjects,
// followed by organizations, then locations and concepts.
func EntityTypeWeight(entityType string) float64 {
	switch NormalizeType(entityType) {
	case "PERSON", "CEO", "FOUNDER", "PRESIDENT":
		return 1.0
	case "ORGANIZATION", "ORG", "COMPANY", "BRAND":
		return 0.9
	case "GPE", "LOCATION", "CITY", "COUNTRY":
		return 0.8
	case "PRODUCT", "LOGO":
		return 0.75
	case "CONCEPT", "EVENT", "IDEA":
		return 0.65
	case "NUMBER", "MONEY", "PERCENT", "QUOTE":
		return 0.55
	default:
		return 0.5
	}
}

// RankedEntity is one entity occurrence with its computed importance score.
type RankedEntity struct {
	Occurrence EntityOccurrence
	Importance float64
}

// RankConfig bounds the entity ranking of one scene. Zero means "no cap".
// (The phrase/word/image caps of the plan live in the overlays planner's
// PlannerConfig — this package only caps entity overlays.)
type RankConfig struct {
	// MaxEntityOverlaysPerScene caps how many entity overlays a scene may
	// carry (the plan's example: 2). The top-N by importance survive.
	MaxEntityOverlaysPerScene int
}

// RankScene ranks the entity occurrences of ONE scene by importance and
// applies the per-scene caps from cfg. The scene frequency/normality context
// (frequency, novelty, asset quality) is derived from the whole run by the
// caller via RankContext.
func RankScene(occurrences []EntityOccurrence, ctx RankContext, cfg RankConfig) []RankedEntity {
	frequency := ctx.Frequency
	if frequency == nil {
		frequency = func(string) float64 { return 0 }
	}
	sceneRelevance := ctx.SceneRelevance
	if sceneRelevance == nil {
		sceneRelevance = func(occ EntityOccurrence) float64 {
			if occ.Confidence > 0 {
				return occ.Confidence
			}
			return 0.5
		}
	}
	novelty := ctx.Novelty
	if novelty == nil {
		novelty = func(string) float64 { return 1.0 }
	}
	assetQuality := ctx.AssetQuality
	if assetQuality == nil {
		assetQuality = func(string) float64 { return 0 }
	}
	ranked := make([]RankedEntity, 0, len(occurrences))
	for _, occ := range occurrences {
		ranked = append(ranked, RankedEntity{
			Occurrence: occ,
			Importance: ImportanceScore(EntityRankInput{
				EntityTypeWeight: EntityTypeWeight(occ.Type),
				Frequency:        frequency(occ.EntityID),
				SceneRelevance:   sceneRelevance(occ),
				Novelty:          novelty(occ.EntityID),
				AssetQuality:     assetQuality(occ.EntityID),
			}),
		})
	}
	// Rank by importance descending; ties keep the timeline order
	// (deterministic, stable).
	sortRanked(ranked)
	if cfg.MaxEntityOverlaysPerScene > 0 && len(ranked) > cfg.MaxEntityOverlaysPerScene {
		ranked = ranked[:cfg.MaxEntityOverlaysPerScene]
	}
	return ranked
}

// RankContext supplies the run-level components of the importance score.
// The plan splits the derivation: PipelineGen decides WHO is important
// (frequency across the run, novelty of first mention, bound asset quality);
// the per-scene ranking then combines those run-level facts with the scene
// relevance of each occurrence.
type RankContext struct {
	// Frequency returns how often the entity occurs in the run, normalized
	// to [0,1]. Default: 1/maxFrequency when nil.
	Frequency func(entityID string) float64
	// SceneRelevance returns how central the occurrence is to its scene,
	// normalized to [0,1]. Default: the occurrence confidence.
	SceneRelevance func(occ EntityOccurrence) float64
	// Novelty returns how new the entity is to the run. Default: 1.0 when
	// the entity occurs exactly once in the run (a fresh mention), 0.5 when
	// it recurs anywhere (already established).
	Novelty func(entityID string) float64
	// AssetQuality returns the quality of the entity's best bound asset,
	// normalized to [0,1]. Default: 0 (no asset).
	AssetQuality func(entityID string) float64
}

// NewRankContext builds the default run-level context: frequency normalized
// by the most frequent entity, novelty = first-mention bonus, scene relevance
// = occurrence confidence, asset quality = 0 unless overridden.
func NewRankContext(occurrences []EntityOccurrence) RankContext {
	freq := map[string]int{}
	var maxFreq int
	for _, occ := range occurrences {
		freq[occ.EntityID]++
		if freq[occ.EntityID] > maxFreq {
			maxFreq = freq[occ.EntityID]
		}
	}
	ctx := RankContext{
		Frequency: func(entityID string) float64 {
			if maxFreq == 0 {
				return 0
			}
			return float64(freq[entityID]) / float64(maxFreq)
		},
		Novelty: func(entityID string) float64 {
			if freq[entityID] == 1 {
				return 1.0
			}
			return 0.5
		},
		SceneRelevance: func(occ EntityOccurrence) float64 {
			if occ.Confidence > 0 {
				return occ.Confidence
			}
			return 0.5
		},
	}
	return ctx
}

// sortRanked orders ranked entities by importance descending, stable (ties
// keep input order = timeline order).
func sortRanked(ranked []RankedEntity) {
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && ranked[j].Importance > ranked[j-1].Importance; j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
}
