package system

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestErrReconcilerNotWired_IsTypedSentinel(t *testing.T) {
	require.True(t, errors.Is(ErrReconcilerNotWired, ErrReconcilerNotWired))
	require.False(t, errors.Is(ErrReconcilerNotWired, errors.New("other")))
}

func TestDriveReconcileRoutesFailClosedWhenReconcilerMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewDriveHandler(nil, nil)
	router := gin.New()
	h.RegisterRoutes(router.Group("/api/drive"))

	for _, route := range []string{"/api/drive/reconcile", "/api/drive/cleanup"} {
		t.Run(route, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()

			router.ServeHTTP(res, req)

			require.Equal(t, http.StatusServiceUnavailable, res.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
			require.Equal(t, false, body["ok"])
			require.Equal(t, ErrReconcilerNotWired.Error(), body["error"])
		})
	}
}

// ── POST /api/drive/folders (folder creation) ───────────────────────────────

// recordingDriveOps is the DriveAdminOps double for the folder-creation
// contract. Only GetOrCreateFolder carries behaviour: it records every call
// (name, parentID) in order and mints a deterministic id, so a test can prove
// which parent the handler actually threaded down and that the parent it
// received is the normalised folder id, not a raw Drive URL.
type recordingDriveOps struct {
	created map[string]string // "name|parent" → id
	calls   [][2]string       // {name, parentID} in call order
	failOn  string            // folder name whose creation fails
}

func newRecordingDriveOps(failOn string) *recordingDriveOps {
	return &recordingDriveOps{created: map[string]string{}, failOn: failOn}
}

func (r *recordingDriveOps) GetOrCreateFolder(_ context.Context, name, parentID string) (string, error) {
	r.calls = append(r.calls, [2]string{name, parentID})
	if r.failOn != "" && name == r.failOn {
		return "", errors.New("drive write refused")
	}
	key := name + "|" + parentID
	if id, ok := r.created[key]; ok {
		return id, nil
	}
	id := "id-" + name
	r.created[key] = id
	return id, nil
}

func (r *recordingDriveOps) MoveFile(context.Context, string, string, string) error { return nil }
func (r *recordingDriveOps) ListFiles(context.Context, string) ([]DriveFileInfoDTO, error) {
	return nil, nil
}
func (r *recordingDriveOps) RenameFile(context.Context, string, string) error { return nil }
func (r *recordingDriveOps) ResolveFileInfo(context.Context, string) (ResolveByIDsItem, error) {
	return ResolveByIDsItem{}, nil
}

// performDriveRequest drives one request through the real DriveHandler router.
func performDriveRequest(t *testing.T, ops DriveAdminOps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewDriveHandler(nil, ops)
	router := gin.New()
	h.RegisterRoutes(router.Group("/api/drive"))

	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}

