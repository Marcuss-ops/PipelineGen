package script

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCloneVideoMetadata_CleansTags(t *testing.T) {
	input := &VideoMetadata{
		Title:       "  Titolo  ",
		Description: "  Descrizione  ",
		Tags: []string{
			" boxe ",
			"",
			"   ",
			"Mike Tyson",
		},
	}

	result := CloneVideoMetadata(input)

	require.Equal(t, "Titolo", result.Title)
	require.Equal(t, "Descrizione", result.Description)
	require.Equal(t, []string{"boxe", "Mike Tyson"}, result.Tags)
}
