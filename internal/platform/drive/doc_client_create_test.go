package drive

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestCreateDoc_RetriesTransientCreateError_FailsFastOnPermanent pins the
// document-publish robustness fix at the SDK boundary.
//
// Live evidence (job_1789758060550635114_3ab5353c, 2026-09-18): ONE language's
// document create failed with
//
//	googleapi: Error 500: Internal error encountered., backendError
//
// while every render and every other language's document had already been
// published. The run died — even though the failure was a Google server blip
// and the job was classified retryable — because the create had no retry at
// all. The sibling Drive put path has retried the same class of error forever
// (uploader_put.go), so the document create must too.
//
// The two halves of the contract are:
//  1. a transient 5xx is retried and the publish SUCCEEDS on the second
//     attempt (the run is saved instead of killed);
//  2. a permanent 4xx is NOT retried (a bad title / revoked scope must fail
//     immediately instead of burning two extra round trips and 4 s of sleep).
func TestCreateDoc_RetriesTransientCreateError_FailsFastOnPermanent(t *testing.T) {
	t.Parallel()

	const googleErrorBody = `{"error":{"code":%d,"message":"Internal error encountered.","errors":[{"message":"Internal error encountered.","reason":"backendError"}]}}`

	// docCreateStub serves the Docs API create + batchUpdate surface while
	// counting create attempts and failing the first `failFirst` of them with
	// the given status. It returns the create attempt count so each subtest can
	// assert the retry policy itself (not merely the final error).
	docCreateStub := func(t *testing.T, failStatus, failFirst int) (*DocClientImpl, func() int, func()) {
		t.Helper()
		var (
			mu       sync.Mutex
			attempts int
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/v1/documents" && r.Method == http.MethodPost:
				mu.Lock()
				attempts++
				attempt := attempts
				mu.Unlock()
				if failFirst == 0 || attempt <= failFirst {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(failStatus)
					_, _ = fmt.Fprintf(w, googleErrorBody, failStatus)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"documentId": "doc-retry-1", "title": "TITOLO TEST"}`))
			case strings.HasSuffix(r.URL.Path, ":batchUpdate") && r.Method == http.MethodPost:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			default:
				http.NotFound(w, r)
			}
		}))

		ctx := context.Background()
		service, err := docs.NewService(ctx,
			option.WithEndpoint(srv.URL),
			option.WithoutAuthentication(),
		)
		if err != nil {
			srv.Close()
			t.Fatalf("docs.NewService: %v", err)
		}
		client := &DocClientImpl{docsService: service}
		return client, func() int {
			mu.Lock()
			defer mu.Unlock()
			return attempts
		}, srv.Close
	}

	t.Run("transient 500 is retried and the document is published", func(t *testing.T) {
		t.Parallel()
		client, createAttempts, closeStub := docCreateStub(t, http.StatusInternalServerError, 1)
		defer closeStub()

		doc, err := client.CreateDoc(context.Background(), "TITOLO TEST", createDocHTML, "")
		if err != nil {
			t.Fatalf("a single transient 500 must not fail the publish; got %v", err)
		}
		if doc == nil || doc.ID != "doc-retry-1" {
			t.Fatalf("unexpected doc after retry: %+v", doc)
		}
		if got := createAttempts(); got != 2 {
			t.Fatalf("create attempts = %d; want 2 (one failure + one retry that succeeds)", got)
		}
	})

	t.Run("permanent 4xx fails fast without retrying", func(t *testing.T) {
		t.Parallel()
		client, createAttempts, closeStub := docCreateStub(t, http.StatusBadRequest, 0)
		defer closeStub()

		_, err := client.CreateDoc(context.Background(), "TITOLO TEST", createDocHTML, "")
		if err == nil {
			t.Fatal("a permanent 400 must fail the publish")
		}
		if got := createAttempts(); got != 1 {
			t.Fatalf("create attempts = %d; want 1 (4xx is terminal, never retried)", got)
		}
	})

	t.Run("all attempts transient returns the wrapped create error", func(t *testing.T) {
		t.Parallel()
		client, createAttempts, closeStub := docCreateStub(t, http.StatusServiceUnavailable, 0)
		defer closeStub()

		_, err := client.CreateDoc(context.Background(), "TITOLO TEST", createDocHTML, "")
		if err == nil {
			t.Fatal("a permanently failing 503 must surface an error")
		}
		if got := createAttempts(); got != 3 {
			t.Fatalf("create attempts = %d; want 3 (MaxAttempts exhausted)", got)
		}
		if !strings.Contains(err.Error(), "failed to create google doc") {
			t.Fatalf("error must keep the create context; got %v", err)
		}
	})
}
