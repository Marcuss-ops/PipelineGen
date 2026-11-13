package overlays

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreferAdministrativeMapCandidatesDropsStreetWhenCityPresent(t *testing.T) {
	street := MapCandidate{EntityID: "street", Label: "via Pascoli"}
	city := MapCandidate{EntityID: "city", Label: "Garlasco"}

	got := preferAdministrativeMapCandidates([]MapCandidate{street, city})
	require.Len(t, got, 1)
	require.Equal(t, "Garlasco", got[0].Label)

	onlyStreet := preferAdministrativeMapCandidates([]MapCandidate{street})
	require.Len(t, onlyStreet, 1, "a street-only scene keeps its street map")
}
