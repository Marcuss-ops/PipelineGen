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
//
// P0.7 / P0.8 (June 2026): DecodeModelOutput and LegacyArrayToOutput
// were REMOVED from the adapters package when the JSON extraction
// pipeline migrated to `internal/application/scripts/jsonextract/`
// (single Scanner with Mode controls; canonical
// jsonextract.NewScanner(mode).Scan(raw, source) returning
// `*scriptpkg.ModelScriptOutputV1`, plus unexported
// `convertLegacyArray` for backward-compat fallback). The old
// top-level decoders had 0 callers after migration, so the usecase
// aliases are deleted instead of re-pointed to the stateful new API
// (which has a different signature and no clean 1:1 alias).
var (
	NormalizeItem                  = adapters.NormalizeItem
	SerializeEntityResultRoundTrip = dto.SerializeEntityResultRoundTrip
)

// ── Note ───────────────────────────────────────────────────────────
//
// `dto.SerializeEntityResultRoundTrip` is the only `dto/` symbol that
// appears as a top-level alias at this layer; it lives inside the
// `var` block above alongside the `adapters/` aliases. Any future
// `dto/` re-export that needs this surface should follow the same
// convention (one consolidated `var` block, documentation in this
// header). The previous `dto/ aliases` section header was retired
// when the legacy `adapters.DecodeModelOutput` /
// `adapters.LegacyArrayToOutput` aliases moved out.
