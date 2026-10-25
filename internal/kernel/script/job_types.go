package script

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

// Canonical script job type constants.
//
// The SHARED wire strings are OWNED by internal/kernel/job (godlike/06 one
// owner per fact); the entries below are compile-time re-exports. The
// script-only sibling types have a single declaration site and stay here.
const (
	// TypeGenerate is the canonical job type for script generation.
	TypeGenerate = job.TypeScriptGenerate

	// TypeVoiceoverSibling is the sibling job type for voiceover assets
	// spawned by the script generation handler.
	TypeVoiceoverSibling = "script.spawn_voiceover"

	// TypeImageSibling is the sibling job type for image assets spawned
	// by the script generation handler.
	TypeImageSibling = "script.spawn_images"

	// TypeGenerateItem is the per-item child job type for script.generate batches.
	TypeGenerateItem = job.TypeScriptGenerateItem
)
