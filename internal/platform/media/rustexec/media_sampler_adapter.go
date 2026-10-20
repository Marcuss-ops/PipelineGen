// Package rustexec — media_sampler_adapter.go owns the capability-facing
// MediaSampler adapter over the persistent Rust sampler. Split out of
// semantic_adapters.go to keep that file under the strict 600-LOC gate; same
// package, same behaviour.
package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func NewMediaSamplerAdapter(executor *Executor) (*MediaSamplerAdapter, error) {
	if executor == nil {
		return nil, fmt.Errorf("mediasampler: executor is required")
	}
	return &MediaSamplerAdapter{executor: executor}, nil
}

func (a *MediaSamplerAdapter) SampleScene(ctx context.Context, sceneID, subject string, terms []string, candidates []mediaSamplerCandidate, allowReuse bool) ([]mediaSamplerResult, string, error) {
	if a == nil || a.executor == nil {
		return nil, "", fmt.Errorf("mediasampler: executor is not configured")
	}
	req := mediaSamplerRequest{Version: "mediasampler.v1", Operation: "select", Scene: mediaSamplerScene{ID: sceneID, Subject: subject, Terms: terms}, Candidates: candidates, AllowReuse: allowReuse}
	a.samplerPolicy.applyTo(&req.mediaSamplerSelectionPolicy)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, "", fmt.Errorf("mediasampler: execute: %w", err)
	}
	var response mediaSamplerResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, "", fmt.Errorf("mediasampler: decode: %w", err)
	}
	if strings.TrimSpace(response.Error) != "" {
		return nil, "", fmt.Errorf("mediasampler: worker: %s", response.Error)
	}
	winner := ""
	if response.WinnerID != nil {
		winner = strings.TrimSpace(*response.WinnerID)
	}
	return response.Results, winner, nil
}

// Sample adapts the canonical kernel candidate model to the Rust sampler.
// This is the capability-facing port used by materialization; detailed Rust
// result rows remain an adapter concern.
func (a *MediaSamplerAdapter) Sample(ctx context.Context, sceneID, subject string, terms []string, candidates []scriptpkg.SegmentAssetCandidate, allowReuse bool) (string, error) {
	input := make([]mediaSamplerCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		input = append(input, mediaSamplerCandidate{
			ID: candidate.AssetID, Label: textutil.FirstNonEmpty(candidate.Entity, candidate.Query),
			GenericSimilarity: float32(candidate.RelevanceScore), OwnerSegmentID: candidate.SegmentID,
			EmbeddingSimilarity: optionalSimilarity(candidate.EmbeddingSimilarity),
		})
	}
	_, winner, err := a.SampleScene(ctx, sceneID, subject, terms, input, allowReuse)
	return winner, err
}
