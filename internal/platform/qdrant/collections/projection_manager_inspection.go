package collections

// projection_manager_inspection.go owns the read-only inspection helpers of
// the CollectionManager projection lifecycle: resolving a physical collection
// back to its projection identity and validating a hydrated registry.
//
// Split out of projection_manager.go to stay under the 600-LOC strict cap
// (godlike/08 forward-prevention gate). Functions moved verbatim: no
// behaviour change, same package.

import (
	"fmt"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

func (cm *CollectionManager) projectionByCollection(collection string) (string, bool) {
	cm.projectionMu.RLock()
	defer cm.projectionMu.RUnlock()
	var found string
	var active string
	var ready string
	matches := 0
	activeMatches := 0
	readyMatches := 0
	for id, projection := range cm.projections {
		if projection.CollectionName == collection {
			found = id
			matches++
			switch capregistry.ProjectionStatus(projection.Status) {
			case capregistry.ProjectionActive:
				active = id
				activeMatches++
			case capregistry.ProjectionReady:
				ready = id
				readyMatches++
			}
		}
	}
	// A fixed physical production collection can have multiple historical
	// rebuild rows. Runtime resolution must select the sole ACTIVE row rather
	// than treating those historical rows as an unknown collection.
	if activeMatches == 1 {
		return active, true
	}
	if activeMatches > 1 {
		return "", false
	}
	if readyMatches == 1 {
		return ready, true
	}
	return found, matches == 1
}

func validateHydratedProjections(projections []capregistry.Projection, registrySequence int64) error {
	activeByAlias := make(map[string]string)
	for _, projection := range projections {
		if projection.ProjectionID == "" || projection.ProjectionType == "" || projection.CollectionName == "" || projection.AliasName == "" {
			return fmt.Errorf("hydrate projection registry: invalid projection identity %q", projection.ProjectionID)
		}
		switch capregistry.ProjectionStatus(projection.Status) {
		case capregistry.ProjectionBuilding, capregistry.ProjectionValidating,
			capregistry.ProjectionReady, capregistry.ProjectionActive,
			capregistry.ProjectionRetired, capregistry.ProjectionFailed,
			capregistry.ProjectionFailedCleaned:
		default:
			return fmt.Errorf("hydrate projection registry: unknown status %q for %q", projection.Status, projection.ProjectionID)
		}
		if projection.SourceRegistrySeq > registrySequence {
			return fmt.Errorf("hydrate projection registry: projection %q is ahead of canonical sequence: projection_seq=%d registry_seq=%d", projection.ProjectionID, projection.SourceRegistrySeq, registrySequence)
		}
		if projection.Status == string(capregistry.ProjectionActive) {
			if previous, exists := activeByAlias[projection.AliasName]; exists {
				return fmt.Errorf("hydrate projection registry: multiple ACTIVE projections for alias %q: %q and %q", projection.AliasName, previous, projection.ProjectionID)
			}
			activeByAlias[projection.AliasName] = projection.ProjectionID
		}
	}
	return nil
}
