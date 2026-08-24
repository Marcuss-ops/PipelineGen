// Package drive — uploader_ops_test.go (F1.6, June 2026)
//
// Four test pins for the P0 #4 + #5 + #6 closure of
// (*Uploader).GetOrCreateFolder:
//
//	#1 — fail-closed on transient lookup error (no fallthrough to Create).
//	#2 — race-safety: singleflight deduplicates concurrent calls
//	     for the same (parentID, canonicalName); only ONE List
//	     and ONE Create observed.
//	#3 — canonical name: input "My_Folder  " produces a Files.List
//	     query "name = 'My_Folder'" AND a Files.Create body whose
//	     Name field is "My_Folder" (the SafeFolderName output).
//	     NOT "My_Folder  " or any variant — exact canonical match.
//	#7 — cross-process race: after a successful Create the second
//	     Files.List must return the OLDEST folder ID when multiple
//	     folders with the canonical name exist; NOT the freshly
//	     created ID we just produced.
//
// Untagged (default `go test`) — these mocks don't depend on any
// build-tagged seam. The drive SDK handles can be configured against
// an httptest.NewServer via option.WithEndpoint + WithoutAuthentication.
package drive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// urlRewritingTransport redirects every request's host:port to the mock
// server. The path stays as the SDK constructed it so we don't have to
// reason about Google's exact URL grammar (regular /drive/v3/files vs
// upload /upload/drive/v3/files variant — the Drive SDK flips between
// them based on whether .Media() is attached and the underlying version,
// and any future SDK bump could swing it on us). Tests below assert
// behavior through the mock — not by introspecting SDK URL quirks.
type urlRewritingTransport struct {
	mockHost   string
	mockScheme string
}

func (t *urlRewritingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = t.mockScheme
	req.URL.Host = t.mockHost
	return http.DefaultTransport.RoundTrip(req)
}

// ── Mock Drive SDK server ────────────────────────────────────────────────────

// mockDriveAPIServer wires up an httptest server that mimics the Google
// Drive v3 API surface used by GetOrCreateFolder (Files.List + Files.Create).
// Each call to either endpoint consumes the next canned response from a
// FIFO queue. Call counts and last-seen request bodies are exposed for
// test assertions.
type mockDriveAPIServer struct {
	*httptest.Server
	mu                  sync.Mutex
	listResponses       []listResponse // FIFO of canned Files.List responses
	createResponses     []createResp   // FIFO of canned Files.Create responses
	listBodyResponses   []rawResp      // FIFO of canned raw HTTP responses for List (used to inject errors)
	createBodyResponses []rawResp      // FIFO of canned raw HTTP responses for Create

	listCallCount   atomic.Int32
	createCallCount atomic.Int32
	lastListQuery   string
	lastCreateBody  string
}

type listResponse struct {
	files []listEntry
}
type listEntry struct {
	id          string
	name        string
	createdTime string
}

