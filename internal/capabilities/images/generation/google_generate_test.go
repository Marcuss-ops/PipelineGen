package generation

import (
	"context"
	"testing"
)

// TestGenerateSmartImage_ReturnsNotImplemented verifies that GenerateSmartImage
// returns ErrImageGenNotImplemented after the Google Slides API removal.
func TestGenerateSmartImage_ReturnsNotImplemented(t *testing.T) {
	s := &Service{}
	_, err := s.GenerateSmartImage(context.Background(), "test", "", "", nil, nil, 1920, 1080, "", false)
	if err == nil {
		t.Fatal("expected ErrImageGenNotImplemented, got nil")
	}
	if err != ErrImageGenNotImplemented {
		t.Fatalf("expected ErrImageGenNotImplemented, got %v", err)
	}
}
