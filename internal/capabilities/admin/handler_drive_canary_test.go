package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/clipfolder"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const canaryTestAliasesYAML = `
folder_aliases:
  boxe:
    path: Boxe
    normalized_group: boxe
    folder_id: ""
`

type canaryPublisherFake struct {
	publishCalls int
	resolveCalls int
	publishReq   delivery.PublishRequest
	resolveReq   delivery.PublishRequest
	resolveID    string
	resolveErr   error
	publishErr   error
	nilResult    bool
}

func (f *canaryPublisherFake) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	f.publishCalls++
	f.publishReq = req
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	if _, err := os.Stat(req.LocalPath); err != nil {
		return nil, err
	}
	if f.nilResult {
		return nil, nil
	}
	return &delivery.PublishResult{
		FileID:      "canary-file-id",
		WebViewLink: "https://drive.google.com/file/d/canary-file-id/view",
		FolderID:    req.ParentFolderID,
	}, nil
}

func (f *canaryPublisherFake) ResolveFolder(_ context.Context, req delivery.PublishRequest) (string, error) {
	f.resolveCalls++
	f.resolveReq = req
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	return f.resolveID, nil
}

func newCanaryTestHandler(t *testing.T, publisher delivery.Publisher) *DriveCanaryHandler {
	t.Helper()
	return NewDriveCanaryHandler(publisher, newCanaryTestResolver(t), zap.NewNop())
}

func newCanaryTestResolver(t *testing.T) *clipfolder.FolderAliasResolver {
	t.Helper()
	resolver, err := clipfolder.NewFolderAliasResolverFromBytes([]byte(canaryTestAliasesYAML))
	if err != nil {
		t.Fatalf("build resolver: %v", err)
	}
	return resolver
}

func canaryJSON(fields string) string {
	return "{" + strings.ReplaceAll(fields, `\"`, `"`) + "}"
}

func performCanaryRequest(t *testing.T, handler *DriveCanaryHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/canary-upload", handler.CanaryUpload)

	req := httptest.NewRequest(http.MethodPost, "/canary-upload", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func responseError(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, response.Body.String())
	}
	return payload.Error
}

func TestCanaryUploadRequiresExactlyOneFolderSelector(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "neither", body: `{}`},
		{name: "whitespace-only", body: canaryJSON(`"folder_id":"  ","folder_alias":"\t"`)},
		{name: "both", body: canaryJSON(`"folder_id":"explicit-id","folder_alias":"boxe"`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			publisher := &canaryPublisherFake{}
			response := performCanaryRequest(t, newCanaryTestHandler(t, publisher), test.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if got := responseError(t, response); got != "exactly one of folder_id or folder_alias is required" {
				t.Fatalf("error = %q", got)
			}
			if publisher.publishCalls != 0 || publisher.resolveCalls != 0 {
				t.Fatalf("invalid selector invoked publisher: publish=%d resolve=%d", publisher.publishCalls, publisher.resolveCalls)
			}
		})
	}
}

func TestCanaryUploadRejectsMalformedJSON(t *testing.T) {
	publisher := &canaryPublisherFake{}
	body := canaryJSON(`"folder_id":"explicit-folder-id"`)
	response := performCanaryRequest(t, newCanaryTestHandler(t, publisher), body[:len(body)-1])
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if publisher.publishCalls != 0 || publisher.resolveCalls != 0 {
		t.Fatalf("malformed JSON invoked publisher: publish=%d resolve=%d", publisher.publishCalls, publisher.resolveCalls)
	}
}

func TestCanaryUploadWithFolderIDPublishesExplicitRoot(t *testing.T) {
	publisher := &canaryPublisherFake{}
	response := performCanaryRequest(t, newCanaryTestHandler(t, publisher), canaryJSON(`"folder_id":"explicit-folder-id"`))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if publisher.resolveCalls != 0 {
		t.Fatalf("explicit folder_id unexpectedly resolved as alias")
	}
	if publisher.publishCalls != 1 || publisher.publishReq.ParentFolderID != "explicit-folder-id" {
		t.Fatalf("publish request = %+v, calls=%d", publisher.publishReq, publisher.publishCalls)
	}
}

func TestCanaryUploadWithFolderAliasUsesCanonicalResolverAndPublisher(t *testing.T) {
	publisher := &canaryPublisherFake{resolveID: "resolved-boxe-folder-id"}
	response := performCanaryRequest(t, newCanaryTestHandler(t, publisher), canaryJSON(`"folder_alias":" Boxe "`))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if publisher.resolveCalls != 1 {
		t.Fatalf("ResolveFolder calls = %d, want 1", publisher.resolveCalls)
	}
	if publisher.resolveReq.Destination != delivery.DestinationYouTubeClip ||
		publisher.resolveReq.Group != "Boxe" ||
		publisher.resolveReq.Subject != "pipelinegen-canary" {
		t.Fatalf("canonical resolve request = %+v", publisher.resolveReq)
	}
	if publisher.publishCalls != 1 || publisher.publishReq.ParentFolderID != "resolved-boxe-folder-id" {
		t.Fatalf("publish request = %+v, calls=%d", publisher.publishReq, publisher.publishCalls)
	}
}

func TestCanaryUploadRejectsUnknownFolderAliasBeforeDriveWrite(t *testing.T) {
	publisher := &canaryPublisherFake{}
	response := performCanaryRequest(t, newCanaryTestHandler(t, publisher), canaryJSON(`"folder_alias":"unknown"`))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if publisher.publishCalls != 0 || publisher.resolveCalls != 0 {
		t.Fatalf("unknown alias invoked publisher: publish=%d resolve=%d", publisher.publishCalls, publisher.resolveCalls)
	}
}

func TestCanaryUploadFailsClosedWhenPublisherIsMissing(t *testing.T) {
	response := performCanaryRequest(t, NewDriveCanaryHandler(nil, newCanaryTestResolver(t), zap.NewNop()), canaryJSON(`"folder_id":"explicit-folder-id"`))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	if got := responseError(t, response); got != "Drive publisher not wired — check composition root" {
		t.Fatalf("error = %q", got)
	}
}

func TestCanaryUploadFailsClosedWhenAliasResolverIsMissing(t *testing.T) {
	publisher := &canaryPublisherFake{}
	response := performCanaryRequest(t, NewDriveCanaryHandler(publisher, nil, zap.NewNop()), canaryJSON(`"folder_alias":"Boxe"`))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	if publisher.resolveCalls != 0 || publisher.publishCalls != 0 {
		t.Fatalf("missing resolver invoked publisher: resolve=%d publish=%d", publisher.resolveCalls, publisher.publishCalls)
	}
}

func TestCanaryUploadFailsClosedWhenAliasFolderResolutionFails(t *testing.T) {
	publisher := &canaryPublisherFake{resolveErr: errors.New("resolver backend unavailable")}
	response := performCanaryRequest(t, newCanaryTestHandler(t, publisher), canaryJSON(`"folder_alias":"Boxe"`))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if publisher.resolveCalls != 1 || publisher.publishCalls != 0 {
		t.Fatalf("resolution failure continued to publish: resolve=%d publish=%d", publisher.resolveCalls, publisher.publishCalls)
	}
}

func TestCanaryUploadFailsClosedWhenPublishFails(t *testing.T) {
	publisher := &canaryPublisherFake{publishErr: errors.New("drive write refused")}
	response := performCanaryRequest(t, newCanaryTestHandler(t, publisher), canaryJSON(`"folder_id":"explicit-folder-id"`))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if publisher.publishCalls != 1 {
		t.Fatalf("publish calls = %d, want 1", publisher.publishCalls)
	}
}

func TestCanaryUploadFailsClosedWhenPublisherReturnsNilResult(t *testing.T) {
	publisher := &canaryPublisherFake{nilResult: true}
	response := performCanaryRequest(t, newCanaryTestHandler(t, publisher), canaryJSON(`"folder_id":"explicit-folder-id"`))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if publisher.publishCalls != 1 {
		t.Fatalf("publish calls = %d, want 1", publisher.publishCalls)
	}
}
