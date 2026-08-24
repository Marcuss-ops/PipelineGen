// Package gemmamemory provides the gemma-based memory caching layer
// for script generation.
package adapters

import (
	"context"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"go.uber.org/zap"
)

// ── Mode constants ─────────────────────────────────────────────────────

const (
	ModeText         = "text"
	ModeClipToScript = "clip_to_script"
	ModeBook         = "book"
)

// ── Service ────────────────────────────────────────────────────────────

// Service is the gemma memory caching service.
type Service struct {
	repo scriptports.MemoryGate
	log  *zap.Logger
}

// NewService creates a new Service. If log is nil, a no-op logger is used.
func NewService(repo scriptports.MemoryGate, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{repo: repo, log: log}
}

// CheckGate queries the gemma_script_outputs table for an exact cache match.
func (s *Service) CheckGate(ctx context.Context, req MemoryGateRequest) (*GateResult, error) {
	if !req.UseMemory || req.ForceRefresh {
		return nil, nil
	}
	if s.repo == nil {
		return nil, nil
	}
	hashKey := req.CacheKey
	if hashKey == "" {
		hashKey = req.Title
	}
	if hashKey == "" {
		return nil, nil
	}

	out, err := s.repo.FindExactOutput(ctx, req.ChannelID, req.Mode, hashKey)
	if err != nil {
		s.log.Error("gemmamemory: CheckGate query failed", zap.Error(err))
		return nil, err
	}
	if out == nil {
		return nil, nil
	}

	return &GateResult{
		Hit:       true,
		Output:    out.OutputText,
		WordCount: out.WordCount,
		Model:     out.Model,
	}, nil
}

// SaveAfterGeneration inserts or updates the gemma_script_outputs table.
// The output parameter is the generated text written to output_text.
func (s *Service) SaveAfterGeneration(ctx context.Context, in SaveGenerationInput, output string) (int, error) {
	if s.repo == nil {
		return 0, nil
	}
	if output == "" {
		return 0, nil
	}
	affected, err := s.repo.SaveGeneration(ctx, in, output)
	if err != nil {
		s.log.Error("gemmamemory: SaveAfterGeneration failed", zap.Error(err))
		return 0, err
	}
	return int(affected), nil
}

// EvictExactOutputs removes cached outputs whose title is in titles.
func (s *Service) EvictExactOutputs(ctx context.Context, titles []string) (int, error) {
	if s.repo == nil || len(titles) == 0 {
		return 0, nil
	}
	affected, err := s.repo.DeleteExactOutputsByTitles(ctx, titles)
	if err != nil {
		s.log.Error("gemmamemory: EvictExactOutputs failed", zap.Error(err))
		return 0, err
	}
	return int(affected), nil
}

// ── Request/Response types ─────────────────────────────────────────────

// MemoryGateRequest carries the inputs for a gate check.
//
// PR 2: CacheKey is the canonical cache address computed by
// scriptpkg.BuildCacheKey(plan). Title/Language/Mode remain for
// backwards compat with the legacy Title-keyed lookup; production
// wiring is expected to prefer CacheKey once the real Service
// implementation lands.
type MemoryGateRequest struct {
	ChannelID    string `json:"channel_id"`
	Title        string `json:"title"`
	Prompt       string `json:"prompt"`
	Language     string `json:"language"`
	Mode         string `json:"mode"`
	CacheKey     string `json:"cache_key,omitempty"`
	UseMemory    bool   `json:"use_memory"`
	ForceRefresh bool   `json:"force_refresh"`
}

// GateResult is the result of a memory gate check.
type GateResult struct {
	Hit       bool   `json:"hit"`
	Output    string `json:"output"`
	WordCount int    `json:"word_count"`
	Model     string `json:"model"`
}

// SaveGenerationInput carries the inputs to save a generation result.
type SaveGenerationInput = scriptports.SaveGenerationInput

// GenerationOutput carries a previous generation output.
type GenerationOutput = scriptports.GenerationOutput

// ── Free functions ─────────────────────────────────────────────────────

// BuildFreshVariantPrompt returns the base prompt with variant instructions appended.
func BuildFreshVariantPrompt(basePrompt string, out *GenerationOutput) string {
	if out == nil {
		return basePrompt
	}
	return basePrompt + "\n\n[FRESH_VARIANT_INSTRUCTIONS]\nPREVIOUS_RUN_AVOID_LIST:\n" + out.OutputText
}
