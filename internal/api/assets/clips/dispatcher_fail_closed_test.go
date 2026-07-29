package clips

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type recordingDispatcher struct {
	calls     atomic.Int32
	lastClip  *asset.Asset
	lastHash  string
	injectErr error
}

func (r *recordingDispatcher) EnqueueAndIndex(_ context.Context, clip *asset.Asset, hash string) error {
	r.calls.Add(1)
	r.lastClip = clip
	r.lastHash = hash
	return r.injectErr
}

var _ appclips.ClipIndexDispatcherPort = (*recordingDispatcher)(nil)

func routerFor(t *testing.T, h *Handler) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/:source")
	g.POST("/clips", h.CreateClip)
	return r
}

func newIngestTestHandler(dispatcher appclips.ClipIndexDispatcherPort) *Handler {
	return NewHandler(Deps{
		Ingest: IngestDeps{
			Dispatcher: dispatcher,
			Log:        zap.NewNop(),
		},
	}, nil)
}

func TestPR6_CreateClip_NilDispatcher_503(t *testing.T) {
	h := newIngestTestHandler(nil)
	r := routerFor(t, h)

	body := `{"id":"pr6-nil-dispatch-001","name":"t"}`
	req := httptest.NewRequest("POST", "/api/youtube/clips", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AssetMutationDispatcher not wired")
}

func TestPR6_CreateClip_DispatcherPresent_OneCall(t *testing.T) {
	disp := &recordingDispatcher{}
	h := newIngestTestHandler(disp)
	r := routerFor(t, h)

	body := `{"id":"pr6-happy-001","name":"happy"}`
	req := httptest.NewRequest("POST", "/api/youtube/clips", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int32(1), disp.calls.Load())
	if disp.lastClip != nil {
		assert.Equal(t, "pr6-happy-001", disp.lastClip.ID)
	}
}

func TestPR6_CreateClip_DispatcherError_Propagated(t *testing.T) {
	cause := errors.New("simulated dispatcher tx failure")
	disp := &recordingDispatcher{injectErr: cause}
	h := newIngestTestHandler(disp)
	r := routerFor(t, h)

	body := `{"id":"pr6-err-001","name":"err"}`
	req := httptest.NewRequest("POST", "/api/youtube/clips", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errString, _ := resp["error"].(string)
	assert.Contains(t, errString, "dispatcher.EnqueueAndIndex")
	assert.Equal(t, int32(1), disp.calls.Load())
}
