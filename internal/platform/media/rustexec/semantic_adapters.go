package rustexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

type visualNERRequest struct {
	Version    string `json:"version"`
	Operation  string `json:"operation"`
	SegmentID  string `json:"segment_id"`
	TextHash   string `json:"text_hash"`
	SourceText string `json:"source_text"`
	Limit      int    `json:"limit"`
}
type visualNERResponse struct {
	Entities []scriptgen.VisualEntity `json:"entities"`
}

type VisualNERAdapter struct{ executor *Executor }

func NewVisualNERAdapter(executor *Executor) (*VisualNERAdapter, error) {
	if executor == nil {
		return nil, fmt.Errorf("visualner: executor is required")
	}
	return &VisualNERAdapter{executor: executor}, nil
}
func (a *VisualNERAdapter) Extract(ctx context.Context, sourceText string, limit int) ([]scriptgen.VisualEntity, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("visualner: executor is not configured")
	}
	sum := sha256.Sum256([]byte(sourceText))
	req := visualNERRequest{Version: "visualner.v1", Operation: "extract", TextHash: hex.EncodeToString(sum[:]), SourceText: sourceText, Limit: limit}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("visualner: execute: %w", err)
	}
	var response visualNERResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("visualner: decode: %w", err)
	}
	for i := range response.Entities {
		e := &response.Entities[i]
		if e.Start < 0 || e.End <= e.Start || e.End > len(sourceText) || sourceText[e.Start:e.End] != e.Text {
			return nil, fmt.Errorf("visualner: entity %q is not grounded in source_text", e.Text)
		}
	}
	return response.Entities, nil
}

type MediaSamplerAdapter struct{ executor *Executor }
type mediaSamplerRequest struct {
	Version    string                            `json:"version"`
	Operation  string                            `json:"operation"`
	Scene      mediaSamplerScene                 `json:"scene"`
	Candidates []scriptgen.MediaSamplerCandidate `json:"candidates"`
	AllowReuse bool                              `json:"allow_reuse"`
}
type mediaSamplerScene struct {
	ID      string   `json:"id"`
	Subject string   `json:"subject"`
	Terms   []string `json:"terms"`
}
type mediaSamplerResponse struct {
	Results  []scriptgen.MediaSamplerResult `json:"results"`
	WinnerID *string                        `json:"winner_id"`
}

func NewMediaSamplerAdapter(executor *Executor) (*MediaSamplerAdapter, error) {
	if executor == nil {
		return nil, fmt.Errorf("mediasampler: executor is required")
	}
	return &MediaSamplerAdapter{executor: executor}, nil
}
func (a *MediaSamplerAdapter) SampleScene(ctx context.Context, sceneID, subject string, terms []string, candidates []scriptgen.MediaSamplerCandidate, allowReuse bool) ([]scriptgen.MediaSamplerResult, string, error) {
	if a == nil || a.executor == nil {
		return nil, "", fmt.Errorf("mediasampler: executor is not configured")
	}
	req := mediaSamplerRequest{Version: "mediasampler.v1", Operation: "select", Scene: mediaSamplerScene{ID: sceneID, Subject: subject, Terms: terms}, Candidates: candidates, AllowReuse: allowReuse}
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
	winner := ""
	if response.WinnerID != nil {
		winner = strings.TrimSpace(*response.WinnerID)
	}
	return response.Results, winner, nil
}

var _ scriptgen.VisualNERPort = (*VisualNERAdapter)(nil)
var _ scriptgen.MediaSamplerPort = (*MediaSamplerAdapter)(nil)
