package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	entityports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities/ports"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
	// TemporalFacts carries every source-grounded date occurrence,
	// independent of the visual top-N budget (absent from older binaries).
	TemporalFacts []visualTemporalFact `json:"temporal_facts"`
	// NumericFacts carries every NUMBER/PERCENT/MONEY occurrence
	// with its parse (absent from older binaries).
	NumericFacts []visualNumericFact `json:"numeric_facts"`
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

// visualTemporalFact mirrors the Rust TemporalFact wire contract:
// surface/type/normalized_date/precision/start_byte/end_byte/evidence.
type visualTemporalFact struct {
	Surface        string  `json:"surface"`
	Kind           string  `json:"type"`
	NormalizedDate *string `json:"normalized_date"`
	Precision      string  `json:"precision"`
	StartByte      int     `json:"start_byte"`
	EndByte        int     `json:"end_byte"`
	Evidence       string  `json:"evidence"`
	Era            *string `json:"era,omitempty"`
	Ambiguous      bool    `json:"ambiguous,omitempty"`
}

var visualTemporalPrecisions = map[string]bool{
	"day": true, "month": true, "year": true, "unresolved": true,
}

// visualNumericFact mirrors the Rust NumericFact wire contract.
type visualNumericFact struct {
	Surface   string   `json:"surface"`
	Kind      string   `json:"type"`
	Value     *float64 `json:"value"`
	StartByte int      `json:"start_byte"`
	EndByte   int      `json:"end_byte"`
	Evidence  string   `json:"evidence"`
}

