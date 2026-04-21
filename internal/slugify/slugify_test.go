package slugify

import (
	"testing"
	"github.com/stretchr/testify/assert"
)

func TestSlugifyMarshal(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Gervonta Davis", "gervonta-davis"},
		{"Gervonta_Davis", "gervonta-davis"},
		{"Gervontà Davis", "gervonta-davis"},
		{"  Boxe   Highlights  ", "boxe-highlights"},
		{"Upper-cut @ Mento!", "upper-cut-mento"},
		{"100% Boxing!", "100-boxing"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, Marshal(tc.input))
		})
	}
}
