package entities

import "context"

// EntityOverlayRankingOccurrence is the minimal grounded input BrainRust needs
// to rank one named overlay. OverlayID is computed by the presentation owner
// so Rust returns stable indices without owning Go render DTOs.
type EntityOverlayRankingOccurrence struct {
	EntityID   string  `json:"entity_id"`
	OverlayID  string  `json:"overlay_id"`
	EntityType string  `json:"entity_type"`
	Confidence float64 `json:"confidence"`
}

type EntityOverlayRankingScene struct {
	Entities []EntityOverlayRankingOccurrence `json:"entities"`
}

// EntityOverlayRanker owns run-frequency scoring, stable ranking and
// per-scene overlay-identity deduplication in BrainRust.
type EntityOverlayRanker interface {
	RankEntityOverlays(context.Context, []EntityOverlayRankingScene) ([][]int, error)
}
