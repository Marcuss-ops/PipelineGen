package rustexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	entityports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities/ports"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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

// ExtractEntities adapts the same Rust VisualNER implementation to the
// canonical entity port used by the legacy batch boundary. The source spans
// are validated by Extract before this projection is returned.
func (a *VisualNERAdapter) ExtractEntities(ctx context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	limit := req.EntityCount
	if limit <= 0 {
		limit = 3
	}
	entities, err := a.Extract(ctx, req.Text, limit)
	if err != nil {
		return nil, err
	}
	result := &scriptpkg.EntityResult{NounChunks: make([]string, 0, len(entities)), Concepts: make([]scriptpkg.Entity, 0, len(entities))}
	for _, entity := range entities {
		result.NounChunks = append(result.NounChunks, entity.Text)
		result.Concepts = append(result.Concepts, scriptpkg.Entity{Value: entity.Text, Type: "CONCEPT", Score: entity.Score})
	}
	return result, nil
}

type MediaSamplerAdapter struct{ executor *Executor }

// These wire types belong to the Rust adapter only. They are deliberately
// not part of the capability contract; callers use kernel SegmentAssetCandidate
// and receive only the selected asset ID through MediaSamplerPort.
type mediaSamplerCandidate struct {
	ID                string  `json:"id"`
	Label             string  `json:"label"`
	GenericSimilarity float32 `json:"generic_similarity"`
	OwnerSegmentID    string  `json:"owner_segment_id,omitempty"`
}
type mediaSamplerResult struct {
	CandidateID string  `json:"candidate_id"`
	Label       string  `json:"label"`
	Score       float32 `json:"score"`
	Rejection   string  `json:"rejection,omitempty"`
	Reason      string  `json:"reason"`
}
type mediaSamplerRequest struct {
	Version    string                  `json:"version"`
	Operation  string                  `json:"operation"`
	Scene      mediaSamplerScene       `json:"scene"`
	Candidates []mediaSamplerCandidate `json:"candidates"`
	AllowReuse bool                    `json:"allow_reuse"`
}
type mediaSamplerScene struct {
	ID      string   `json:"id"`
	Subject string   `json:"subject"`
	Terms   []string `json:"terms"`
}
type mediaSamplerResponse struct {
	Results  []mediaSamplerResult `json:"results"`
	WinnerID *string              `json:"winner_id"`
}

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

// Sample adapts the canonical kernel candidate model to the Rust sampler.
// This is the capability-facing port used by materialization; detailed Rust
// result rows remain an adapter concern.
func (a *MediaSamplerAdapter) Sample(ctx context.Context, sceneID, subject string, terms []string, candidates []scriptpkg.SegmentAssetCandidate, allowReuse bool) (string, error) {
	input := make([]mediaSamplerCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		input = append(input, mediaSamplerCandidate{
			ID: candidate.AssetID, Label: firstNonEmpty(candidate.Entity, candidate.Query),
			GenericSimilarity: float32(candidate.RelevanceScore), OwnerSegmentID: candidate.SegmentID,
		})
	}
	_, winner, err := a.SampleScene(ctx, sceneID, subject, terms, input, allowReuse)
	return winner, err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var _ scriptgen.VisualNERPort = (*VisualNERAdapter)(nil)
var _ entityports.EntityExtractor = (*VisualNERAdapter)(nil)
var _ scriptports.MediaSamplerPort = (*MediaSamplerAdapter)(nil)