// TestDriveCreateFolders_GetOrCreateUnderNormalizedParent pins the contract
// promise the API docs make ("Create Drive folders under a parent"): every
// name in the request becomes one get-or-create call under the SAME parent,
// a Drive URL parent is normalised to its bare folder id before it reaches the
// port, and blank names are skipped instead of creating a nameless folder.
func TestDriveCreateFolders_GetOrCreateUnderNormalizedParent(t *testing.T) {
	ops := newRecordingDriveOps("")
	res := performDriveRequest(t, ops, http.MethodPost, "/api/drive/folders",
		`{"parent_id":"https://drive.google.com/drive/folders/1wES95cH_RVYxl5I3kxnppbv-AlymCRqG","folders":["boxe","TeamCoco","  "]}`)

	require.Equal(t, http.StatusOK, res.Code)
	var body struct {
		OK           bool              `json:"ok"`
		Created      map[string]string `json:"created"`
		CreatedCount int               `json:"created_count"`
		Errors       []string          `json:"errors"`
		ErrorCount   int               `json:"error_count"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	require.True(t, body.OK)
	require.Equal(t, 2, body.CreatedCount)
	require.Equal(t, 0, body.ErrorCount)
	require.Equal(t, "id-boxe", body.Created["boxe"])
	require.Equal(t, "id-TeamCoco", body.Created["TeamCoco"])

	const wantParent = "1wES95cH_RVYxl5I3kxnppbv-AlymCRqG"
	require.Equal(t, [][2]string{{"boxe", wantParent}, {"TeamCoco", wantParent}}, ops.calls)
}

// TestDriveCreateFolders_TrimsWhitespaceOnlyNames pins that a whitespace-only
// entry never reaches Drive: it used to become a folder literally named "   ".
func TestDriveCreateFolders_TrimsWhitespaceOnlyNames(t *testing.T) {
	ops := newRecordingDriveOps("")
	res := performDriveRequest(t, ops, http.MethodPost, "/api/drive/folders",
		`{"parent_id":"parent-1","folders":[" boxe ","   ",""]}`)

	require.Equal(t, http.StatusOK, res.Code)
	var body struct {
		Created      map[string]string `json:"created"`
		CreatedCount int               `json:"created_count"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	require.Equal(t, 1, body.CreatedCount)
	require.Equal(t, "id-boxe", body.Created["boxe"])
	require.Equal(t, [][2]string{{"boxe", "parent-1"}}, ops.calls)
}

// TestDriveCreateFolders_IsIdempotentPerName pins that the handler is a
// get-or-create, not a create: re-submitting the same name under the same
// parent yields the same folder id and never mints a second folder.
func TestDriveCreateFolders_IsIdempotentPerName(t *testing.T) {
	ops := newRecordingDriveOps("")
	body := `{"parent_id":"parent-1","folders":["boxe"]}`

	first := performDriveRequest(t, ops, http.MethodPost, "/api/drive/folders", body)
	second := performDriveRequest(t, ops, http.MethodPost, "/api/drive/folders", body)

	for i, res := range []*httptest.ResponseRecorder{first, second} {
		require.Equal(t, http.StatusOK, res.Code, "call %d", i+1)
	}
	require.Len(t, ops.created, 1, "a repeated request must not create a second folder")
	require.Equal(t, 2, len(ops.calls))
}

// TestDriveCreateFolders_ReportsPartialFailureWithoutHidingIt pins the
// partial-failure contract: one refused folder still returns the folders that
// succeeded (so the caller can retry only the missing ones) and names the
// failure in `errors`.
func TestDriveCreateFolders_ReportsPartialFailureWithoutHidingIt(t *testing.T) {
	ops := newRecordingDriveOps("boom")
	res := performDriveRequest(t, ops, http.MethodPost, "/api/drive/folders",
		`{"parent_id":"parent-1","folders":["ok","boom"]}`)

	require.Equal(t, http.StatusOK, res.Code)
	var body struct {
		CreatedCount int      `json:"created_count"`
		ErrorCount   int      `json:"error_count"`
		Errors       []string `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	require.Equal(t, 1, body.CreatedCount)
	require.Equal(t, 1, body.ErrorCount)
	require.Len(t, body.Errors, 1)
	require.Contains(t, body.Errors[0], "boom")
	require.Contains(t, body.Errors[0], "drive write refused")
}

// TestDriveCreateFolders_RejectsMalformedRequests pins the 400s: a request that
// carries no parent (there is no implicit "My Drive" root here), one whose
// parent is blank, and one that carries no folder name to create.
func TestDriveCreateFolders_RejectsMalformedRequests(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		// A missing key trips the struct's `binding:"required"` tag, so the
		// rejection is the binding error; the explicit message below is the
		// second line of defence for a present-but-blank parent.
		{"no parent", `{"folders":["boxe"]}`, "invalid request"},
		{"blank parent", `{"parent_id":"   ","folders":["boxe"]}`, "parent_id is required"},
		{"no folders key", `{"parent_id":"parent-1"}`, "invalid request"},
		{"empty folders list", `{"parent_id":"parent-1","folders":[]}`, "folders list is empty"},
		// Every entry is blank: nothing would be created, so answering
		// ok:true with created_count 0 would read as "already existed".
		{"only blank folder names", `{"parent_id":"parent-1","folders":["  ",""]}`, "folders list is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := performDriveRequest(t, newRecordingDriveOps(""), http.MethodPost, "/api/drive/folders", tc.body)
			require.Equal(t, http.StatusBadRequest, res.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
			require.Equal(t, false, body["ok"])
			require.Contains(t, body["error"], tc.wantErr)
		})
	}
}

// TestDriveCreateFolders_FailsClosedWithoutUploader pins godlike/07: with no
// Drive admin port wired the route answers 500 instead of a green no-op that
// would read as "folders already existed".
func TestDriveCreateFolders_FailsClosedWithoutUploader(t *testing.T) {
	res := performDriveRequest(t, nil, http.MethodPost, "/api/drive/folders",
		`{"parent_id":"parent-1","folders":["boxe"]}`)

	require.Equal(t, http.StatusInternalServerError, res.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	require.Equal(t, false, body["ok"])
	require.Equal(t, "drive uploader not configured", body["error"])
}
