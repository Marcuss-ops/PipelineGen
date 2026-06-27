package usecase

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
)

// ScriptListFilter is the canonical filter for listing scripts.
// Aliased to the canonical adapters type so consumers in api/script
// (which use the usecase alias as a public type) can pass it
// directly to ScriptRepository.ListScripts (which expects
// adapters.ScriptListFilter — the same underlying type).
type ScriptListFilter = adapters.ScriptListFilter
