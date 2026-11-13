package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestNormalizeVisualEntityTypesCorrectsStrongSurfaceCues(t *testing.T) {
	entities := []VisualEntity{
		{Text: "Villa Pamphili", Type: scriptpkg.EntityTypePerson},
		{Text: "Francis Kaufmann", Type: scriptpkg.EntityTypePerson},
		{Text: "Associated Press", Type: scriptpkg.EntityTypePerson},
	}
	normalizeVisualEntityTypes(entities)
	require.Equal(t, scriptpkg.EntityType("GPE"), entities[0].Type)
	require.Equal(t, scriptpkg.EntityTypePerson, entities[1].Type)
	require.Equal(t, scriptpkg.EntityType("ORG"), entities[2].Type)
}
