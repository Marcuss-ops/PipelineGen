package adapters

import (
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type vidRushProviderOutcome struct {
	provider     string
	candidates   []scriptpkg.SegmentAssetCandidate
	primary      *scriptpkg.SegmentAssetCandidate
	allCacheHits bool
	err          error
}

type vidRushFanoutPlan struct {
	segmentID         string
	textHash          string
	text              string
	title             string
	artlistIntentHash string
	artlistQueries    []string
	imageQueries      []string
	firstEntity       string
	youtubeSources    []scriptports.VidRushSourceHint
	perQueryLimit     int
	artlistEnabled    bool
	imagesEnabled     bool
	youtubeEnabled    bool
}
