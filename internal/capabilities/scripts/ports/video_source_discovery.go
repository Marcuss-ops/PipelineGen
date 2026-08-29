// Package ports defines the VideoSourceDiscovery port: autonomous video
// discovery for VidRush providers. The small model decides WHAT to search
// for; discovery finds candidate videos; ranking and download stay with the
// canonical StockService pipeline.
package ports

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
)

// ErrNoDiscoveryCandidates is returned when discovery produced no usable
// video candidates for the requested queries.
var ErrNoDiscoveryCandidates = errors.New("video source discovery: no candidates")

// VideoSourceDiscoveryRequest carries the discovery intent for one segment.
// Queries are built deterministically from the segment semantic profile; the
// discovery adapter owns transport (YouTube search API or equivalent).
type VideoSourceDiscoveryRequest struct {
	// SegmentID bounds the discovery to one segment for cache isolation.
	SegmentID string
	// Queries are focused search phrases (exact subject, context, fallback).
	Queries []string
	// Language is the preferred content language (BCP-47, e.g. "en").
	Language string
	// MaxVideos caps the candidate count across all queries after dedupe.
	MaxVideos int
	// MinVideoDurationMs filters out videos too short to host the beat.
	MinVideoDurationMs int64
	// ExcludeLive filters live streams, whose timing windows are unstable.
	ExcludeLive bool
	// QueryPlan is the weighted per-provider query translation of the
	// segment semantic profile. Optional: backends that only accept a
	// flat phrase list fall back to Queries.
	QueryPlan *ProviderQueryPlan
}

// VideoSourceCandidate is one discovered video, not yet planned or
// downloaded. Metadata scoring happens here; transcript timing stays in
// StockService.Plan.
type VideoSourceCandidate struct {
	// Provider names the discovery backend (e.g. "youtube").
	Provider string
	// VideoID is the backend-native identity used for dedupe and caching.
	VideoID string
	// URL is the canonical watch URL handed to StockService.Plan.
	URL string
	// Title is the raw video title, used by deterministic ranking.
	Title string
	// DurationMs is the full video length when known (0 = unknown).
	DurationMs int64
	// Query records which discovery query produced this candidate.
	Query string
	// Rank is the per-query result position (0-based).
	Rank int
	// MetadataScore is the deterministic metadata relevance in [0, 1].
	MetadataScore float64
}

// Query intents for ProviderQuery, ordered from most to least specific.
// The intent records WHY the query builder emitted the phrase so ranking
// and logging can distinguish a direct subject hit from a visual fallback.
type QueryIntent string

const (
	// QueryIntentExactSubject names the segment's subject directly
	// (e.g. "John Froelich gasoline tractor").
	QueryIntentExactSubject QueryIntent = "exact_subject"
	// QueryIntentHistoricalContext adds temporal/place context
	// (e.g. "first gasoline tractor 1892").
	QueryIntentHistoricalContext QueryIntent = "historical_context"
	// QueryIntentVisualFallback is a broader visual phrase used when the
	// subject queries return nothing (e.g. "historic tractor footage").
	QueryIntentVisualFallback QueryIntent = "visual_fallback"
)

// ProviderQuery is one focused search phrase with its intent and weight.
// Weight is a deterministic priority in (0, 1]; higher wins. It is NOT a
// relevance prediction — ranking stays with the deterministic scorer and
// the small-model reranker downstream.
type ProviderQuery struct {
	Query  string      `json:"query"`
	Intent QueryIntent `json:"intent"`
	Weight float64     `json:"weight"`
}

// ProviderQueryPlan is the per-provider query translation of one segment
// semantic profile: 3-5 focused phrases, deduplicated, weights sorted
// non-increasing. Focused phrases beat one long concatenated query on
// every search backend this port has been pointed at.
type ProviderQueryPlan struct {
	// Provider is the consuming provider name (e.g. "youtube").
	Provider string `json:"provider"`
	// Queries is the ordered, weighted phrase list (3-5 entries).
	Queries []ProviderQuery `json:"queries"`
}

// Validate enforces the plan invariants: non-empty queries, weights in
// (0, 1], and non-increasing weight order (most specific first).
func (p ProviderQueryPlan) Validate() error {
	if strings.TrimSpace(p.Provider) == "" {
		return errors.New("provider query plan: provider is required")
	}
	if len(p.Queries) == 0 {
		return errors.New("provider query plan: at least one query is required")
	}
	previous := math.Inf(1)
	for i, query := range p.Queries {
		if strings.TrimSpace(query.Query) == "" {
			return fmt.Errorf("provider query plan: queries[%d].query is required", i)
		}
		if math.IsNaN(query.Weight) || math.IsInf(query.Weight, 0) || query.Weight <= 0 || query.Weight > 1 {
			return fmt.Errorf("provider query plan: queries[%d].weight must be in (0, 1]", i)
		}
		if query.Weight > previous {
			return fmt.Errorf("provider query plan: queries[%d].weight breaks non-increasing order", i)
		}
		previous = query.Weight
	}
	return nil
}

// Phrases projects the plan to its flat query strings, in weight order.
// This is the shape consumed by backends that only accept a phrase list.
func (p ProviderQueryPlan) Phrases() []string {
	out := make([]string, 0, len(p.Queries))
	for _, query := range p.Queries {
		out = append(out, query.Query)
	}
	return out
}

// VideoSourceDiscovery finds candidate videos for a segment without
// downloading anything. Implementations must be side-effect free with
// respect to media storage: no downloads, no Drive uploads, no SQLite rows.
type VideoSourceDiscovery interface {
	Discover(ctx context.Context, req VideoSourceDiscoveryRequest) ([]VideoSourceCandidate, error)
}
