// Package usecase — types_aliases.go holds type aliases for symbols moved
// to sub-packages (adapters/, dto/) during the PR-G Phase 1b melt.
// This file is the single source of truth for bare-name references across
// the usecase package.
// Package usecase — types_aliases.go holds type aliases for symbols moved
// to sub-packages (adapters/, dto/) during the PR-G Phase 1b melt.
// This file is the single source of truth for bare-name references across
// the usecase package.
package usecase

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	dto "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/dto"
)

// ── adapters/ aliases ──────────────────────────────────────────────
type NormalizationConfig = adapters.NormalizationConfig
type SourceRegistry = adapters.SourceRegistry

// PostProcessorRegistry (audit #6 — single canonical): aliases the
// typed registry in internal/application/scripts. The adapters
// stub (a placeholder that existed only to break the inheritance
// build cycle in PR 0) was removed in Phase 4; any future
// reader is pointed here. The canonical Run() / ValidateRequested()
// / Freeze() / Registered() / LookupPolicy() / Policy(none) flow
// lives in scripts.PostProcessorRegistry.
type PostProcessorRegistry = scripts.PostProcessorRegistry
type PipelineResult = adapters.PipelineResult
type ProcessInput = adapters.ProcessInput
type FolderResolver = adapters.FolderResolver
type ScriptRepository = adapters.ScriptRepository
type SourceResolver = adapters.SourceResolver
type PostProcessResult = adapters.PostProcessResult

// PostProcessor interface lives in the canonical scripts package
// (audit #6 — single canonical). The prior adapters.PostProcessor
// shadow was never declared (the interface always lived in scripts),
// so this alias redirects at the canonical home.
type PostProcessor = scripts.PostProcessor
type SceneImage = adapters.SceneImage
type SceneVoiceover = adapters.SceneVoiceover

const (
	ProcessorRequired   = adapters.ProcessorRequired
	ProcessorBestEffort = adapters.ProcessorBestEffort
	// RequiredAtLeastOne re-exported for callers (PR ref
	// fix/required-postprocessor-semantics, June 2026). The legacy
	// `ProcessorRequired` alias is kept so pre-PR 3 callers that
	// referenced the two-value enum under the old "all failed → error"
	// semantic still compile.
	RequiredAtLeastOne = adapters.RequiredAtLeastOne
	// RequiredAll re-exported for callers that want to assert the
	// strict-union "any failure is hard" policy directly.
	RequiredAll = adapters.RequiredAll
)

// ScriptRecord / ScriptSectionRecord — the native types are
// declared in section_regen.go (usecase package). Re-exports
// of the canonical adapters types below cover the remaining
// types used by the engine_test.go fake repo.
type (
	ScriptStockMatchRecord     = adapters.ScriptStockMatchRecord
	ScriptGenerationLog        = adapters.ScriptGenerationLog
	ScriptOutlineSectionRecord = adapters.ScriptOutlineSectionRecord
	ScriptResearchSource       = adapters.ScriptResearchSource
)

// ── adapters/ function aliases ─────────────────────────────────────
var (
	NormalizeItem                  = adapters.NormalizeItem
	SerializeEntityResultRoundTrip = dto.SerializeEntityResultRoundTrip
)

// ── dto/ aliases ───────────────────────────────────────────────────

// (LegacyArrayToOutput and DecodeModelOutput removed in P0.8 —
// replaced by jsonextract.Scanner.)
