package scriptgeneration

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/modelvalues"
	domain "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// DurableResultToDomain projects the durable capability result onto the
// canonical domain envelope. The pure mapping lives in modelvalues so the
// scripts root stays a compatibility facade; this wrapper preserves the
// scriptgen.DurableResultToDomain surface consumed by the jobs handlers.
func DurableResultToDomain(in *contract.GenerateResult) *domain.GenerationResult {
	return modelvalues.DurableResultToDomain(in)
}
