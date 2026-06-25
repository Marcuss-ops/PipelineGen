// Package gemmamemory provides the gemma-based memory caching layer
// for script generation. Recreated as a minimal stub after production
// code was removed from remote.
package gemmamemory

import (
	"context"
	"database/sql"

	"go.uber.org/zap"
)

// ── Mode constants ─────────────────────────────────────────────────────

const (
	ModeText         = "text"
	ModeClipToScript = "clip_to_script"
	ModeBook         = "book"
)

// ── Repository ─────────────────────────────────────────────────────────

// Repository holds the database handle for gemma memory operations.
type Repository struct {
	DB *sql.DB
}

// NewRepository creates a new Repository with the given DB handle.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{DB: db}
}

// SweepAll is a stub that always returns 0. Return type upgraded from
// int → int64 so callers compile without ad-hoc casts. Stub semantics
// unchanged.
func (r *Repository) SweepAll(ctx context.Context) (int64, error) {
	return 0, nil
}

// ── Service ────────────────────────────────────────────────────────────

// Service is the gemma memory caching service.
type Service struct {
	repo *Repository
	log  *zap.Logger
}

// NewService creates a new Service. If log is nil, a no-op logger is used.
func NewService(repo *Repository, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{repo: repo, log: log}
}

// CheckGate is a stub that always returns nil (cache miss).
func (s *Service) CheckGate(ctx context.Context, req MemoryGateRequest) (*GateResult, error) {
	return nil, nil
}

// SaveAfterGeneration is a stub that always returns 0.
func (s *Service) SaveAfterGeneration(ctx context.Context, in SaveGenerationInput, output string) (int, error) {
	return 0, nil
}

// EvictExactOutputs is a stub that always returns 0.
func (s *Service) EvictExactOutputs(ctx context.Context, titles []string) (int, error) {
	return 0, nil
}

// ── Request/Response types ─────────────────────────────────────────────

// MemoryGateRequest carries the inputs for a gate check.
type MemoryGateRequest struct {
	ChannelID    string `json:"channel_id"`
	Title        string `json:"title"`
	Prompt       string `json:"prompt"`
	Language     string `json:"language"`
	Mode         string `json:"mode"`
	UseMemory    bool   `json:"use_memory"`
	ForceRefresh bool   `json:"force_refresh"`
}

// GateResult is the result of a memory gate check.
type GateResult struct {
	Hit      bool   `json:"hit"`
	Output   string `json:"output"`
	WordCount int    `json:"word_count"`
	Model     string `json:"model"`
}

// SaveGenerationInput carries the inputs to save a generation result.
type SaveGenerationInput struct {
	ChannelID  string `json:"channel_id"`
	Mode       string `json:"mode"`
	Language   string `json:"language"`
	Title      string `json:"title"`
	Prompt     string `json:"prompt"`
	Model      string `json:"model"`
	OutputText string `json:"output_text"`
	WordCount  int    `json:"word_count"`
}

// GenerationOutput carries a previous generation output.
type GenerationOutput struct {
	OutputText string `json:"output_text"`
}

// ── Free functions ─────────────────────────────────────────────────────

// BuildFreshVariantPrompt returns the base prompt with variant instructions appended.
func BuildFreshVariantPrompt(basePrompt string, out *GenerationOutput) string {
	if out == nil {
		return basePrompt
	}
	return basePrompt + "\n\n[FRESH_VARIANT_INSTRUCTIONS]\nPREVIOUS_RUN_AVOID_LIST:\n" + out.OutputText
}
