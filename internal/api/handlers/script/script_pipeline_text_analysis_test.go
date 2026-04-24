package script

import (
	"context"
	"testing"
)

func TestBuildTextAnalysisDefaultTextSplitsIntoThreeChapters(t *testing.T) {
	h := &ScriptPipelineHandler{}
	text := `### First section

One sentence here. Another sentence here.

### Second section

This is the middle block. It has two sentences too.

### Third section

Final section closes the text. It ends cleanly.`

	resp, err := h.buildTextAnalysis(context.Background(), TextAnalysisRequest{
		Text:        text,
		Duration:    300,
		MaxChapters: 4,
	})
	if err != nil {
		t.Fatalf("buildTextAnalysis() error = %v", err)
	}
	if resp == nil {
		t.Fatal("buildTextAnalysis() returned nil response")
	}
	if got := len(resp.Chapters); got != 3 {
		t.Fatalf("expected 3 chapters, got %d", got)
	}

	for i := range resp.Chapters {
		if resp.Chapters[i].StartPhrase == "" {
			t.Fatalf("chapter %d start phrase is empty", i)
		}
		if resp.Chapters[i].EndPhrase == "" {
			t.Fatalf("chapter %d end phrase is empty", i)
		}
	}
}
