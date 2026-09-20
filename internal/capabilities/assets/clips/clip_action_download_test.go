package clips

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ── stubs ─────────────────────────────────────────────────────────────

type stubClipReader struct {
	clip *asset.Asset
	err  error
}

func (s stubClipReader) Get(context.Context, string) (*asset.Asset, error) {
	return s.clip, s.err
}

type stubJobStatus struct {
	status string
	retry  int
	found  bool
}

func (s stubJobStatus) LatestJobStatus(context.Context, string) (string, int, bool) {
	return s.status, s.retry, s.found
}

func downloadHandler(t *testing.T, reader stubClipReader, jobs AssetJobStatusLookup) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewActionHandler(ActionDeps{
		DownloadUC: appclips.NewDownloadUseCase(reader, nil),
		JobStatus:  jobs,
		Log:        zap.NewNop(),
	})
	r := gin.New()
	r.GET("/clips/:source/clips/:id/download", h.DownloadClip)
	return r
}

func doDownload(t *testing.T, r *gin.Engine, id string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/clips/youtube/clips/"+id+"/download", nil)
	r.ServeHTTP(w, req)
	return w
}

// ── tests ─────────────────────────────────────────────────────────────

// TestDownloadConflictWhenAssetExistsWithoutArtifact pins the core fix: an
// asset row that exists but whose video artifact is not materialized yet
// answers 409 Conflict, not 404. A 404 here claimed the asset did not exist.
func TestDownloadConflictWhenAssetExistsWithoutArtifact(t *testing.T) {
	r := downloadHandler(t, stubClipReader{clip: &asset.Asset{}}, nil)
	w := doDownload(t, r, "yt_pending")

	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409", w.Code)
	}
	var body struct {
		Status     string `json:"status"`
		RetryCount int    `json:"retry_count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (%s)", err, w.Body.String())
	}
	if body.Status != "RUNNING" {
		t.Errorf("status=%q, want RUNNING", body.Status)
	}
	if body.RetryCount < 1 {
		t.Errorf("retry_count=%d, want >= 1", body.RetryCount)
	}
}

// TestDownloadConflictReportsWiredJobStatus proves a wired lookup supplies the
// real status and retry count.
func TestDownloadConflictReportsWiredJobStatus(t *testing.T) {
	r := downloadHandler(t, stubClipReader{clip: &asset.Asset{}},
		stubJobStatus{status: "RETRYING", retry: 3, found: true})
	w := doDownload(t, r, "yt_retrying")

	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409", w.Code)
	}
	var body struct {
		Status     string `json:"status"`
		RetryCount int    `json:"retry_count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "RETRYING" || body.RetryCount != 3 {
		t.Fatalf("body=%+v, want RETRYING/3", body)
	}
}

// TestDownloadConflictWhenAssetRowNotCommittedYet proves the not-found path is
// also upgraded to 409 while the owning job is still known to be running.
func TestDownloadConflictWhenAssetRowNotCommittedYet(t *testing.T) {
	r := downloadHandler(t, stubClipReader{err: errors.New("clip not found: yt_new")},
		stubJobStatus{status: "RUNNING", retry: 1, found: true})
	w := doDownload(t, r, "yt_new")

	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409", w.Code)
	}
}

// TestDownloadNotFoundWithoutJobRecord preserves the honest 404: no asset row
// and no job record is a genuine miss, not a timing problem.
func TestDownloadNotFoundWithoutJobRecord(t *testing.T) {
	r := downloadHandler(t, stubClipReader{err: errors.New("clip not found: yt_gone")}, nil)
	w := doDownload(t, r, "yt_gone")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", w.Code)
	}
}
