package system

import (
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
