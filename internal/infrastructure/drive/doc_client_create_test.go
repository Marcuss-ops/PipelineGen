package drive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/api/docs/v1"
	"google.golang.org/api/option"
)

const createDocHTML = `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body>
<h1>TITOLO TEST</h1>
<section><h2>Scene 1</h2><p>TESTO SCENA UNO</p></section>
<h2>SpecScene JSON</h2><pre><code>{
  "version": 1,
  "scenes": [{"id": "scene-0"}]
}</code></pre>
</body></html>`

// TestCreateDoc_HTMLCarriesTitleAndSpecSceneJSON drives CreateDoc through a
// stub Docs API and asserts the emitted BatchUpdate payload carries the
// caller-facing title and the SpecScene JSON markers, and that the title is
// styled as a heading.
func TestCreateDoc_HTMLCarriesTitleAndSpecSceneJSON(t *testing.T) {
	t.Parallel()

	var (
		mu              sync.Mutex
		batchUpdateBody []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/documents" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"documentId": "doc-test-1", "title": "TITOLO TEST"}`))
		case strings.HasSuffix(r.URL.Path, ":batchUpdate") && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			batchUpdateBody = body
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	service, err := docs.NewService(ctx,
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("docs.NewService: %v", err)
	}

	client := &DocClientImpl{
		docsService:  service,
		driveService: nil,
	}

	doc, err := client.CreateDoc(ctx, "TITOLO TEST", createDocHTML, "")
	if err != nil {
		t.Fatalf("CreateDoc: %v", err)
	}
	if doc == nil || doc.ID != "doc-test-1" {
		t.Fatalf("unexpected doc: %+v", doc)
	}

	mu.Lock()
	body := batchUpdateBody
	mu.Unlock()
	if len(body) == 0 {
		t.Fatal("expected a BatchUpdate request, got none")
	}

	var req docs.BatchUpdateDocumentRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal batchUpdate body: %v", err)
	}

	var inserted string
	for _, r := range req.Requests {
		if r.InsertText != nil {
			inserted = r.InsertText.Text
			break
		}
	}
	if inserted == "" {
		t.Fatal("expected an InsertText request")
	}
	for _, want := range []string{"TITOLO TEST", "TESTO SCENA UNO", `"version": 1`, `"scene-0"`} {
		if !strings.Contains(inserted, want) {
			t.Fatalf("inserted text missing %q\n%s", want, inserted)
		}
	}

	var titleHeading bool
	for _, r := range req.Requests {
		if r.UpdateParagraphStyle != nil &&
			r.UpdateParagraphStyle.ParagraphStyle != nil &&
			r.UpdateParagraphStyle.ParagraphStyle.NamedStyleType == "HEADING_1" {
			titleHeading = true
			break
		}
	}
	if !titleHeading {
		t.Fatal("expected the title to be styled as HEADING_1")
	}
}
