// Package ports defines neutral entity extraction contracts shared by
// capabilities and platform adapters.
package ports

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// EntityExtractor is the single neutral port for source-grounded entity
// extraction. Implementations must preserve the request segment identity and
// must not invent values absent from the source text.
type EntityExtractor interface {
	ExtractEntities(context.Context, script.EntityExtractionRequest) (*script.EntityResult, error)
}

// BatchEntityExtractor is an optional optimization of EntityExtractor.
type BatchEntityExtractor interface {
	EntityExtractor
	ExtractEntitiesBatch(context.Context, []script.EntityExtractionRequest) ([]script.EntityExtractionBatchResult, error)
}
