// Package rustexec — media_ranker_adapter.go is the Go boundary of the
// canonical media ranker that now lives in `mediasampler::rank` (TICKET-16).
//
// The adapter sends one `rank_candidates` request per slot pool and validates
// the answer. It deliberately does NOT re-rank, re-gate or re-score: the only
// thing it owns is the wire contract and the fail-closed checks that a
// malformed worker answer cannot be consumed as a plan.
package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	kmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// MediaRankerBinding is the operator-curated envelope the ranker reads.
type MediaRankerBinding struct {
	ID             string  `json:"id,omitempty"`
	AssetID        string  `json:"asset_id,omitempty"`
	StartMS        int64   `json:"start_ms,omitempty"`
	EndMS          int64   `json:"end_ms,omitempty"`
	ApprovalStatus string  `json:"approval_status,omitempty"`
	ManualScore    float64 `json:"manual_score,omitempty"`
	SemanticScore  float64 `json:"semantic_score,omitempty"`
	QualityScore   float64 `json:"quality_score,omitempty"`
	SuccessScore   float64 `json:"success_score,omitempty"`
}

// MediaRankerCandidate is one candidate as the Rust ranker sees it.
type MediaRankerCandidate struct {
	CandidateID           string              `json:"candidate_id,omitempty"`
	AssetID               string              `json:"asset_id,omitempty"`
	VideoID               string              `json:"video_id,omitempty"`
	Provider              string              `json:"provider,omitempty"`
	SourceURL             string              `json:"source_url,omitempty"`
	MediaType             string              `json:"media_type,omitempty"`
	DurationMS            int64               `json:"duration_ms,omitempty"`
	CandidateScore        float64             `json:"candidate_score,omitempty"`
	RightsStatus          string              `json:"rights_status,omitempty"`
	DiscoveryStatus       string              `json:"discovery_status,omitempty"`
	MaterializationStatus string              `json:"materialization_status,omitempty"`
	ChannelID             string              `json:"channel_id,omitempty"`
	Binding               *MediaRankerBinding `json:"binding,omitempty"`
	// Bonus is the caller's ordering preference. The ranker adds it after the
	// verdict is classified: it can reorder survivors, never rescue a drop.
	Bonus float64 `json:"bonus,omitempty"`
}

// MediaRankerRequest is one slot pool: scene context plus candidates.
type MediaRankerRequest struct {
	SceneDurationMS int64                  `json:"scene_duration_ms"`
	PrevVideoID     string                 `json:"prev_video_id,omitempty"`
	SlotKind        string                 `json:"slot_kind"`
	Candidates      []MediaRankerCandidate `json:"candidates"`
}

type mediaRankerWireRequest struct {
	Version   string `json:"version"`
	Operation string `json:"operation"`
	MediaRankerRequest
}

// MediaRankerScoreBreakdown carries every seat plus both penalties so the
// caller can audit a score instead of trusting a number.
type MediaRankerScoreBreakdown struct {
	Semantic          float64 `json:"semantic"`
	ExactMatch        float64 `json:"exact_match"`
	Visual            float64 `json:"visual"`
	ManualApproval    float64 `json:"manual_approval"`
	Quality           float64 `json:"quality"`
	HistoricalSuccess float64 `json:"historical_success"`
	DurationFit       float64 `json:"duration_fit"`
	RepetitionPenalty float64 `json:"repetition_penalty"`
	RightsPenalty     float64 `json:"rights_penalty"`
}

// MediaRankerRanked is one surviving candidate in rank order.
type MediaRankerRanked struct {
	AssetID      string                    `json:"asset_id"`
	CandidateID  string                    `json:"candidate_id"`
	SourceURL    string                    `json:"source_url"`
	Provider     string                    `json:"provider"`
	MediaType    string                    `json:"media_type"`
	DurationMS   int64                     `json:"duration_ms"`
	RightsStatus string                    `json:"rights_status"`
	FinalScore   float64                   `json:"final_score"`
	Verdict      string                    `json:"verdict"`
	Reason       string                    `json:"reason"`
	Breakdown    MediaRankerScoreBreakdown `json:"breakdown"`
}

// MediaRankerDrop is one removed candidate with the gate that removed it.
type MediaRankerDrop struct {
	AssetID     string `json:"asset_id"`
	CandidateID string `json:"candidate_id"`
	Gate        string `json:"gate,omitempty"`
	Reason      string `json:"reason"`
}

// MediaRankerOutcome is the full per-slot answer.
type MediaRankerOutcome struct {
	Ranked []MediaRankerRanked `json:"ranked"`
	Drops  []MediaRankerDrop   `json:"drops"`
	Winner *MediaRankerRanked  `json:"winner,omitempty"`
	Error  string              `json:"error,omitempty"`
}

// MediaRankerAdapter talks to the persistent mediasampler worker.
type MediaRankerAdapter struct {
	executor *Executor
}

