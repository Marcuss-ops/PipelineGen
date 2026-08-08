// Package adapters re-exports the canonical script repository ports for
// compatibility with existing application and API consumers.
package adapters

import "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"

type ScriptRepository = ports.ScriptRepository
type ScriptListFilter = ports.ScriptListFilter
type ScriptSectionRecord = ports.ScriptSectionRecord
type ScriptStockMatchRecord = ports.ScriptStockMatchRecord
type ScriptResearchSource = ports.ScriptResearchSource
type ScriptOutlineSectionRecord = ports.ScriptOutlineSectionRecord
type ScriptGenerationLog = ports.ScriptGenerationLog
type ScriptRecord = ports.ScriptRecord
