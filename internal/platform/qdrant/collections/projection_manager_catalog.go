package collections

import (
	"fmt"

	"go.uber.org/zap"

	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

// NewProjectionManagerFor validates the projection contract and returns a
// CollectionManager bound to that projection's canonical manifest
// (contract.Schema). It is the single composition entry point for building a
// dedicated manager per projection; callers must not re-derive the schema
// from a raw IndexSchema literal.
func NewProjectionManagerFor(contract qdrantschema.ProjectionContract, client *transport.Client, log *zap.Logger) (*CollectionManager, error) {
	if err := contract.Validate(); err != nil {
		return nil, fmt.Errorf("build projection manager: %w", err)
	}
	return NewProjectionManager(client, contract.Schema, log), nil
}

// ProjectionManagers holds one dedicated CollectionManager per canonical
// projection (media_assets, media_frames, media_concepts), each built from
// its own ProjectionContract. The three managers share the transport client
// but are bound to distinct, non-overlapping schemas.
type ProjectionManagers struct {
	Assets   *CollectionManager
	Frames   *CollectionManager
	Concepts *CollectionManager
}

// NewProjectionManagers builds the three dedicated managers from the
// canonical projection catalog, validating the 3-way separation first so a
// drift in the contracts fails construction rather than producing two
// managers over the same collection.
func NewProjectionManagers(client *transport.Client, log *zap.Logger) (*ProjectionManagers, error) {
	contracts := qdrantschema.AllProjections()
	if err := qdrantschema.ValidateProjectionSeparation(contracts); err != nil {
		return nil, fmt.Errorf("build projection managers: %w", err)
	}

	var out ProjectionManagers
	for _, contract := range contracts {
		mgr, err := NewProjectionManagerFor(contract, client, log)
		if err != nil {
			return nil, err
		}
		switch contract.Kind {
		case qdrantschema.ProjectionMediaAssets:
			out.Assets = mgr
		case qdrantschema.ProjectionMediaFrames:
			out.Frames = mgr
		case qdrantschema.ProjectionMediaConcepts:
			out.Concepts = mgr
		}
	}
	return &out, nil
}

// For returns the dedicated manager for the given projection kind, or nil
// when the kind is unknown or the manager was never built.
func (m *ProjectionManagers) For(kind qdrantschema.ProjectionKind) *CollectionManager {
	if m == nil {
		return nil
	}
	switch kind {
	case qdrantschema.ProjectionMediaAssets:
		return m.Assets
	case qdrantschema.ProjectionMediaFrames:
		return m.Frames
	case qdrantschema.ProjectionMediaConcepts:
		return m.Concepts
	default:
		return nil
	}
}
