// Package usecase — types_aliases.go holds type aliases for symbols moved
// to sub-packages (adapters/, dto/) during the PR-G Phase 1b melt.
// This file is the single source of truth for bare-name references across
// the usecase package.
package usecase

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	dto "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/dto"
)

// ── adapters/ aliases ──────────────────────────────────────────────
type NormalizationConfig = adapters.NormalizationConfig
type SourceRegistry = adapters.SourceRegistry
type PostProcessorRegistry = adapters.PostProcessorRegistry
type PipelineResult = adapters.PipelineResult
type ProcessInput = adapters.ProcessInput
type FolderResolver = adapters.FolderResolver
type ScriptRepository = adapters.ScriptRepository
type SourceResolver = adapters.SourceResolver

// ── adapters/ function aliases ─────────────────────────────────────
var (
	NormalizeItem                  = adapters.NormalizeItem
	DecodeModelOutput              = adapters.DecodeModelOutput
	SerializeEntityResultRoundTrip = dto.SerializeEntityResultRoundTrip
)

// ── dto/ aliases ───────────────────────────────────────────────────

// compat aliases for legacy support
var LegacyArrayToOutput = adapters.LegacyArrayToOutput
