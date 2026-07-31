package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/clipfolder"
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
}

func (f *canaryPublisherFake) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	f.publishCalls++
	f.publishReq = req
	if _, err := os.Stat(req.LocalPath); err != nil {
		return nil, err
	}
	return &delivery.PublishResult{
		FileID:      "canary-file-id",
		WebViewLink: "https://drive.google.com/file/d/canary-file-id/view",
		FolderID:    req.RootFolderOverride,
	}, nil
}

func (f *canaryPublisherFake) ResolveFolder(_ context.Context, req delivery.PublishRequest) (string, error) {
	f.resolveCalls++
	f.resolveReq = req
	return f.resolveID, nil
}

func newCanaryTestHandler(t *testing.T, publisher delivery.Publisher) *DriveCanaryHandler {
	t.Helper()
	resolver, err := clipfolder.NewFolderAliasResolverFromBytes([]byte(canaryTestAliasesYAML))
	if err != nil {
		t.Fatalf("build resolver: %v", err)
	}
	return NewDriveCanaryHandler(publisher, resolver, zap.NewNop())
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
		{name: "whitespace-only", body: `{"folder_id":"  ","folder_alias":"\t"}`},
		{name: "both", body: `{"folder_id":"explicit-id","folder_alias":"boxe"}`},
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

func TestCanaryUploadWithFolderIDPublishesExplicitRoot(t *testing.T) {
	publisher := &canaryPublisherFake{}
	response := performCanaryRequest(t, newCanaryTestHandler(t, publisher), `{"folder_id":"explicit-folder-id"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if publisher.resolveCalls != 0 {
		t.Fatalf("explicit folder_id unexpectedly resolved as alias")
	}
	if publisher.publishCalls != 1 || publisher.publishReq.RootFolderOverride != "explicit-folder-id" {
		t.Fatalf("publish request = %+v, calls=%d", publisher.publishReq, publisher.publishCalls)
	}
}

func TestCanaryUploadWithFolderAliasUsesCanonicalResolverAndPublisher(t *testing.T) {
	publisher := &canaryPublisherFake{resolveID: "resolved-boxe-folder-id"}
	response := performCanaryRequest(t, newCanaryTestHandler(t, publisher), `{"folder_alias":" Boxe "}`)
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
	if publisher.publishCalls != 1 || publisher.publishReq.RootFolderOverride != "resolved-boxe-folder-id" {
		t.Fatalf("publish request = %+v, calls=%d", publisher.publishReq, publisher.publishCalls)
	}
}

func TestCanaryUploadRejectsUnknownFolderAliasBeforeDriveWrite(t *testing.T) {
	publisher := &canaryPublisherFake{}
	response := performCanaryRequest(t, newCanaryTestHandler(t, publisher), `{"folder_alias":"unknown"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if publisher.publishCalls != 0 || publisher.resolveCalls != 0 {
		t.Fatalf("unknown alias invoked publisher: publish=%d resolve=%d", publisher.publishCalls, publisher.resolveCalls)
	}
}