func (l listResponse) marshal() string {
	type fileX struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		CreatedTime string `json:"createdTime"`
	}
	type body struct {
		Files []fileX `json:"files"`
	}
	out := body{Files: make([]fileX, 0, len(l.files))}
	for _, f := range l.files {
		out.Files = append(out.Files, fileX{ID: f.id, Name: f.name, CreatedTime: f.createdTime})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

type createResp struct{ id string }

func (c createResp) marshal() string {
	b, _ := json.Marshal(map[string]string{"id": c.id})
	return string(b)
}

// rawResp injects exact HTTP status + body for the next call (used for
// HTTP 503 transient-error simulation in test pin #1).
type rawResp struct {
	status int
	body   string
}

func newMockDriveAPIServer() *mockDriveAPIServer {
	srv := &mockDriveAPIServer{}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Drive SDK routes Create through the /upload/drive/v3/files
		// endpoint (even when no .Media is attached). Match by suffix
		// to accept BOTH /drive/v3/files (List, Get) AND
		// /upload/drive/v3/files (Create). Other paths 404.
		if !strings.Contains(r.URL.Path, "/drive/v3/files") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		srv.mu.Lock()
		defer srv.mu.Unlock()

		// Route by HTTP method primarily (path is wildcard above);
		// this lets a single mock handle both /drive/v3/files (List)
		// and /upload/drive/v3/files (Create) without per-path cases.
		switch r.Method {
		case http.MethodGet:
			srv.listCallCount.Add(1)
			srv.lastListQuery = r.URL.Query().Get("q")
			if len(srv.listBodyResponses) > 0 {
				rr := srv.listBodyResponses[0]
				srv.listBodyResponses = srv.listBodyResponses[1:]
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(rr.status)
				_, _ = w.Write([]byte(rr.body))
				return
			}
			if len(srv.listResponses) > 0 {
				resp := srv.listResponses[0]
				srv.listResponses = srv.listResponses[1:]
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(resp.marshal()))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":[]}`))
			return
		case http.MethodPost:
			srv.createCallCount.Add(1)
			srv.lastCreateBody = string(body)
			if len(srv.createBodyResponses) > 0 {
				rr := srv.createBodyResponses[0]
				srv.createBodyResponses = srv.createBodyResponses[1:]
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(rr.status)
				_, _ = w.Write([]byte(rr.body))
				return
			}
			if len(srv.createResponses) > 0 {
				resp := srv.createResponses[0]
				srv.createResponses = srv.createResponses[1:]
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(resp.marshal()))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"default-id"}`))
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}))
	return srv
}

// attachMockService builds a *driveapi.Service whose HTTP transport
// rewrites every request's host to the mock server. Path stays as the
// SDK constructed it — we route by HTTP method on the mock side, which
// is robust against Drive SDK URL-grammar changes (e.g. /drive/v3/files
// vs /upload/drive/v3/files variant).
func (s *mockDriveAPIServer) attachMockService(t *testing.T) *driveapi.Service {
	t.Helper()
	mockURL, err := url.Parse(s.Server.URL)
	if err != nil {
		t.Fatalf("mockDriveAPIServer.attachMockService: parse mock URL %q: %v", s.Server.URL, err)
	}
	httpClient := &http.Client{
		Transport: &urlRewritingTransport{
			mockHost:   mockURL.Host,
			mockScheme: mockURL.Scheme,
		},
	}
	srv, err := driveapi.NewService(context.Background(),
		option.WithHTTPClient(httpClient),
		option.WithoutAuthentication(),
		option.WithScopes(driveapi.DriveScope),
	)
	if err != nil {
		t.Fatalf("mockDriveAPIServer.attachMockService: driveapi.NewService: %v", err)
	}
	return srv
}

// ── Test pin #1: Fail-closed on transient lookup error ──────────────────────

// TestUploader_GetOrCreateFolder_FailClosedOnTransientLookup pins the
// F1.6 P0 #4 contract: when Files.List returns a transient error,
// GetOrCreateFolder MUST propagate the error WITHOUT falling through
// to Files.Create. Pre-F1.6 the soft-error fallthrough produced
// duplicate folders when two concurrent EnsureFolderPath calls' Create
// branches both succeeded against a transient-List backdrop.
func TestUploader_GetOrCreateFolder_FailClosedOnTransientLookup(t *testing.T) {
	srv := newMockDriveAPIServer()
	defer srv.Server.Close()

	// Simulate a transient Drive API error: HTTP 503 with the standard
	// Google error envelope. The SDK wraps this in a *googleapi.Error
	// whose .Error() contains "503" — matches isRetryableDriveErr so
	// the retry seam activates. After 3 attempts, the err propagates.
	srv.listBodyResponses = []rawResp{
		{status: 503, body: `{"error":{"code":503,"message":"backendError","status":"UNAVAILABLE"}}`},
		{status: 503, body: `{"error":{"code":503,"message":"backendError","status":"UNAVAILABLE"}}`},
		{status: 503, body: `{"error":{"code":503,"message":"backendError","status":"UNAVAILABLE"}}`},
		// Extra responses in case the retry-table is re-tuned; idempotent.
		{status: 503, body: `{"error":{"code":503,"message":"backendError","status":"UNAVAILABLE"}}`},
		{status: 503, body: `{"error":{"code":503,"message":"backendError","status":"UNAVAILABLE"}}`},
	}
	srv.createResponses = []createResp{{id: "should-not-be-used"}}

	uploader := &Uploader{
		Service: srv.attachMockService(t),
		Log:     zap.NewNop(),
	}

	_, err := uploader.GetOrCreateFolder(context.Background(), "test-folder", "parent-id")
	if err == nil {
		t.Fatal("expected error from transient lookup, got nil — F1.6 P0 #4 fail-closed contract violated")
	}

	// Pass criterion A: error message references the lookup branch
	// (seam intercepted correctly). Both the wrapper text
	// "lookup folder ... under ..." and the annotation
	// "no fallthrough to Create" satisfy this.
	if !strings.Contains(err.Error(), "lookup") {
		t.Fatalf("expected err msg to reference 'lookup' (F1.6 P0 #4 fail-closed), got: %v", err)
	}

	// Pass criterion B: error mentions the F1.6 annotation so audit-trail
	// grep can identify this contract quickly.
	if !strings.Contains(err.Error(), "no fallthrough to Create") {
		t.Fatalf("expected err msg to contain 'no fallthrough to Create' annotation, got: %v", err)
	}

	// Pass criterion C: Create MUST NOT have been called (the legacy
	// soft-error-fallthrough surface that produced duplicate folders).
	if got := srv.createCallCount.Load(); got != 0 {
		t.Fatalf("expected 0 Files.Create calls (fail-closed never falls through), got: %d", got)
	}
}

// ── Test pin #2: Race-safety keyed lock via singleflight ─────────────────────

// TestUploader_GetOrCreateFolder_RaceSafetySingleflight pins the F1.6
// P0 #5 in-process race-safety contract: 100 concurrent calls to
// GetOrCreateFolder for the same (parent, name) MUST be deduplicated
// via singleflight.Group; only ONE Files.List and ONE Files.Create
// observed through the mock — concurrent callers receive the same
// finished result instead of each racing through Create.
func TestUploader_GetOrCreateFolder_RaceSafetySingleflight(t *testing.T) {
	srv := newMockDriveAPIServer()
	defer srv.Server.Close()

	// Under correct singleflight + Stage 3 cross-process second-lookup,
	// the canonical algorithm consumes exactly TWO list responses
	// (Stage 1 pre-create empty + Stage 3 post-create empty) and ONE
	// create response. The 100 pre-seeded responses below are
	// belt-and-braces for the failure surface (singleflight failing to
	// coalesce → 2*N calls would otherwise fall through to the default
	// empty `{"files":[]}` body — which is fine but masks whether the
	// mock ran out). Trimmed from 100 to 2 in the F1.6 final-fix pass
	// per reviewer feedback — under correct singleflight we consume 2.
	for i := 0; i < 2; i++ {
		srv.listResponses = append(srv.listResponses, listResponse{files: nil})
	}
	srv.createResponses = []createResp{
		{id: "singleflight-coalesced-id"},
		// One extra in case a future SDK bump ever widens the canonical
		// algorithm; assertion below stays at ==1 so the spare is unused.
		{id: "spare-1"},
	}

	uploader := &Uploader{
		Service: srv.attachMockService(t),
		Log:     zap.NewNop(),
	}

	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	startGate := make(chan struct{})
	results := make([]string, N)
	errs := make([]error, N)

	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-startGate // release the gate simultaneously for max contention
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			r, e := uploader.GetOrCreateFolder(ctx, "test-folder", "parent-id")
			results[i] = r
			errs[i] = e
		}()
	}
	close(startGate) // start all N goroutines at once
	wg.Wait()

	// Pass criterion A: every call succeeded (no concurrent error).
	for i, e := range errs {
		if e != nil {
			t.Fatalf("goroutine #%d failed: %v", i, e)
		}
	}

	// Pass criterion B: every call observed the SAME folder ID
	// (singleflight coalesces results — no goroutine saw a different id).
	first := results[0]
	for i, r := range results {
		if r != first {
			t.Fatalf("goroutine #%d observed a different folder ID (%q) than goroutine #0 (%q) — singleflight did not coalesce",
				i, r, first)
		}
	}

	// Pass criterion C: exactly TWO Files.List observed (Singleflight
	// dedup + F1.6 P0 #5 Stage 3 cross-process second-lookup). One List
	// for the canonical Stage 1 lookup, and one List for the Stage 3
	// second-lookup after the fresh Create succeeded. The singleflight
	// guarantee means we see the canonical 2 calls total, not 2*N.
	if got := srv.listCallCount.Load(); got != 2 {
		t.Fatalf("expected exactly 2 Files.List calls (Stage 1 + Stage 3 cross-process second-lookup), got: %d", got)
	}

	// Pass criterion D: exactly ONE Files.Create observed (keyed lock).
	if got := srv.createCallCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 Files.Create call (singleflight keyed by parent+name), got: %d", got)
	}
}

// ── Test pin #3: Canonical name (SafeFolderName) ─────────────────────────────

// TestUploader_GetOrCreateFolder_CanonicalNameExactMatch pins the F1.6
// P0 #6 contract: input is sanitised via pkg/pathutil.SafeFolderName
// before being passed to BOTH Files.List and Files.Create. The legacy
// fuzzy branch (fileutil.CleanFolderName(file.Name) == CleanFolderName(name))
// is REMOVED — exact-match via the canonical form only.
//
// The mock captures the underlying q- parameter and the POST body. The
// test asserts both contain the SafeFolderName output verbatim; the
// surrounding whitespace + underscore mixture in the input must be
// eliminated by SafeFolderName.
func TestUploader_GetOrCreateFolder_CanonicalNameExactMatch(t *testing.T) {
	srv := newMockDriveAPIServer()
	defer srv.Server.Close()

	// First call returns empty (folder does not exist) → triggers Create.
	srv.listResponses = []listResponse{{files: nil}}
	srv.createResponses = []createResp{{id: "canonical-id"}}

	uploader := &Uploader{
		Service: srv.attachMockService(t),
		Log:     zap.NewNop(),
	}

	// Input has underscore + trailing space — SafeFolderName keeps the
	// underscore (it is letter/digit/'-'/'_'/' ' safe) and trims the
	// trailing space. The expected canonical form is "My_Folder".
	got, err := uploader.GetOrCreateFolder(context.Background(), "My_Folder ", "parent-abc")
	if err != nil {
		t.Fatalf("GetOrCreateFolder failed: %v", err)
	}
	if got != "canonical-id" {
		t.Fatalf("expected returned folder ID to be canonical-id, got: %q", got)
	}

	// Pass criterion A: Files.List query contains the canonical name
	// (not the raw input). SafeFolderName("My_Folder ") = "My_Folder".
	if !strings.Contains(srv.lastListQuery, "name = 'My_Folder'") {
		t.Fatalf("expected Files.List query to contain canonical name `name = 'My_Folder'`, got: %q", srv.lastListQuery)
	}

	// Pass criterion B: Files.List query must NOT contain the raw
	// input (this is the key invariant — the legacy fuzzy branch
	// would have matched against the raw "My_Folder " via
	// fileutil.CleanFolderName equivalence).
	if strings.Contains(srv.lastListQuery, "My_Folder ") {
		t.Fatalf("Files.List query must NOT contain the trailing-space raw input (legacy fuzzy fallback), got: %q", srv.lastListQuery)
	}

	// Pass criterion C: Files.Create body uses the canonical name
	// verbatim in the Name field — not the raw input.
	var createBody struct {
		Name     string   `json:"name"`
		MimeType string   `json:"mimeType"`
		Parents  []string `json:"parents"`
	}
	if err := json.Unmarshal([]byte(srv.lastCreateBody), &createBody); err != nil {
		t.Fatalf("could not decode Files.Create body: %v (body=%q)", err, srv.lastCreateBody)
	}
	if createBody.Name != "My_Folder" {
		t.Fatalf("expected Files.Create.name to be canonical 'My_Folder', got: %q", createBody.Name)
	}
	if createBody.MimeType != "application/vnd.google-apps.folder" {
		t.Fatalf("expected Files.Create.mimeType to be folder mime, got: %q", createBody.MimeType)
	}
	if len(createBody.Parents) != 1 || createBody.Parents[0] != "parent-abc" {
		t.Fatalf("expected Files.Create.parents to be [parent-abc], got: %v", createBody.Parents)
	}
}

// ── Test pin #7: Cross-process race second-lookup returns oldest ─────────────

// TestUploader_GetOrCreateFolder_CrossProcessRaceReturnsOldest pins the
// F1.6 P0 #5 cross-process race mitigation contract: after a successful
// Create, the second Files.List (ordered by createdTime asc) MUST
// surface the OLDEST folder ID when multiple folders with the canonical
// name exist. Pre-F1.6 the function returned the freshly-created ID
// blindly, leaving duplicates stranded on Drive.
func TestUploader_GetOrCreateFolder_CrossProcessRaceReturnsOldest(t *testing.T) {
	srv := newMockDriveAPIServer()
	defer srv.Server.Close()

	// Mock sequence:
	//  1. Files.List (Stage 1, exact-match pre-create): empty — folder
	//     does not yet exist.
	//  2. Files.Create (Stage 2): returns ID "newly-created-id".
	//  3. Files.List (Stage 3, post-create cross-process race check):
	//     returns TWO folders with the canonical name. The mock orders
	//     them by the requested orderBy=createdTime asc — so the first
	//     is the oldest, the second is the freshly-created one.
	//     The contract: GetOrCreateFolder must return "older-id" (the
	//     index-0 entry), NOT "newly-created-id".
	srv.listResponses = []listResponse{
		{files: nil},
		{files: []listEntry{
			{id: "older-id", name: "test-folder", createdTime: "2024-01-01T00:00:00.000Z"},
			{id: "newly-created-id", name: "test-folder", createdTime: "2026-06-30T12:00:00.000Z"},
		}},
	}
	srv.createResponses = []createResp{{id: "newly-created-id"}}

	uploader := &Uploader{
		Service: srv.attachMockService(t),
		Log:     zap.NewNop(),
	}

	got, err := uploader.GetOrCreateFolder(context.Background(), "test-folder", "parent-id")
	if err != nil {
		t.Fatalf("GetOrCreateFolder failed: %v", err)
	}

	// Pass criterion A: returned ID is the OLDEST folder (index 0 of
	// the createdTime-asc-ordered List), NOT the freshly-created ID.
	if got != "older-id" {
		t.Fatalf("expected returned folder ID to be oldest match 'older-id' (F1.6 P0 #5 cross-process race), got: %q", got)
	}

	// Pass criterion B: List was called twice (Stage 1 + Stage 3).
	if got := srv.listCallCount.Load(); got != 2 {
		t.Fatalf("expected exactly 2 Files.List calls (Stage 1 + Stage 3 cross-process), got: %d", got)
	}

	// Pass criterion C: Create was called exactly once (Stage 2).
	if got := srv.createCallCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 Files.Create call (Stage 2), got: %d", got)
	}
}