var visualNumericKinds = map[string]bool{
	"NUMBER": true, "PERCENT": true, "MONEY": true,
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
	if err := validateTemporalFacts(sourceText, analysis.TemporalFacts); err != nil {
		return nil, err
	}
	if err := validateNumericFacts(sourceText, analysis.NumericFacts); err != nil {
		return nil, err
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

// validateTemporalFacts enforces the temporal contract at the boundary:
// every fact is a DATE, its surface reproduces the exact byte span of the
// source, the precision is canonical, and the normalized ISO form matches
// the precision's shape (YYYY-MM-DD for day, YYYY-MM for month, YYYY for
// year, empty for unresolved facts).
func validateTemporalFacts(sourceText string, facts []visualTemporalFact) error {
	for i := range facts {
		fact := &facts[i]
		if fact.Kind != "DATE" {
			return fmt.Errorf("visualner: temporal fact %q has kind %q", fact.Surface, fact.Kind)
		}
		if !visualTemporalPrecisions[fact.Precision] {
			return fmt.Errorf("visualner: temporal fact %q has unknown precision %q", fact.Surface, fact.Precision)
		}
		if fact.StartByte < 0 || fact.EndByte <= fact.StartByte || fact.EndByte > len(sourceText) || sourceText[fact.StartByte:fact.EndByte] != fact.Surface || fact.Evidence != fact.Surface {
			return fmt.Errorf("visualner: temporal fact %q is not grounded in source_text", fact.Surface)
		}
		switch fact.Precision {
		case "unresolved":
			if fact.NormalizedDate != nil {
				return fmt.Errorf("visualner: unresolved temporal fact %q must not carry a normalized date", fact.Surface)
			}
		case "day":
			if fact.NormalizedDate == nil || !isoRegexDay.MatchString(*fact.NormalizedDate) || !isoCalendarValid(*fact.NormalizedDate) {
				return fmt.Errorf("visualner: day temporal fact %q lacks a valid ISO date %v", fact.Surface, fact.NormalizedDate)
			}
		case "month":
			if fact.NormalizedDate == nil || !isoRegexMonth.MatchString(*fact.NormalizedDate) || !isoCalendarValid(*fact.NormalizedDate) {
				return fmt.Errorf("visualner: month temporal fact %q lacks a valid ISO month %v", fact.Surface, fact.NormalizedDate)
			}
		case "year":
			if fact.NormalizedDate == nil || !isoRegexYear.MatchString(*fact.NormalizedDate) {
				return fmt.Errorf("visualner: year temporal fact %q lacks a valid ISO year %v", fact.Surface, fact.NormalizedDate)
			}
		}
	}
	return nil
}

// validateNumericFacts enforces the numeric contract at the boundary: the
// kind is canonical and the surface reproduces the exact byte span of the
// source. A parsed value is attached only when the worker produced one.
func validateNumericFacts(sourceText string, facts []visualNumericFact) error {
	for i := range facts {
		fact := &facts[i]
		if !visualNumericKinds[fact.Kind] {
			return fmt.Errorf("visualner: numeric fact %q has unknown kind %q", fact.Surface, fact.Kind)
		}
		if fact.StartByte < 0 || fact.EndByte <= fact.StartByte || fact.EndByte > len(sourceText) || sourceText[fact.StartByte:fact.EndByte] != fact.Surface || fact.Evidence != fact.Surface {
			return fmt.Errorf("visualner: numeric fact %q is not grounded in source_text", fact.Surface)
		}
		if fact.Value != nil && !isFinite(*fact.Value) {
			return fmt.Errorf("visualner: numeric fact %q has a non-finite value", fact.Surface)
		}
	}
	return nil
}

func isFinite(value float64) bool {
	return !math.IsInf(value, 0) && !math.IsNaN(value)
}

var isoRegexDay = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
var isoRegexMonth = regexp.MustCompile(`^\d{4}-\d{2}$`)
var isoRegexYear = regexp.MustCompile(`^\d{4}$`)

// isoCalendarValid checks the calendar position of an ISO prefix (with or
// without month/day segments); boundaries are intra-month and the rest is
// irrelevant to the projection.
func isoCalendarValid(iso string) bool {
	parts := strings.Split(iso, "-")
	if len(parts) == 0 {
		return false
	}
	year, err := strconv.Atoi(parts[0])
	if err != nil || year <= 0 {
		return false
	}
	if len(parts) < 2 {
		return true
	}
	month, err := strconv.Atoi(parts[1])
	if err != nil || month < 1 || month > 12 {
		return false
	}
	if len(parts) < 3 {
		return true
	}
	day, err := strconv.Atoi(parts[2])
	max := daysInGoMonth(month, year)
	return err == nil && day >= 1 && day <= max
}

func daysInGoMonth(month, year int) int {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	default:
		return 0
	}
}

// projectBullets reuses the bullet projection of BulletCandidates for the
// single-analyze bundle; nothing is inferred.
func projectBullets(sourceText string, analysis *VisualAnalysis) []scriptgen.BulletCandidate {
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
	return out
}

// projectCharts reuses the chart projection of ChartCandidates for the
// single-analyze bundle; nothing is inferred.
func projectCharts(sourceText string, analysis *VisualAnalysis) []scriptgen.ChartValueCandidate {
	out := make([]scriptgen.ChartValueCandidate, 0, len(analysis.ChartPoints))
	for _, cp := range analysis.ChartPoints {
		if cp.Entity < 0 || cp.Entity >= len(analysis.Entities) {
			continue
		}
		out = append(out, scriptgen.ChartValueCandidate{Text: analysis.Entities[cp.Entity].Text, Value: cp.Value})
	}
	return out
}

// SemanticCandidates is the single-analyze projection the scripts layer
// consumes for one scene: one worker call yields the grounded Bullet,
// ChartValue and TemporalFact candidates. Nothing is inferred and each
// surface is byte-verified against sourceText by Analyze.
func (a *VisualNERAdapter) SemanticCandidates(ctx context.Context, language, sourceText string, limit int) (*scriptgen.SemanticCandidateBundle, error) {
	analysis, err := a.Analyze(ctx, language, sourceText, limit)
	if err != nil {
		return nil, err
	}
	temporal := make([]scriptgen.TemporalFactCandidate, 0, len(analysis.TemporalFacts))
	for _, fact := range analysis.TemporalFacts {
		candidate := scriptgen.TemporalFactCandidate{
			Text: fact.Surface, Precision: fact.Precision,
			Start: fact.StartByte, End: fact.EndByte, Ambiguous: fact.Ambiguous,
		}
		if fact.NormalizedDate != nil {
			candidate.NormalizedDate = *fact.NormalizedDate
		}
		if fact.Era != nil {
			candidate.Era = *fact.Era
		}
		temporal = append(temporal, candidate)
	}
	numeric := make([]scriptgen.NumericFactCandidate, 0, len(analysis.NumericFacts))
	for _, fact := range analysis.NumericFacts {
		candidate := scriptgen.NumericFactCandidate{
			Text: fact.Surface, Kind: fact.Kind,
			Start: fact.StartByte, End: fact.EndByte,
		}
		if fact.Value != nil {
			candidate.Value = *fact.Value
			candidate.HasValue = true
		}
		numeric = append(numeric, candidate)
	}
	return &scriptgen.SemanticCandidateBundle{
		Bullets:  projectBullets(sourceText, analysis),
		Charts:   projectCharts(sourceText, analysis),
		Temporal: temporal,
		Numeric:  numeric,
	}, nil
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

type MediaSamplerAdapter struct {
	executor *Executor
	// samplerPolicy is the Go-owned data-driven selection policy (keyword
	// tables + optional diversity weight) bound at the composition root.
	// The zero value keeps the Rust worker's curated V1 defaults.
	samplerPolicy SamplerPolicy
}

// These wire types belong to the Rust adapter only. They are deliberately
// not part of the capability contract; callers use kernel SegmentAssetCandidate
// and receive only the selected asset ID through MediaSamplerPort.
type mediaSamplerCandidate struct {
	ID                string  `json:"id"`
	Label             string  `json:"label"`
	GenericSimilarity float32 `json:"generic_similarity"`
	OwnerSegmentID    string  `json:"owner_segment_id,omitempty"`
	// EmbeddingSimilarity is the optional real vector similarity for the
	// candidate. It is a SOFT signal only (a source-derived keyword anchor
	// still dominates, and a hard-reject is never resurrected). Unset
	// keeps the legacy behavior; out-of-range values fail closed in the
	// worker.
	EmbeddingSimilarity *float32 `json:"embedding_similarity,omitempty"`
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
	// The Go-owned data-driven selection policy (empty tables stay absent
	// on the wire so the worker keeps its curated defaults).
	mediaSamplerSelectionPolicy
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

var _ scriptgen.VisualNERPort = (*VisualNERAdapter)(nil)
var _ entityports.EntityExtractor = (*VisualNERAdapter)(nil)
var _ scriptports.MediaSamplerPort = (*MediaSamplerAdapter)(nil)
