package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	entityports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities/ports"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

type visualNERRequest struct {
	Version    string `json:"version"`
	Operation  string `json:"operation"`
	SegmentID  string `json:"segment_id"`
	TextHash   string `json:"text_hash"`
	SourceText string `json:"source_text"`
	Language   string `json:"language"`
	Limit      int    `json:"limit"`
	// EntityCount is the canonical VisualNER v1 field. Limit is retained for
	// compatibility with older runners; sending both lets the dedicated
	// visualner binary enforce the caller's requested bound.
	EntityCount int `json:"entity_count"`
}
type visualNERResponse struct {
	Entities []scriptgen.VisualEntity `json:"entities"`
	// Error is set by the Rust worker when it rejects a line without
	// killing the persistent process (decode/contract failure). Empty on
	// success; absent on responses from older binaries.
	Error string `json:"error,omitempty"`
}

type VisualNERAdapter struct{ executor *Executor }

func NewVisualNERAdapter(executor *Executor) (*VisualNERAdapter, error) {
	if executor == nil {
		return nil, fmt.Errorf("visualner: executor is required")
	}
	return &VisualNERAdapter{executor: executor}, nil
}
func (a *VisualNERAdapter) Close() {
	if a != nil && a.executor != nil {
		a.executor.Close()
	}
}

// VisualAnalysis is the grounded analysis returned by visualner's analyze
// operation. Entity references in relations, lower-thirds and chart points
// are indexes into Entities.
type VisualAnalysis struct {
	Entities    []scriptgen.VisualEntity `json:"entities"`
	Relations   []visualRelation         `json:"relations"`
	LowerThirds []visualLowerThird       `json:"lower_thirds"`
	Bullets     []visualBullet           `json:"bullets"`
	ChartPoints []visualChartPoint       `json:"chart_points"`
}

type visualRelation struct {
	Source        int    `json:"source"`
	Target        int    `json:"target"`
	SentenceIndex int    `json:"sentence_index"`
	Evidence      string `json:"evidence"`
}

type visualLowerThird struct {
	Entity        int    `json:"entity"`
	Title         string `json:"title"`
	Kind          string `json:"kind"`
	SentenceIndex int    `json:"sentence_index"`
	Evidence      string `json:"evidence"`
	Start         int    `json:"start"`
	End           int    `json:"end"`
}

type visualBullet struct {
	SentenceIndex int    `json:"sentence_index"`
	Text          string `json:"text"`
	Start         int    `json:"start"`
	End           int    `json:"end"`
	Entities      []int  `json:"entities"`
}

type visualChartPoint struct {
	Entity        int     `json:"entity"`
	Kind          string  `json:"kind"`
	Value         float64 `json:"value"`
	Label         string  `json:"label"`
	SentenceIndex int     `json:"sentence_index"`
}

type visualAnalyzeResponse struct {
	VisualAnalysis
	Error string `json:"error,omitempty"`
}

// Analyze runs visualner's analyze operation for one segment of text and
// validates that every entity span and cross-reference is grounded in
// sourceText.
func (a *VisualNERAdapter) Analyze(ctx context.Context, language, sourceText string, limit int) (*VisualAnalysis, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("visualner: executor is not configured")
	}
	language = strings.TrimSpace(language)
	if language == "" {
		return nil, fmt.Errorf("visualner: language is required")
	}
	req := visualNERRequest{Version: "visualner.v1", Operation: "analyze", TextHash: digest.SHA256String(sourceText), SourceText: sourceText, Language: language, Limit: limit, EntityCount: limit}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("visualner: execute: %w", err)
	}
	var response visualAnalyzeResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("visualner: decode: %w", err)
	}
	if strings.TrimSpace(response.Error) != "" {
		return nil, fmt.Errorf("visualner: worker: %s", response.Error)
	}
	analysis := response.VisualAnalysis
	for i := range analysis.Entities {
		e := &analysis.Entities[i]
		if e.Start < 0 || e.End <= e.Start || e.End > len(sourceText) || sourceText[e.Start:e.End] != e.Text {
			return nil, fmt.Errorf("visualner: entity %q is not grounded in source_text", e.Text)
		}
	}
	validIndex := func(i int) bool { return i >= 0 && i < len(analysis.Entities) }
	for _, r := range analysis.Relations {
		if !validIndex(r.Source) || !validIndex(r.Target) {
			return nil, fmt.Errorf("visualner: relation references entity out of range")
		}
	}
	for _, lt := range analysis.LowerThirds {
		if !validIndex(lt.Entity) {
			return nil, fmt.Errorf("visualner: lower third references entity out of range")
		}
	}
	for _, b := range analysis.Bullets {
		for _, idx := range b.Entities {
			if !validIndex(idx) {
				return nil, fmt.Errorf("visualner: bullet references entity out of range")
			}
		}
	}
	for _, cp := range analysis.ChartPoints {
		if !validIndex(cp.Entity) {
			return nil, fmt.Errorf("visualner: chart point references entity out of range")
		}
	}
	return &analysis, nil
}

