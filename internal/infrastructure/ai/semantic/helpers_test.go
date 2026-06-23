package semantic

import "testing"

func TestCompactSemanticPhrase(t *testing.T) {
	got := CompactSemanticPhrase("A very long and noisy generated prompt about ancient ruins and emerald water in the Riviera Maya", 4, 40)
	if got == "" {
		t.Fatal("expected compact phrase")
	}
	if len(got) > 40 {
		t.Fatalf("expected compact phrase <= 40 chars, got %q", got)
	}
}

func TestCompactGeneratedPayload(t *testing.T) {
	meta := &Payload{
		MediaType:           "image",
		Source:              "generated",
		Generator:           "google-flow",
		PromptOriginal:      "a whiteboard flowchart for parsing drive roots",
		SemanticDescription: "a whiteboard flowchart for parsing drive roots",
		Subjects:            []string{"a whiteboard flowchart for parsing drive roots"},
		ConceptTags:         []string{"workflow", "routing", "storage"},
		VisualObjects:       []string{"flowchart", "arrows", "folders"},
		EmotionalTone:       []string{"clarity"},
		Tags:                []string{"a whiteboard flowchart for parsing drive roots", "workflow"},
		Style:               []string{"whiteboard"},
	}

	out := CompactGeneratedPayload(meta)
	if out == nil {
		t.Fatal("expected payload")
	}
	if len(out.Subjects) == 0 || out.Subjects[0] == "a whiteboard flowchart for parsing drive roots" {
		t.Fatalf("expected compact subjects, got %#v", out.Subjects)
	}
	if len(out.SubjectSlugs) == 0 {
		t.Fatal("expected subject slugs")
	}
	if len(out.SearchText) == 0 || len(out.SearchText) > 120 {
		t.Fatalf("unexpected search_text: %q", out.SearchText)
	}
	if len(out.Tags) == 0 || len(out.Tags) > 12 {
		t.Fatalf("unexpected tags: %#v", out.Tags)
	}
	if out.SemanticDescription == "a whiteboard flowchart for parsing drive roots" {
		t.Fatalf("expected normalized semantic description, got %q", out.SemanticDescription)
	}
}
