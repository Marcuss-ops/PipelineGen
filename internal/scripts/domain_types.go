// Package script defines the canonical domain types for the script subsystem.
//
// Deprecated: Types are now canonically defined in internal/domain/script/.
// This file re-exports them as type aliases for backward compatibility.
package scripts

import "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

// ── Type aliases (canonical types in domain/script) ─────────────────

type Script = script.Script
type Section = script.Section
type StockMatch = script.StockMatch
type ResearchSource = script.ResearchSource
type GenerationLog = script.GenerationLog
type OutlineSection = script.OutlineSection
type VersionRecord = script.VersionRecord
