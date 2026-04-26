package client

import (
	"testing"
)

func TestNewClient(t *testing.T) {
	t.Run("nil base URL", func(t *testing.T) {
		client := NewClient("", "")
		if client == nil {
			t.Error("Expected non-nil client")
		}
	})
}