package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// select_group is the second active mediasampler operation (MultiImage 2.0):
// it selects 2–5 distinct winners for one scene instead of the single
// `select` winner. The group-specific wire types are declared in this file
// so semantic_adapters.go keeps its single-winner contract untouched and
// stays under its size cap: the plain `select` request/response and the
// candidate struct (mediaSamplerCandidate, mediaSamplerScene,
// mediaSamplerResult) are shared from semantic_adapters.go.

// mediaSamplerGroupRequest mirrors the Rust SampleRequest for the
// select_group operation, including the per-request assignment ledger.
type mediaSamplerGroupRequest struct {
	Version    string                  `json:"version"`
	Operation  string                  `json:"operation"`
	Scene      mediaSamplerScene       `json:"scene"`
	Candidates []mediaSamplerCandidate `json:"candidates"`
	AllowReuse bool                    `json:"allow_reuse"`
	// GroupSize is the requested number of winners (2..=5).
	GroupSize int `json:"group_size"`
	// BoundAssets is the Go composition's authoritative already-assigned
	// ledger: entries with an owner other than this scene fail the
	// worker's reuse check as already_bound.
	BoundAssets []mediaSamplerBoundAsset `json:"bound_assets,omitempty"`
}

// mediaSamplerBoundAsset mirrors the Rust BoundEntry wire shape.
type mediaSamplerBoundAsset struct {
	AssetID string `json:"asset_id"`
	SceneID string `json:"scene_id"`
}

// mediaSamplerGroupResponse adds the ordered group members to the single
// `select` response shape; GroupIDs is present only for select_group.
type mediaSamplerGroupResponse struct {
	Results  []mediaSamplerResult `json:"results"`
	WinnerID *string              `json:"winner_id"`
	GroupIDs []string             `json:"group_ids"`
	Error    string               `json:"error,omitempty"`
}

// SelectGroup selects 2–5 distinct ranked winners for one scene through the
// Rust mediasampler's select_group operation.
//
// boundAssets carries the caller's authoritative already-assigned ledger
// (asset → owning segment): entries with an owner other than sceneID are
// refused by the worker as already_bound, so a group never repeats an asset
// from an earlier scene. The adapter fails closed on worker contract
// breaks: an empty id, a duplicated member, or a group wider than
// groupSize is an error, never a selection. The returned slice has at most
// groupSize entries: when fewer candidates pass the hard constraints the
// worker returns fewer members and the caller adapts the layout (x4 keeps
// working when the fifth image is missing) instead of padding with
// duplicates.
func (a *MediaSamplerAdapter) SelectGroup(
	ctx context.Context,
	sceneID, subject string,
	terms []string,
	candidates []mediaSamplerCandidate,
	allowReuse bool,
	groupSize int,
	boundAssets []mediaSamplerBoundAsset,
) (results []mediaSamplerResult, group []string, err error) {
	if a == nil || a.executor == nil {
		return nil, nil, fmt.Errorf("mediasampler: executor is not configured")
	}
	if !isValidGroupSize(groupSize) {
		return nil, nil, fmt.Errorf("mediasampler: select_group size must be 2..=5, got %d", groupSize)
	}
	req := mediaSamplerGroupRequest{
		Version:     "mediasampler.v1",
		Operation:   "select_group",
		Scene:       mediaSamplerScene{ID: sceneID, Subject: subject, Terms: terms},
		Candidates:  candidates,
		AllowReuse:  allowReuse,
		GroupSize:   groupSize,
		BoundAssets: boundAssets,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, nil, err
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("mediasampler: execute: %w", err)
	}
	var response mediaSamplerGroupResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, nil, fmt.Errorf("mediasampler: decode: %w", err)
	}
	if strings.TrimSpace(response.Error) != "" {
		return nil, nil, fmt.Errorf("mediasampler: worker: %s", response.Error)
	}
	group = make([]string, 0, len(response.GroupIDs))
	seen := make(map[string]struct{}, len(response.GroupIDs))
	for _, id := range response.GroupIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, nil, fmt.Errorf("mediasampler: worker returned an empty group member")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, nil, fmt.Errorf("mediasampler: worker returned duplicate group member %q", id)
		}
		seen[id] = struct{}{}
		group = append(group, id)
	}
	if len(group) > groupSize {
		return nil, nil, fmt.Errorf(
			"mediasampler: worker returned %d members for group_size %d", len(group), groupSize)
	}
	return response.Results, group, nil
}

func isValidGroupSize(size int) bool {
	return size >= 2 && size <= 5
}

// SampleGroup is the capability-facing MediaSamplerGroupPort: callers use
// kernel SegmentAssetCandidate and AssetBinding entries, receive the full
// winners back in rank order with the selection reason stamped, and never
// see the Rust wire candidates. The order of the returned slice matches the
// worker's group_ids order, so x2–x5 layouts can position members by rank.
func (a *MediaSamplerAdapter) SampleGroup(
	ctx context.Context,
	sceneID, subject string,
	terms []string,
	candidates []scriptpkg.SegmentAssetCandidate,
	allowReuse bool,
	groupSize int,
	bound []scriptports.AssetBinding,
) ([]scriptpkg.SegmentAssetCandidate, error) {
	input := make([]mediaSamplerCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		input = append(input, mediaSamplerCandidate{
			ID: candidate.AssetID, Label: textutil.FirstNonEmpty(candidate.Entity, candidate.Query),
			GenericSimilarity: float32(candidate.RelevanceScore), OwnerSegmentID: candidate.SegmentID,
		})
	}
	boundAssets := make([]mediaSamplerBoundAsset, 0, len(bound))
	for _, entry := range bound {
		boundAssets = append(boundAssets, mediaSamplerBoundAsset{AssetID: entry.AssetID, SceneID: entry.SceneID})
	}
	byID := make(map[string]scriptpkg.SegmentAssetCandidate, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.AssetID] = candidate
	}
	_, group, err := a.SelectGroup(ctx, sceneID, subject, terms, input, allowReuse, groupSize, boundAssets)
	if err != nil {
		return nil, err
	}
	winners := make([]scriptpkg.SegmentAssetCandidate, 0, len(group))
	for _, id := range group {
		winner, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("mediasampler: worker returned unknown group member %q", id)
		}
		winner.SelectionReason = "MediaSampler selected verified candidate (group)"
		winners = append(winners, winner)
	}
	return winners, nil
}

var _ scriptports.MediaSamplerGroupPort = (*MediaSamplerAdapter)(nil)
