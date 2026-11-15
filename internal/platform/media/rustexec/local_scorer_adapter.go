package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
)

type localScoreRequest struct {
	Version    string                       `json:"version"`
	Operation  string                       `json:"operation"`
	Query      search.LocalScoreQuery       `json:"query"`
	Candidates []search.LocalScoreCandidate `json:"candidates"`
}

type localScoreResponse struct {
	Scores []search.LocalScoreResult `json:"scores"`
	Error  string                    `json:"error,omitempty"`
}

// LocalScorerAdapter sends one batch to the persistent mediasampler worker.
type LocalScorerAdapter struct {
	executor *Executor
}

func NewLocalScorerAdapter(executor *Executor) (*LocalScorerAdapter, error) {
	if executor == nil {
		return nil, fmt.Errorf("mediasampler local scorer: executor is required")
	}
	return &LocalScorerAdapter{executor: executor}, nil
}

func (a *LocalScorerAdapter) ScoreLocal(ctx context.Context, query search.LocalScoreQuery, candidates []search.LocalScoreCandidate) ([]search.LocalScoreResult, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("mediasampler local scorer: executor is not configured")
	}
	if len(candidates) == 0 {
		return []search.LocalScoreResult{}, nil
	}
	request := localScoreRequest{
		Version: "mediasampler.v1", Operation: "score_local",
		Query: search.LocalScoreQuery{
			Text: query.Text, Tags: append([]string{}, query.Tags...),
			Source: query.Source, MinDurationMS: query.MinDurationMS,
		},
		Candidates: make([]search.LocalScoreCandidate, len(candidates)),
	}
	for i, candidate := range candidates {
		candidate.Tags = append([]string{}, candidate.Tags...)
		request.Candidates[i] = candidate
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("mediasampler local scorer encode: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("mediasampler local scorer execute: %w", err)
	}
	var response localScoreResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("mediasampler local scorer decode: %w", err)
	}
	if strings.TrimSpace(response.Error) != "" {
		return nil, fmt.Errorf("mediasampler local scorer worker: %s", response.Error)
	}
	return response.Scores, nil
}
