package intent

import (
	"context"
	"slices"
	"testing"
)

func TestDefaultResolver_ExtractsKeywords(t *testing.T) {
	r := NewDefaultResolver()
	intent, err := r.Resolve(context.Background(), "it", "I Maya studiavano le stelle")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"i", "maya", "studiavano", "le", "stelle"}
	if len(intent.Keywords) != len(want) {
		t.Fatalf("keywords = %v, want %v", intent.Keywords, want)
	}
	for i, w := range want {
		if intent.Keywords[i] != w {
			t.Errorf("keyword[%d] = %q, want %q", i, intent.Keywords[i], w)
		}
	}
}

func TestDefaultResolver_DetectsEntities(t *testing.T) {
	r := NewDefaultResolver()
	intent, err := r.Resolve(context.Background(), "it", "Alessandro Magno conquistava Persepoli")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"alessandro", "magno", "persepoli"}
	for _, w := range want {
		if !slices.Contains(intent.Entities, w) {
			t.Errorf("expected entity %q in %v", w, intent.Entities)
		}
	}
}

func TestDefaultResolver_EmptyInput(t *testing.T) {
	r := NewDefaultResolver()
	intent, err := r.Resolve(context.Background(), "it", "   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intent.Keywords) != 0 || len(intent.Entities) != 0 {
		t.Errorf("expected empty intent, got keywords=%v entities=%v", intent.Keywords, intent.Entities)
	}
}
