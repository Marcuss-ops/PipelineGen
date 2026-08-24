package voiceover

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Re-export canonical job type constants from kernel/job for back-compat.
const (
	TypeGenerate     = job.TypeVoiceoverGenerate
	TypeBatch        = job.TypeVoiceoverBatch
	TypeGenerateItem = job.TypeVoiceoverGenerateItem
	TypePromo        = job.TypeVoiceoverPromo
)
