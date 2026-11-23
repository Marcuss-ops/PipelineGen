package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestReadyPublishesGPUAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHealthHandler(nil, nil)
	h.SetGPUAdmission(func() GPUAdmissionStatus {
		return GPUAdmissionStatus{Lanes: 2, Slots: 3, ParityOK: false}
	})
	r := gin.New()
	r.GET("/ready", h.Ready)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ready", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	raw, ok := payload["gpu_admission"]
	require.True(t, ok, "ready must publish gpu_admission: %s", w.Body.String())
	var gpu GPUAdmissionStatus
	require.NoError(t, json.Unmarshal(raw, &gpu))
	require.Equal(t, 2, gpu.Lanes)
	require.Equal(t, 3, gpu.Slots)
	require.False(t, gpu.ParityOK)
}
