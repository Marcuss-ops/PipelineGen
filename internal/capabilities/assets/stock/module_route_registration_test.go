package assets

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
)

func TestBuild_RegistersRunRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := stockpipeline.NewStockUseCase(
		&fakeServiceRunner{},
		&fakeJobsEnqueuer{jobID: "job-route"},
		zap.NewNop(),
	)
	descriptor, err := Build(Dependencies{
		UseCase:     uc,
		EnabledFunc: func() bool { return true },
		Logger:      zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	router := gin.New()
	descriptor.RegisterRoutes(router.Group("/api"))
	req := httptest.NewRequest(http.MethodPost, "/api/stock-pipeline/run", bytes.NewBufferString(
		`{"direct_urls":["https://example.com/video.mp4"],"async":true}`,
	))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("POST /api/stock-pipeline/run = %d, want 202: %s", res.Code, res.Body.String())
	}

	searchReq := httptest.NewRequest(http.MethodPost, "/api/stock-pipeline/search-and-run", bytes.NewBufferString(
		`{"queries":[{"q":"boxing","limit":1}],"folder_id":"folder-1","async":true}`,
	))
	searchReq.Header.Set("Content-Type", "application/json")
	searchRes := httptest.NewRecorder()
	router.ServeHTTP(searchRes, searchReq)
	if searchRes.Code != http.StatusAccepted {
		t.Fatalf("POST /api/stock-pipeline/search-and-run = %d, want 202: %s", searchRes.Code, searchRes.Body.String())
	}
}
