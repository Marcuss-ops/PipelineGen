// Package gemmamemory is the "gemma memory gate" cache used by the script
// engine to short-circuit repeated generation requests. Its types are
// referenced from scriptcore.Engine, the script flow batch/curation paths,
// and the app composition root.
//
// STATUS: STUB PACKAGE (Onda 5 mid-state recovery, Stage 1).
// The Onda 5 refactor moved internal/scripts/* → internal/application/scripts/*,
// but the gemmamemory subpackage was not migrated alongside its consumers.
// This file restores the type surface needed to make `go build` green again.
// Service.CheckGate / SaveAfterGeneration / SweepAll / BuildFreshVariantPrompt
// are no-op stubs that preserve API shape but DO NOT implement the cache
// semantics. The follow-up Stage 7 of feature/onda-5-completion will
// re-import the real implementation (or port it in-place if it was retired).
//
// Until that follow-up lands:
//   - engine.CheckMemoryGate will return nil (cache miss → fresh generation)
//   - engine.SaveMemory is effectively a no-op (no cache rows written)
//   - lifecycle.startGemmaMemorySweeper reports zero deletions
//
// Runtime behavior under this stub is degraded but SAFE — the script
// pipeline will continue to produce output, just without the memory gate
// speedup/cache-hit path.
package gemmamemory

import (
	"context"
	"database/sql"

	"go.uber.org/zap"
)

// Mode constants used by callers to route generation requests through the
// right prompt template / cache key namespace. Only ModeClipToScript was
// previously referenced by the consumer code, but the others are declared
// to keep the constant surface consistent with the engine contract.
const (
	ModeText         = "text"
	ModeClipToScript = "clip_to_script"
	ModeBook         = "book"
)

// MemoryGateRequest is the input to Service.CheckGate. Mirrors the engine's
// pre-generation cache lookup key.
type MemoryGateRequest struct {
	ChannelID    string
	Title        string
	Prompt       string
	Language     string
	Mode         string
	UseMemory    bool
	ForceRefresh bool
}

// GateResult is the output of Service.CheckGate. nil means "no cache entry
// — proceed with full generation".
type GateResult struct {
	EnrichedPrompt     string
	CacheHit           bool
	SourceGenerationID string
	ExactOutput        *GenerationOutput
}

// GenerationOutput is the cached payload returned on a cache hit. Consumer
// code (engine.WriteScript, batch.generateSingleChapterFromWorkItem,
// scriptcurator) reads OutputText and uses it as the final script.
type GenerationOutput struct {
	OutputText string
	// Future fields (cache metadata, embeddings, etc.) can be added
	// here without breaking callers — the consumer code only reads
	// OutputText today.
}

// SaveGenerationInput is the input to Service.SaveAfterGeneration. Called
// after a successful fresh generation to seed the cache for next time.
type SaveGenerationInput struct {
	ChannelID  string
	Mode       string
	Language   string
	Title      string
	Prompt     string
	Model      string
	OutputText string
	WordCount  int
}

// Service is the in-memory + SQLite cache facade. Constructed by
// NewService, owned by the script engine, and consumed indirectly by
// Engine.CheckMemoryGate.
type Service struct {
	repo *Repository
	log  *zap.Logger
}

// Repository is the SQLite-backed storage for memory gate rows. Owned by
// the app composition root (registry.go, dependencies.go, bootstrap.go)
// and consumed by the background sweepers (lifecycle.startGemmaMemorySweeper).
type Repository struct {
	DB *sql.DB
}

// NewRepository wires a Repository over an open SQLite handle. The DB
// handle is the canonical media.db.sqlite from internal/infrastructure/database/sqlite.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{DB: db}
}

// NewService wires a Service over a Repository with structured logging.
// In the real implementation this is where the LRU bucket + Lua-style
// prompt-dedup index is constructed; the stub holds the references only.
func NewService(repo *Repository, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{repo: repo, log: log}
}

// CheckGate returns nil — meaning "no cache hit, proceed with fresh
// generation". As a result the consumer code in scriptcore.Engine always
// follows the cold path, which is correct but slower than the cached path.
//
// STUB: implement prompt-hash lookup + reference-hit enrichment here.
func (s *Service) CheckGate(ctx context.Context, req MemoryGateRequest) (*GateResult, error) {
	_ = ctx
	_ = req
	return nil, nil
}

// SaveAfterGeneration is a no-op. The cache table stays empty until the
// real implementation lands.
//
// STUB: insert a row keyed by (channel_id, mode, language, prompt_hash).
func (s *Service) SaveAfterGeneration(ctx context.Context, in SaveGenerationInput, outputText string) (int64, error) {
	_ = ctx
	_ = in
	_ = outputText
	return 0, nil
}

// BuildFreshVariantPrompt returns the base prompt unchanged. The real
// implementation injects an "avoid repeating the prior output" instruction
// when a near-duplicate is detected.
//
// STUB: when CheckGate returns CacheHit=true, enrich the prompt here.
func BuildFreshVariantPrompt(basePrompt string, output *GenerationOutput) string {
	_ = output
	return basePrompt
}

// EvictExactOutputs removes cache entries whose titles match the given
// list. Returns the number of deleted rows.
//
// STUB: the cache table is empty until the real implementation lands.
func (s *Service) EvictExactOutputs(ctx context.Context, titles []string) (int64, error) {
	_ = ctx
	_ = titles
	return 0, nil
}

// SweepAll is a no-op. The background gemma-memory sweeper (every 6h)
// will report zero deletions.
//
// STUB: combine decay, TTL, per-channel capping, and chunk cleanup into
// one transactional sweep here.
func (r *Repository) SweepAll(ctx context.Context) (int64, error) {
	_ = ctx
	return 0, nil
}
