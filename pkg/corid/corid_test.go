package corid

import (
	"context"
	"testing"
)

func TestFromContext(t *testing.T) {
	ctx := WithCorrelationID(context.Background(), "test-id-123")
	id := FromContext(ctx)
	if id != "test-id-123" {
		t.Errorf("expected 'test-id-123', got %q", id)
	}
}

func TestFromContext_Empty(t *testing.T) {
	ctx := context.Background()
	id := FromContext(ctx)
	if id != "" {
		t.Errorf("expected empty, got %q", id)
	}
}

func TestFromContext_Nil(t *testing.T) {
	id := FromContext(nil)
	if id != "" {
		t.Errorf("expected empty for nil ctx, got %q", id)
	}
}

func TestWithCorrelationID_Empty(t *testing.T) {
	ctx := WithCorrelationID(context.Background(), "")
	if id := FromContext(ctx); id != "" {
		t.Errorf("expected empty, got %q", id)
	}
}

func TestWithCorrelationID_Overwrite(t *testing.T) {
	ctx := WithCorrelationID(context.Background(), "first")
	ctx = WithCorrelationID(ctx, "second")
	id := FromContext(ctx)
	if id != "second" {
		t.Errorf("expected 'second', got %q", id)
	}
}

func TestWithCorrelationID_PreservesExisting(t *testing.T) {
	ctx := WithCorrelationID(context.Background(), "original")
	// Adding empty should preserve the original
	ctx2 := WithCorrelationID(ctx, "")
	if id := FromContext(ctx2); id != "original" {
		t.Errorf("expected 'original', got %q", id)
	}
}

func TestWithCorrelationID_ReturnsSameContext(t *testing.T) {
	base := context.Background()
	result := WithCorrelationID(base, "")
	if result != base {
		t.Error("expected same context when id is empty")
	}
}

func TestFromContext_DifferentValues(t *testing.T) {
	ctx1 := WithCorrelationID(context.Background(), "trace-1")
	ctx2 := WithCorrelationID(context.Background(), "trace-2")
	if FromContext(ctx1) == FromContext(ctx2) {
		t.Error("expected different correlation IDs")
	}
}
