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
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// TestCreateDocIdempotent_FreshPathFoldsMoveAndTagIntoOneRoundTrip drives the
// fresh-doc path of CreateDocIdempotent through a stub Google Docs + Drive API
// and pins the API-call budget: find (1) + create (1) + insert batchUpdate (1)
// + parents read (1) + ONE Files.Update that moves the doc AND sets BOTH app
// properties (1) = 5 sequential round trips. The previous shape issued the
// move (get+update) and each app property as separate calls (7 total); folding
// the move and the two tags into the single update is the publish-path
// optimization that keeps the caller-facing contract identical.
func TestCreateDocIdempotent_FreshPathFoldsMoveAndTagIntoOneRoundTrip(t *testing.T) {
	t.Parallel()

	var (
		mu        sync.Mutex
		paths     []string
		updateBod string
		updateQ   string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case r.URL.Path == "/drive/v3/files" && r.Method == http.MethodGet:
			// findDocByIdempotencyKey: no existing doc.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files": []}`))
		case r.URL.Path == "/v1/documents" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"documentId": "doc-fresh-1", "title": "T"}`))
		case strings.HasSuffix(r.URL.Path, ":batchUpdate") && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			updateBod = string(body)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/drive/v3/files/doc-fresh-1" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": "doc-fresh-1", "parents": ["root"]}`))
		case r.URL.Path == "/drive/v3/files/doc-fresh-1" && r.Method == http.MethodPatch:
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			updateBod = string(body)
			updateQ = r.URL.RawQuery
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": "doc-fresh-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	docsService, err := docs.NewService(ctx, option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("docs.NewService: %v", err)
	}
	// The Drive v3 client treats the endpoint as its base path and appends
	// resource names, so the endpoint must include the /drive/v3/ prefix;
	// the Docs client carries /v1/ in its discovery base path already.
	driveService, err := drive.NewService(ctx, option.WithEndpoint(srv.URL+"/drive/v3/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("drive.NewService: %v", err)
	}

	client := &DocClientImpl{docsService: docsService, driveService: driveService}
	doc, err := client.CreateDocIdempotent(ctx, "T", createDocHTML, "folder-1", "key-1", false)
	if err != nil {
		t.Fatalf("CreateDocIdempotent: %v", err)
	}
	if doc == nil || doc.ID != "doc-fresh-1" {
		t.Fatalf("unexpected doc: %+v", doc)
	}

	mu.Lock()
	defer mu.Unlock()

	// 5 sequential round trips — not 7 (the two app properties used to be
	// separate Files.Update calls after the move).
	if len(paths) != 5 {
		t.Fatalf("expected 5 API calls, got %d: %v", len(paths), paths)
	}
	for _, want := range []string{
		"GET /drive/v3/files",                        // idempotency find
		"POST /v1/documents",                         // create
		"POST /v1/documents/doc-fresh-1:batchUpdate", // insert content
		"GET /drive/v3/files/doc-fresh-1",            // parents read
		"PATCH /drive/v3/files/doc-fresh-1",          // move + BOTH tags
	} {
		if !containsStr(paths, want) {
			t.Fatalf("missing call %q in %v", want, paths)
		}
	}

	// The single Files.Update must carry the move query params AND both app
	// properties in the body.
	if !strings.Contains(updateQ, "addParents=folder-1") || !strings.Contains(updateQ, "removeParents=root") {
		t.Fatalf("update query missing move params: %q", updateQ)
	}
	var file drive.File
	if err := json.Unmarshal([]byte(updateBod), &file); err != nil {
		t.Fatalf("unmarshal update body: %v", err)
	}
	if file.AppProperties["pipelinegen_generation_id"] != "key-1" {
		t.Fatalf("update missing generation id property: %+v", file.AppProperties)
	}
	if file.AppProperties[docContentHashProperty] == "" {
		t.Fatalf("update missing content hash property: %+v", file.AppProperties)
	}
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
