// Package handlerutil_test verifies the nil-guard prologues used by handlers
// throughout internal/api and other packages.
package handlerutil_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/Marcuss-ops/PipelineGen/pkg/handlerutil"
)

func TestRequireService_NilServiceReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ok := handlerutil.RequireService(c, nil, "my svc")

	assert.False(t, ok, "RequireService should return false for nil svc")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body map[string]any
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, false, body["ok"])
	assert.Equal(t, "my svc not initialized", body["error"])
}

func TestRequireService_NonNilServicePasses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ok := handlerutil.RequireService(c, struct{}{}, "my svc")

	assert.True(t, ok, "RequireService should return true for non-nil svc")
	assert.Equal(t, http.StatusOK, rec.Code, "RequireService must not write a response on the happy path")
}

func TestRequireJobs_NilUsesJobSystemName(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ok := handlerutil.RequireJobs(c, nil)

	assert.False(t, ok)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body map[string]any
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "job system not initialized", body["error"])
}
