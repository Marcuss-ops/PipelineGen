package mediamemory

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// SlotKind is an alias for the canonical media.SlotKind kept for backward
// compatibility until all callers are migrated to media.SlotKind directly.
type SlotKind = media.SlotKind