// ChartCandidates returns the worker's chart-point labels for one scene's
// text. Each Text is an exact entity span of sourceText, so the scripts layer
// can verify it by substring match and strict parsing; nothing is inferred.
func (a *VisualNERAdapter) ChartCandidates(ctx context.Context, language, sourceText string, limit int) ([]scriptgen.ChartValueCandidate, error) {
	analysis, err := a.Analyze(ctx, language, sourceText, limit)
	if err != nil {
		return nil, err
	}
	out := make([]scriptgen.ChartValueCandidate, 0, len(analysis.ChartPoints))
	for _, cp := range analysis.ChartPoints {
		if cp.Entity < 0 || cp.Entity >= len(analysis.Entities) {
			continue
		}
		out = append(out, scriptgen.ChartValueCandidate{Text: analysis.Entities[cp.Entity].Text, Value: cp.Value})
	}
	return out, nil
}

// BulletCandidates returns the worker's sentence bullets for one scene's
// text. Each candidate is validated as an exact byte span of sourceText so
// the scripts layer can re-map it onto its own sentence splitter; nothing
// is inferred.
func (a *VisualNERAdapter) BulletCandidates(ctx context.Context, language, sourceText string, limit int) ([]scriptgen.BulletCandidate, error) {
	analysis, err := a.Analyze(ctx, language, sourceText, limit)
	if err != nil {
		return nil, err
	}
	out := make([]scriptgen.BulletCandidate, 0, len(analysis.Bullets))
	for _, b := range analysis.Bullets {
		if b.Start < 0 || b.End <= b.Start || b.End > len(sourceText) {
			continue
		}
		if sourceText[b.Start:b.End] != b.Text {
			continue
		}
		if strings.TrimSpace(b.Text) == "" || b.SentenceIndex < 0 {
			continue
		}
		out = append(out, scriptgen.BulletCandidate{Text: b.Text, SentenceIndex: b.SentenceIndex, Start: b.Start, End: b.End})
	}
	return out, nil
}

// Extract uses the compact entity-only operation. The richer analyze response
// includes pairwise relations and sentence evidence; that response is useful
// to callers that need it, but can exceed the persistent worker's bounded
// output for long narration. Scene entity extraction only needs grounded spans.
func (a *VisualNERAdapter) Extract(ctx context.Context, language, sourceText string, limit int) ([]scriptgen.VisualEntity, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("visualner: executor is not configured")
	}
	language = strings.TrimSpace(language)
	if language == "" {
		return nil, fmt.Errorf("visualner: language is required")
	}
	req := visualNERRequest{Version: "visualner.v1", Operation: "extract", TextHash: digest.SHA256String(sourceText), SourceText: sourceText, Language: language, Limit: limit, EntityCount: limit}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("visualner: encode: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("visualner: execute: %w", err)
	}
	var response visualNERResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("visualner: decode: %w", err)
	}
	if strings.TrimSpace(response.Error) != "" {
		return nil, fmt.Errorf("visualner: worker: %s", response.Error)
	}
	for _, entity := range response.Entities {
		if entity.Start < 0 || entity.End <= entity.Start || entity.End > len(sourceText) || sourceText[entity.Start:entity.End] != entity.Text {
			return nil, fmt.Errorf("visualner: entity %q is not grounded in source_text", entity.Text)
		}
	}
	return response.Entities, nil
}

// ExtractEntities adapts the same Rust VisualNER implementation to the
// canonical entity port used by the legacy batch boundary. The source spans
// are validated by Extract before this projection is returned.
func (a *VisualNERAdapter) ExtractEntities(ctx context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	if strings.TrimSpace(req.Language) == "" {
		return nil, fmt.Errorf("visualner: language is required")
	}
	limit := req.EntityCount
	if limit <= 0 {
		limit = 3
	}
	entities, err := a.Extract(ctx, req.Language, req.Text, limit)
	if err != nil {
		return nil, err
	}
	result := &scriptpkg.EntityResult{NounChunks: make([]string, 0, len(entities)), Concepts: make([]scriptpkg.Entity, 0, len(entities))}
	for _, entity := range entities {
		result.NounChunks = append(result.NounChunks, entity.Text)
		result.Concepts = append(result.Concepts, scriptpkg.Entity{Value: entity.Text, Type: string(entity.Type), Score: entity.Score})
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
	// Error is set by the Rust worker when it rejects a line without
	// killing the persistent process (decode/contract failure). Empty on
	// success; absent on responses from older binaries.
	Error string `json:"error,omitempty"`
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
		})
	}
	_, winner, err := a.SampleScene(ctx, sceneID, subject, terms, input, allowReuse)
	return winner, err
}

var _ scriptgen.VisualNERPort = (*VisualNERAdapter)(nil)
var _ entityports.EntityExtractor = (*VisualNERAdapter)(nil)
var _ scriptports.MediaSamplerPort = (*MediaSamplerAdapter)(nil)
