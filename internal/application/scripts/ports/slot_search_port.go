package ports

import (
	"errors"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/clipfolder"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

const (
	SlotsPerSlotDefaultTimeout        = 3 * time.Second
	SlotsPerSlotDefaultCandidateLimit = 20
)

// SlotsSearchOptions controls the canonical per-slot semantic search.
type SlotsSearchOptions struct {
	PerSlotTimeout         time.Duration
	PerSlotCandidateLimit  int
	MinScore               float64
	SourceFilter           string
	Category               string
	MediaType              string
	WorkspaceID            string
	IsSystem               bool
	Folder                 *clipfolder.ClipFolderRef
	IncludeRightRestricted bool
}

// SlotsSearchResult contains candidates and auditable per-slot failures.
type SlotsSearchResult struct {
	ByRef         map[string][]scriptpkg.ClipCandidate
	TruncatedRefs []string
	ErroredRefs   map[string]error
	Duration      time.Duration
}

var (
	ErrSlotSearchInvalidPlan     = errors.New("slot search: invalid plan")
	ErrSlotSearchContextCanceled = errors.New("slot search: context canceled")
)
