package ollama

import (
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