func NewMediaRankerAdapter(executor *Executor) (*MediaRankerAdapter, error) {
	if executor == nil {
		return nil, fmt.Errorf("mediasampler ranker: executor is required")
	}
	return &MediaRankerAdapter{executor: executor}, nil
}

// mediaRankerSlotKinds is the accepted slot vocabulary. "any" is the neutral
// value for a pool ranked before a slot is assigned (the Brain path): it
// disables the aspect gate instead of pretending the pool belongs to one slot.
var mediaRankerSlotKinds = map[string]struct{}{
	string(kmedia.SlotPrimaryVideo):    {},
	string(kmedia.SlotSecondaryImage):  {},
	string(kmedia.SlotEvidenceOverlay): {},
	string(kmedia.SlotMap):             {},
	string(kmedia.SlotPortrait):        {},
	string(kmedia.SlotDocument):        {},
	string(kmedia.SlotBackground):      {},
	"any":                              {},
}

// RankCandidates asks the Rust ranker for one slot pool and validates the
// answer before returning it.
func (a *MediaRankerAdapter) RankCandidates(ctx context.Context, request MediaRankerRequest) (MediaRankerOutcome, error) {
	if a == nil || a.executor == nil {
		return MediaRankerOutcome{}, fmt.Errorf("mediasampler ranker: executor is not configured")
	}
	if _, ok := mediaRankerSlotKinds[request.SlotKind]; !ok {
		return MediaRankerOutcome{}, fmt.Errorf("mediasampler ranker: unknown slot_kind %q", request.SlotKind)
	}
	if len(request.Candidates) == 0 {
		return MediaRankerOutcome{Ranked: []MediaRankerRanked{}, Drops: []MediaRankerDrop{}}, nil
	}
	payload, err := json.Marshal(mediaRankerWireRequest{
		Version:            "mediasampler.v1",
		Operation:          "rank_candidates",
		MediaRankerRequest: request,
	})
	if err != nil {
		return MediaRankerOutcome{}, fmt.Errorf("mediasampler ranker encode: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return MediaRankerOutcome{}, fmt.Errorf("mediasampler ranker execute: %w", err)
	}
	var outcome MediaRankerOutcome
	if err := json.Unmarshal(stdout, &outcome); err != nil {
		return MediaRankerOutcome{}, fmt.Errorf("mediasampler ranker decode: %w", err)
	}
	if strings.TrimSpace(outcome.Error) != "" {
		return MediaRankerOutcome{}, fmt.Errorf("mediasampler ranker worker: %s", outcome.Error)
	}
	if err := validateMediaRankerOutcome(request, outcome); err != nil {
		return MediaRankerOutcome{}, err
	}
	return outcome, nil
}

// validateMediaRankerOutcome rejects an answer that cannot be a ranking of the
// request: a non-finite score, a broken order, a winner outside the selection,
// or a candidate that neither survived nor was dropped.
func validateMediaRankerOutcome(request MediaRankerRequest, outcome MediaRankerOutcome) error {
	seen := make(map[string]int, len(request.Candidates))
	for _, candidate := range request.Candidates {
		seen[candidate.CandidateID]++
	}
	accounted := make(map[string]int, len(request.Candidates))
	previous := MediaRankerRanked{}
	for index, ranked := range outcome.Ranked {
		if math.IsNaN(ranked.FinalScore) || math.IsInf(ranked.FinalScore, 0) {
			return fmt.Errorf("mediasampler ranker: non-finite score for %q", ranked.AssetID)
		}
		if index > 0 {
			if ranked.FinalScore > previous.FinalScore {
				return fmt.Errorf("mediasampler ranker: order breaks on %q (%.9f after %.9f)",
					ranked.AssetID, ranked.FinalScore, previous.FinalScore)
			}
			if ranked.FinalScore == previous.FinalScore && ranked.AssetID < previous.AssetID {
				return fmt.Errorf("mediasampler ranker: tie-break breaks on %q after %q",
					ranked.AssetID, previous.AssetID)
			}
		}
		previous = ranked
		accounted[ranked.CandidateID]++
	}
	for _, drop := range outcome.Drops {
		accounted[drop.CandidateID]++
	}
	for candidateID, want := range seen {
		if accounted[candidateID] != want {
			return fmt.Errorf("mediasampler ranker: candidate %q appears %d time(s), want %d",
				candidateID, accounted[candidateID], want)
		}
	}
	if outcome.Winner != nil {
		found := false
		for _, ranked := range outcome.Ranked {
			if ranked.AssetID == outcome.Winner.AssetID &&
				ranked.CandidateID == outcome.Winner.CandidateID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("mediasampler ranker: winner %q is not part of the ranked selection",
				outcome.Winner.AssetID)
		}
	}
	return nil
}
