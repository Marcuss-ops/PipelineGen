package generation

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
	"go.uber.org/zap"

	appgeneration "github.com/Marcuss-ops/PipelineGen/internal/application/generation"
	domaingeneration "github.com/Marcuss-ops/PipelineGen/internal/domain/generation"
)

type fakeService struct {
	createResp *appgeneration.CreateResponse
	statusResp *appgeneration.StatusResponse
	createErr  error
	statusErr  error
	cancelErr  error
}

func (f *fakeService) Create(ctx context.Context, req domaingeneration.Request) (*appgeneration.CreateResponse, error) {
	return f.createResp, f.createErr
}

func (f *fakeService) Status(ctx context.Context, id string) (*appgeneration.StatusResponse, error) {
	return f.statusResp, f.statusErr
}

func (f *fakeService) Cancel(ctx context.Context, id string) error {
	return f.cancelErr
}

func TestHandler_Create_Accepts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeService{
		createResp: &appgeneration.CreateResponse{},
	}
	svc.createResp.OK = true
	svc.createResp.Job.ID = "job-123"
	svc.createResp.Job.Type = "book.generate"
	svc.createResp.Job.Status = "QUEUED"
	svc.createResp.Job.StatusURL = "/api/generations/job-123"
	h := NewHandler(svc, zap.NewNop())
	router := gin.New()
	router.POST("/generations", h.Create)

	body := `{"type":"book.generate","input":{"source_asset_id":"asset-123","language":"it"}}`
	req := httptest.NewRequest(http.MethodPost, "/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, true, got["ok"])
}

func TestHandler_Status_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeService{statusErr: appgeneration.ErrJobNotFound}
	h := NewHandler(svc, zap.NewNop())
	router := gin.New()
	router.GET("/generations/:id", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/generations/job-123", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandler_Create_UnsupportedType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeService{createErr: errors.Join(appgeneration.ErrUnsupportedType, errors.New("bad.type"))}
	h := NewHandler(svc, zap.NewNop())
	router := gin.New()
	router.POST("/generations", h.Create)

	req := httptest.NewRequest(http.MethodPost, "/generations", strings.NewReader(`{"type":"bad.type","input":{}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}
