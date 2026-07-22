package intent

import (
	"context"
	"slices"
	"testing"
)

func TestIntentResolverRegistry_ItalianKeywordsAndEntities(t *testing.T) {
	r := NewDefaultResolver()
	intent, err := r.Resolve(context.Background(), "it", "I Maya studiavano le stelle", "i maya studiavano le stelle")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"maya", "studiavano", "stelle"}
	if len(intent.Keywords) != len(want) {
		t.Fatalf("keywords = %v, want %v", intent.Keywords, want)
	}
	for i, w := range want {
		if intent.Keywords[i] != w {
			t.Errorf("keyword[%d] = %q, want %q", i, intent.Keywords[i], w)
		}
	}
	if len(intent.SearchKeywords) != len(want) {
		t.Errorf("search keywords = %v, want %v", intent.SearchKeywords, want)
	}

	// Maya is capitalised in the original text and should be detected
	// as an entity.
	if !slices.Contains(intent.Entities, "maya") {
		t.Errorf("expected entity 'maya' in %v", intent.Entities)
	}
}

func TestIntentResolverRegistry_EnglishActionsAndNegativeConcepts(t *testing.T) {
	r := NewDefaultResolver()
	intent, err := r.Resolve(context.Background(), "en-us", "Not walking alone in Rome", "not walking alone in rome")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !slices.Contains(intent.VisualActions, "walking") {
		t.Errorf("expected visual action 'walking', got %v", intent.VisualActions)
	}
	if !slices.Contains(intent.Actions, "walking") {
		t.Errorf("expected action 'walking', got %v", intent.Actions)
	}
	if !slices.Contains(intent.NegativeConcepts, "not") {
		t.Errorf("expected negative concept 'not', got %v", intent.NegativeConcepts)
	}
	if !slices.Contains(intent.Objects, "alone") || !slices.Contains(intent.Objects, "rome") {
		t.Errorf("expected objects to contain 'alone' and 'rome', got %v", intent.Objects)
	}
}

func TestIntentResolverRegistry_LanguageTagFallback(t *testing.T) {
	r := NewDefaultResolver()
	intent, err := r.Resolve(context.Background(), "fr", "Le chat", "le chat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The fallback resolver should keep the meaningful word "chat".
	if !slices.Contains(intent.Keywords, "chat") {
		t.Errorf("expected keyword 'chat' from fallback resolver, got %v", intent.Keywords)
	}
}

func TestIntentResolverRegistry_SentenceInitialArticleNotEntity(t *testing.T) {
	r := NewDefaultResolver()

	// English: "The" is sentence-initial and should not be treated
	// as a named entity.
	intent, err := r.Resolve(context.Background(), "en", "The Boxer enters the ring", "the boxer enters the ring")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slices.Contains(intent.Entities, "the") {
		t.Errorf("did not expect 'the' as an sentence-initial entity, got %v", intent.Entities)
	}
	if !slices.Contains(intent.Entities, "boxer") {
		t.Errorf("expected entity 'boxer', got %v", intent.Entities)
	}

	// Italian: "Il" is sentence-initial and should not be treated as
	// a named entity.
	intent, err = r.Resolve(context.Background(), "it", "Il Gatto corre veloce", "il gatto corre veloce")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slices.Contains(intent.Entities, "il") {
		t.Errorf("did not expect 'il' as a sentence-initial entity, got %v", intent.Entities)
	}
	if !slices.Contains(intent.Entities, "gatto") {
		t.Errorf("expected entity 'gatto', got %v", intent.Entities)
	}
}

func TestIntentResolverRegistry_Version(t *testing.T) {
	r := NewDefaultResolver()
	if r.Version() == "" {
		t.Error("expected non-empty resolver version")
	}
}
