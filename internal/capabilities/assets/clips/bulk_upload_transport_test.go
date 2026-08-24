// Package clips (test) — bulk_upload_transport_test.go.
//
// Regression pin for the DRIFT-CLIPS-BULK-SPLIT-5 reconnect patch
// (July 2026). BulkUploadTransport MUST mount its route at
// POST /:source/clips/bulk-upload-youtube-clips so the canonical
// enqueue path returns 202 (async enqueue) or 503 (JobsSvc not
// configured in the test fixture) — NOT 404 (which would mean the
// sub-handler was silently disconnected).
//
// PR-13 (July 2026): the canonical client surface is the 6-field
// payload {local_folder, drive_folder_id, source, category, recursive,
// concurrency}. Dry-run, skip_*, subdir flag, file/skip patterns are GONE.
//
// The orchestrator-level wiring (Handler → bulkRegistrar →
// BulkUploadTransport.RegisterRoutes) is now covered by the
// composition-root E2E + archcheck gate; this transport-level test
// verifies the route is mounted and the handler non-404 forwards.
package clips

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestBulkUploadTransport_RouteRegistered(t *testing.T) {
	tmp := t.TempDir()
	bt := NewBulkUploadTransport(BulkTransportDeps{
		JobsSvc:          nil,
		MediaPath:        tmp,
		TempPath:         tmp,
		DataDir:          tmp,
		BulkUploadWorker: nil,
		Log:              zap.NewNop(),
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/clips")
	bt.RegisterRoutes(g, func(c *gin.Context) { c.Next() })

	body := fmt.Sprintf(
		`{"local_folder":%q,"drive_folder_id":"d-stale"}`,
		tmp,
	)
	req := httptest.NewRequest(
		"POST",
		"/api/clips/test-source/clips/bulk-upload-youtube-clips",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.NotEqual(t, http.StatusNotFound, w.Code,
		"BulkUploadTransport route not registered (P0 regression: sub-handler dropped from RegisterRoutes)")
	require.True(t,
		w.Code == http.StatusAccepted || w.Code == http.StatusServiceUnavailable,
		"want 202 (async enqueue) or 503 (JobsSvc not configured in test fixture), got %d body=%s",
		w.Code, w.Body.String())
}
