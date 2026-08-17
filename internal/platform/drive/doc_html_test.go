package drive

import (
	"strings"
	"testing"

	"google.golang.org/api/docs/v1"
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

// entityImageDocHTML mirrors the exact renderer surface produced by
// writeDocumentEntityImage: the entity <img> lives in its own paragraph
// between the scene text and the "Entity image:" Drive link.
const entityImageDocHTML = `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body>
<h1>NLP Online Images Certification</h1>
<section><h2>Scene 1</h2><p>Dwayne Johnson trained in Los Angeles.</p>
<p><img src="https://images.example/dwayne.jpg" alt="Dwayne Johnson" style="max-width:320px;max-height:240px;" /></p>
<p><strong>Entity image:</strong> <a href="https://drive.google.com/file/d/dwayne/view">https://drive.google.com/file/d/dwayne/view</a></p></section>
</body></html>`

// TestBuildInsertRequests_InsertsInlineEntityImage certifies that the real
// Docs API path materializes the entity <img> as an InsertInlineImage request
// rather than dropping it (which is what the renderer-only test would miss).
func TestBuildInsertRequests_InsertsInlineEntityImage(t *testing.T) {
	t.Parallel()

	reqs, err := BuildInsertRequests(entityImageDocHTML)
	if err != nil {
		t.Fatalf("BuildInsertRequests: %v", err)
	}
	if len(reqs) == 0 || reqs[0].InsertText == nil {
		t.Fatal("first request must insert text")
	}

	var imgReq *docs.InsertInlineImageRequest
	for _, r := range reqs {
		if r.InsertInlineImage != nil {
			if imgReq != nil {
				t.Fatalf("expected exactly one inline image request, got multiple")
			}
			imgReq = r.InsertInlineImage
		}
	}
	if imgReq == nil {
		t.Fatal("expected an InsertInlineImage request for the entity <img>, got none")
	}
	if imgReq.Uri != "https://images.example/dwayne.jpg" {
		t.Fatalf("inline image URI = %q, want the source image URL", imgReq.Uri)
	}
	if imgReq.Location == nil {
		t.Fatal("inline image request has no Location")
	}

	// The image must land after the scene paragraph and before the entity-image
	// link paragraph, i.e. right after the scene text's trailing newline. All
	// indexes are measured in UTF-16 code units, matching the Docs API.
	title := "NLP Online Images Certification"
	sceneText := "Dwayne Johnson trained in Los Angeles."
	wantIndex := int64(1 + utf16Len(title) + 1 + utf16Len("Scene 1") + 1 + utf16Len(sceneText) + 1)
	if imgReq.Location.Index != wantIndex {
		t.Fatalf("inline image index = %d, want %d", imgReq.Location.Index, wantIndex)
	}

	// The image must not displace the following "Entity image:" Drive link:
	// that link is still emitted as a styled run inside the inserted text.
	if !strings.Contains(reqs[0].InsertText.Text, "Entity image: https://drive.google.com/file/d/dwayne/view") {
		t.Fatalf("inserted text missing the entity-image Drive link:\n%s", reqs[0].InsertText.Text)
	}
}

// TestBuildInsertRequests_OrdersInlineImagesRightToLeft certifies that when a
// document carries several inline images the requests are emitted in descending
// index order, so inserting a rightmost image first never shifts the position
// of the images to its left.
func TestBuildInsertRequests_OrdersInlineImagesRightToLeft(t *testing.T) {
	t.Parallel()

	blocks := []docBlock{
		{style: blockNormal, runs: []docRun{{text: "a"}, {image: "https://x/1.jpg"}, {text: "b"}, {image: "https://x/2.jpg"}, {text: "c"}}},
	}
	reqs := buildDocumentInsertRequests(blocks)

	var indexes []int64
	var uris []string
	for _, r := range reqs {
		if r.InsertInlineImage != nil {
			indexes = append(indexes, r.InsertInlineImage.Location.Index)
			uris = append(uris, r.InsertInlineImage.Uri)
		}
	}
	if len(indexes) != 2 {
		t.Fatalf("expected 2 inline image requests, got %d (uris=%v)", len(indexes), uris)
	}
	// Block text is "abc" starting at index 1: image 1 sits after "a" (index 2),
	// image 2 sits after "ab" (index 3). Right-to-left order emits index 3 first.
	if indexes[0] != 3 || indexes[1] != 2 {
		t.Fatalf("inline image indexes = %v, want [3 2] (descending)", indexes)
	}
	if uris[0] != "https://x/2.jpg" || uris[1] != "https://x/1.jpg" {
		t.Fatalf("inline image URIs = %v, want rightmost image first", uris)
	}
}
