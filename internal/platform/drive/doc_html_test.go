package drive

import (
	"strings"
	"testing"
)

const canonicalDocHTML = `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body>
<h1>TITOLO TEST</h1>
<section><h2>Scene 1</h2><p>TESTO SCENA UNO</p><p><strong>Voiceover:</strong> <a href="VOICE-IT-1">VOICE-IT-1</a></p></section>
<h2>SpecScene JSON</h2><pre><code>{
  "version": 1,
  "scenes": ["a", "b"]
}</code></pre>
</body></html>`

func TestBuildInsertRequests_PreservesTitleHeadingAndJSON(t *testing.T) {
	t.Parallel()

	reqs, err := BuildInsertRequests(canonicalDocHTML)
	if err != nil {
		t.Fatalf("BuildInsertRequests: %v", err)
	}
	if len(reqs) == 0 {
		t.Fatal("expected at least one request")
	}
	if reqs[0].InsertText == nil {
		t.Fatal("first request must insert text")
	}

	inserted := reqs[0].InsertText.Text
	for _, want := range []string{"TITOLO TEST", "TESTO SCENA UNO", `"version": 1`, `"scenes": ["a", "b"]`} {
		if !strings.Contains(inserted, want) {
			t.Fatalf("inserted text missing %q\n%s", want, inserted)
		}
	}

	var h1 bool
	for _, r := range reqs {
		if r.UpdateParagraphStyle != nil && r.UpdateParagraphStyle.ParagraphStyle != nil &&
			r.UpdateParagraphStyle.ParagraphStyle.NamedStyleType == "HEADING_1" {
			h1 = true
			break
		}
	}
	if !h1 {
		t.Fatal("expected the title to be styled as HEADING_1")
	}
}

func TestBuildInsertRequests_HeadingAndVoiceoverLink(t *testing.T) {
	t.Parallel()

	reqs, err := BuildInsertRequests(canonicalDocHTML)
	if err != nil {
		t.Fatalf("BuildInsertRequests: %v", err)
	}

	var (
		h2, bold, link bool
	)
	for _, r := range reqs {
		if r.UpdateParagraphStyle != nil && r.UpdateParagraphStyle.ParagraphStyle != nil &&
			r.UpdateParagraphStyle.ParagraphStyle.NamedStyleType == "HEADING_2" {
			h2 = true
		}
		if r.UpdateTextStyle != nil && r.UpdateTextStyle.TextStyle != nil {
			if r.UpdateTextStyle.TextStyle.Bold {
				bold = true
			}
			if r.UpdateTextStyle.TextStyle.Link != nil && r.UpdateTextStyle.TextStyle.Link.Url == "VOICE-IT-1" {
				link = true
			}
		}
	}
	if !h2 {
		t.Fatal("expected a HEADING_2 paragraph")
	}
	if !bold {
		t.Fatal("expected a bold Voiceover label run")
	}
	if !link {
		t.Fatal("expected a link run for VOICE-IT-1")
	}
}

func TestBuildInsertRequests_StylesCodeFont(t *testing.T) {
	t.Parallel()

	reqs, err := BuildInsertRequests(canonicalDocHTML)
	if err != nil {
		t.Fatalf("BuildInsertRequests: %v", err)
	}

	var code bool
	for _, r := range reqs {
		if r.UpdateTextStyle != nil && r.UpdateTextStyle.TextStyle.WeightedFontFamily != nil &&
			r.UpdateTextStyle.TextStyle.WeightedFontFamily.FontFamily == "Courier New" {
			code = true
			break
		}
	}
	if !code {
		t.Fatal("expected the SpecScene JSON code block to use a monospace font")
	}
}

func TestUTF16Len(t *testing.T) {
	t.Parallel()

	cases := map[string]int{
		"":    0,
		"abc": 3,
		"è":   1, // U+00E8: 2 bytes, 1 UTF-16 unit
		"😀":   2, // U+1F600: surrogate pair, 2 UTF-16 units
		"aèb": 3,
	}
	for in, want := range cases {
		if got := utf16Len(in); got != want {
			t.Fatalf("utf16Len(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestBuildDocumentInsertRequests_UTF16Ranges(t *testing.T) {
	t.Parallel()

	// A paragraph whose text contains an accented letter must still yield
	// ranges measured in UTF-16 code units, not bytes.
	blocks := []docBlock{
		{style: blockNormal, runs: []docRun{{text: "Città è bella"}}},
	}
	reqs := buildDocumentInsertRequests(blocks)
	if len(reqs) < 2 {
		t.Fatalf("expected at least insert + style requests, got %d", len(reqs))
	}
	ps := reqs[1].UpdateParagraphStyle
	if ps == nil {
		t.Fatal("second request must be paragraph style")
	}
	// "Città è bella" = 13 UTF-16 units → range [1, 14).
	if ps.Range.StartIndex != 1 || ps.Range.EndIndex != 14 {
		t.Fatalf("range = [%d, %d), want [1, 14)", ps.Range.StartIndex, ps.Range.EndIndex)
	}
}
