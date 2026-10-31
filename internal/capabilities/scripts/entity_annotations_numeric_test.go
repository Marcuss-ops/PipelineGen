package scriptgeneration

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func TestProjectEntityAnnotationsGroundsExplicitNumericQuantity(t *testing.T) {
	text := "La Corte condannò Alberto Stasi a 16 anni di reclusione."
	ann := projectEntityAnnotations(text, "it", scriptpkg.VidRushSegmentResult{})
	require.NotNil(t, ann)
	require.Len(t, ann.SecondaryEntities, 1)
	entity := ann.SecondaryEntities[0]
	require.Equal(t, "16 anni", entity.Text)
	require.Equal(t, "number:16-anni", entity.CanonicalEntityID)
	require.Equal(t, "NUMBER", entity.Type)
	require.Equal(t, 1.0, entity.Confidence)
	span, ok := findEntitySpan(text, "16 anni")
	require.True(t, ok)
	require.Equal(t, []scriptpkg.AnnotationSpan{span}, entity.Mentions)
}
