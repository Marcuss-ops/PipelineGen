package client

import (
	"velox/go-master/internal/ml/ollama/types"
	"testing"
)

func TestNewGenerator(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		gen := NewGenerator(nil)
		if gen == nil {
			t.Error("Expected non-nil generator")
		}
	})
}