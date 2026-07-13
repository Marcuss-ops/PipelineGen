// Package clips (test) — bulk_upload_transport_test.go.
//
// Regression pin for the DRIFT-CLIPS-BULK-SPLIT-5 reconnect patch
// (July 2026). Handler.RegisterRoutes MUST mount the
// BulkUploadTransport sub-handler so that
// POST /:source/clips/bulk-upload-youtube-clips returns 200 (dry-run)
// or 202 (async enqueue) — NOT 404 (which would mean the sub-handler
// was silently disconnected from the orchestrator by some future
// refactor of the Split-1/2/3/4/5 chain).
//
// The dry-run path is exercised so this regression test stays
// runtime-self-contained: it does NOT need a JobsSvc stub because
// the handler short-circuits to apiutil.OK(200) BEFORE touching
// bt.jobsSvc. The 1-line JobRegistrar equivalent (the 8-method
// job.Service interface satisfaction) is intentionally NOT
// included — a regression in the dispatch chain is a SEPARATE
// concern covered by the live /readyz canary + the worker-side
// handler-registration diagnostic.
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
	tmp := t.TempDir() // ScanLocalClips mandates an existing directory
	h := NewHandler(Deps{Log: zap.NewNop()}, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/clips") // mirrors clips/module.go production mount
	h.RegisterRoutes(g)        // installs :source/clips/bulk-upload-youtube-clips

	body := fmt.Sprintf(
		`{"local_folder":%q,"drive_folder_id":"d-stale","dry_run":true,"limit":0}`,
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
		"BulkUploadTransport disconnected from orchestrator (P0 regression: sub-handler dropped from Handler.RegisterRoutes)")
	require.True(t, w.Code == http.StatusOK || w.Code == http.StatusAccepted,
		"want 200 (dry-run) or 202 (async enqueue), got %d body=%s",
		w.Code, w.Body.String())
}
