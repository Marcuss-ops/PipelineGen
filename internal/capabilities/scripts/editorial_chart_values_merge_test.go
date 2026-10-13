package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeChartValuesKeepsOnlyGroundedStrictMatches(t *testing.T) {
	text := "Le vendite sono salite del 25% e il budget è 2 milioni di euro, poi 3.5 punti."
	got := mergeChartValues(nil, []ChartValueCandidate{
		{Text: "25%", Value: 25},
		{Text: "3.5", Value: 3.5},
		{Text: "2 milioni", Value: 2}, // not a strict literal: rejected by parser
		{Text: "99", Value: 99},       // not present in scene text
		{Text: "25%", Value: 25},      // duplicate
		{Text: "3.5", Value: 4},       // worker value disagrees with parse
	}, text)
	assert.Equal(t, []string{"25%", "3.5"}, got)
}

func TestMergeChartValuesKeepsExistingAndCapsAtManifestLimit(t *testing.T) {
	text := "1 2 3 4 5 6 7 8"
	candidates := []ChartValueCandidate{
		{Text: "3", Value: 3}, {Text: "4", Value: 4}, {Text: "5", Value: 5},
		{Text: "6", Value: 6}, {Text: "7", Value: 7}, {Text: "8", Value: 8},
	}
	got := mergeChartValues([]string{"1", "2"}, candidates, text)
	assert.Equal(t, []string{"1", "2", "3", "4", "5", "6"}, got)
}

func TestMergeChartValuesReturnsNilWhenNothingVerifies(t *testing.T) {
	assert.Nil(t, mergeChartValues(nil, []ChartValueCandidate{{Text: "7", Value: 8}}, "7 giorni"))
}
