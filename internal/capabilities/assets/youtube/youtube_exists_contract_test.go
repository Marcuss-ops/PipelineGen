package youtube

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// stubExistencePort is the T1.3 dedup probe double: records calls and
// returns a canned clip id / error.
type stubExistencePort struct {
	clipID  string
	err     error
	calls   int
	lastURL string
}

func (s *stubExistencePort) FindExistingClipID(_ context.Context, videoURL string) (string, error) {
	s.calls++
	s.lastURL = videoURL
	return s.clipID, s.err
}

// invokeGetClipExists drives the handler method with the given raw query.
func invokeGetClipExists(t *testing.T, h *YouTubeClipHandler, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, target, nil)
	h.GetClipExists(ctx)
	return rec
}

// TestGetClipExists_RejectsMissingURL pins the 400 arm: no url param →
// BadRequest, and the port is NEVER consulted (a probe call without a
// target would be a wasted SSOT query).
func TestGetClipExists_RejectsMissingURL(t *testing.T) {
	port := &stubExistencePort{}
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)
	h.existence = port

	rec := invokeGetClipExists(t, h, "/api/clips/exists")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "url parameter is required")
	assert.Zero(t, port.calls, "the existence port must not be queried without a url")
}

// TestGetClipExists_UnwiredPortFailsClosed pins the 503 arm: a missing
// port (media PostgreSQL SSOT disabled) answers Service Unavailable —
// NEVER {exists:false}, which would tell the agent the candidate is new
// and re-trigger extraction it cannot dedupe.
func TestGetClipExists_UnwiredPortFailsClosed(t *testing.T) {
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)

	rec := invokeGetClipExists(t, h, "/api/clips/exists?url=https%3A%2F%2Fyoutu.be%2Fabc12345678")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "not wired")
	assert.NotContains(t, rec.Body.String(), `"exists":true`)
}

// TestGetClipExists_HitAndMiss pins the two success arms: a registered
// candidate returns {exists:true, clip_id} with the URL forwarded
// verbatim; an unknown one returns {exists:false} with empty clip_id.
func TestGetClipExists_HitAndMiss(t *testing.T) {
	t.Run("hit", func(t *testing.T) {
		port := &stubExistencePort{clipID: "yt_506AyzC7d-k"}
		h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)
		h.existence = port

		const url = "https://www.youtube.com/watch?v=506AyzC7d-k"
		rec := invokeGetClipExists(t, h, "/api/clips/exists?url=https%3A%2F%2Fwww.youtube.com%2Fwatch%3Fv%3D506AyzC7d-k")

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"exists":true`)
		assert.Contains(t, rec.Body.String(), `"clip_id":"yt_506AyzC7d-k"`)
		assert.Equal(t, 1, port.calls)
		assert.Equal(t, url, port.lastURL, "the URL must reach the port verbatim (no re-encoding)")
	})

	t.Run("miss", func(t *testing.T) {
		port := &stubExistencePort{clipID: ""}
		h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)
		h.existence = port

		rec := invokeGetClipExists(t, h, "/api/clips/exists?url=https%3A%2F%2Fyoutu.be%2Fabc12345678")

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"exists":false`)
	})
}

// TestGetClipExists_PortErrorSurfaces500 pins fail-closed error
// propagation: a broken SSOT probe is an error, not a silent miss.
func TestGetClipExists_PortErrorSurfaces500(t *testing.T) {
	port := &stubExistencePort{err: assert.AnError}
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)
	h.existence = port

	rec := invokeGetClipExists(t, h, "/api/clips/exists?url=https%3A%2F%2Fyoutu.be%2Fabc12345678")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), `"exists":false`,
		"a probe error must never be coerced into a miss (that re-triggers extraction)")
}
