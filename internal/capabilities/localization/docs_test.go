package localization

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeDocPublisher records the publish input and returns a fixed result (or
// error).
type fakeDocPublisher struct {
	result *DocPublishResult
	err    error
	got    DocPublishInput
}

func (f *fakeDocPublisher) Publish(_ context.Context, in DocPublishInput) (*DocPublishResult, error) {
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func unorderedEntries() []LocalizedDocumentEntry {
	return []LocalizedDocumentEntry{
		{Language: "it", Priority: 2, DriveLink: "https://drive/it", DurationMS: 9000},
		{Language: "en", Priority: 0, DriveLink: "https://drive/en", DurationMS: 8000},
		{Language: "es", Priority: 1, DriveLink: "https://drive/es", DurationMS: 8500},
	}
}

// TestAssemble_OrdersByPriorityAndPublishes verifies the assembler orders
// entries by priority and publishes the rendered HTML with that order.
func TestAssemble_OrdersByPriorityAndPublishes(t *testing.T) {
	publisher := &fakeDocPublisher{result: &DocPublishResult{ID: "doc-1", Link: "https://docs/doc-1"}}
	a, err := NewDocumentAssembler(publisher)
	if err != nil {
		t.Fatalf("NewDocumentAssembler: %v", err)
	}

	ref, err := a.Assemble(context.Background(), AssembleInput{
		Title:          "Localization",
		FolderID:       "folder-1",
		IdempotencyKey: "localization:asset:1",
		Entries:        unorderedEntries(),
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// Ordered entries: en(0), es(1), it(2).
	for i, lang := range []string{"en", "es", "it"} {
		if ref.Entries[i].Language != lang {
			t.Fatalf("ref.Entries[%d].Language: got %q, want %q", i, ref.Entries[i].Language, lang)
		}
	}
	if ref.ID != "doc-1" || ref.Link != "https://docs/doc-1" {
		t.Errorf("ref: got id=%q link=%q", ref.ID, ref.Link)
	}

	// The published content must carry the languages in priority order.
	content := publisher.got.Content
	en := strings.Index(content, "English")
	es := strings.Index(content, "Spanish")
	it := strings.Index(content, "Italian")
	if en < 0 || es < 0 || it < 0 || !(en < es && es < it) {
		t.Fatalf("published content must order English < Spanish < Italian: %q", content)
	}
	if publisher.got.Title != "Localization" || publisher.got.FolderID != "folder-1" || publisher.got.IdempotencyKey != "localization:asset:1" {
		t.Errorf("publish input: got %+v", publisher.got)
	}
}

// TestAssemble_PublishFailureKeepsOrderedEntries verifies a publish failure
// still returns the ordered entries (fail-soft on entries, fail-closed on the
// link) with the error surfaced.
func TestAssemble_PublishFailureKeepsOrderedEntries(t *testing.T) {
	publisher := &fakeDocPublisher{err: errors.New("docs api down")}
	a, err := NewDocumentAssembler(publisher)
	if err != nil {
		t.Fatalf("NewDocumentAssembler: %v", err)
	}

	ref, err := a.Assemble(context.Background(), AssembleInput{Title: "T", Entries: unorderedEntries()})
	if err == nil {
		t.Fatal("Assemble must surface the publish error")
	}
	if ref == nil || len(ref.Entries) != 3 {
		t.Fatalf("ref must keep ordered entries on publish failure: %+v", ref)
	}
	if ref.Entries[0].Language != "en" || ref.ID != "" || ref.Link != "" {
		t.Errorf("ref on failure: got %+v, want en first with no fabricated link", ref)
	}
}

// TestAssemble_DoesNotMutateInput verifies the caller's slice is not
// reordered in place.
func TestAssemble_DoesNotMutateInput(t *testing.T) {
	publisher := &fakeDocPublisher{result: &DocPublishResult{ID: "doc"}}
	a, err := NewDocumentAssembler(publisher)
	if err != nil {
		t.Fatalf("NewDocumentAssembler: %v", err)
	}

	in := unorderedEntries()
	if _, err := a.Assemble(context.Background(), AssembleInput{Title: "T", Entries: in}); err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if in[0].Language != "it" || in[1].Language != "en" || in[2].Language != "es" {
		t.Fatalf("input slice was mutated: %+v", in)
	}
}

// TestRenderLocalizedDocument_Deterministic verifies the pure renderer is
// stable and escapes markup in the title.
func TestRenderLocalizedDocument_Deterministic(t *testing.T) {
	entries := []LocalizedDocumentEntry{
		{Language: "en", DriveLink: "https://x/en", DurationMS: 8000},
		{Language: "es", DurationMS: 8500}, // no link
	}
	title := `Localization <script>`
	first := RenderLocalizedDocument(title, entries)
	second := RenderLocalizedDocument(title, entries)
	if first != second {
		t.Fatalf("render must be deterministic:\n%s\n%s", first, second)
	}
	if strings.Contains(first, "<script>") {
		t.Fatalf("title markup must be escaped: %q", first)
	}
	if !strings.Contains(first, "English") || !strings.Contains(first, "Spanish") {
		t.Fatalf("render must list every language: %q", first)
	}
	if !strings.Contains(first, "8000 ms") || !strings.Contains(first, "8500 ms") {
		t.Fatalf("render must include durations: %q", first)
	}
	if strings.Contains(first, "https://x/en") && !strings.Contains(first, `<a href="https://x/en">`) {
		t.Fatalf("drive link must be rendered as an anchor: %q", first)
	}
}

// TestLanguageLabel verifies the BCP-47 → display name mapping and its
// fallback.
func TestLanguageLabel(t *testing.T) {
	if got := LanguageLabel("pt-BR"); got != "Portuguese (Brazil)" {
		t.Errorf("LanguageLabel(pt-BR): got %q", got)
	}
	if got := LanguageLabel("en"); got != "English" {
		t.Errorf("LanguageLabel(en): got %q", got)
	}
	if got := LanguageLabel("zz-ZZ"); got != "zz-ZZ" {
		t.Errorf("LanguageLabel(zz-ZZ): got %q, want raw fallback", got)
	}
}

// TestAssemble_NilPublisherFailsConstruction verifies the assembler cannot be
// built without a publisher.
func TestAssemble_NilPublisherFailsConstruction(t *testing.T) {
	if _, err := NewDocumentAssembler(nil); err == nil {
		t.Fatal("NewDocumentAssembler must reject a nil publisher")
	}
}
