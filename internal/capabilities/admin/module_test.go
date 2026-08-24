package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	apimiddleware "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestBuildFailsClosedWhenMandatoryDependencyIsMissing(t *testing.T) {
	validPublisher := &canaryPublisherFake{}
	validResolver := newCanaryTestResolver(t)
	validEnabled := func() bool { return true }

	cases := []struct {
		name string
		deps Dependencies
		want string
	}{
		{
			name: "publisher",
			deps: Dependencies{
				FolderAliasResolver: validResolver,
				EnabledFunc:         validEnabled,
			},
			want: "Publisher is required",
		},
		{
			name: "folder alias resolver",
			deps: Dependencies{
				Publisher:   validPublisher,
				EnabledFunc: validEnabled,
			},
			want: "FolderAliasResolver is required",
		},
		{
			name: "enabled function",
			deps: Dependencies{
				Publisher:           validPublisher,
				FolderAliasResolver: validResolver,
			},
			want: "EnabledFunc is required",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			descriptor, err := Build(test.deps)
			if err == nil {
				t.Fatalf("Build() returned descriptor %+v, want error containing %q", descriptor, test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want substring %q", err, test.want)
			}
		})
	}
}

func TestBuildMountsProtectedCanaryRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	publisher := &canaryPublisherFake{}
	security := &apimiddleware.TokenSecurityAdapter{
		Enable: true,
		Admin:  "admin-secret",
	}

	descriptor, err := Build(Dependencies{
		Publisher:           publisher,
		FolderAliasResolver: newCanaryTestResolver(t),
		EnabledFunc:         func() bool { return true },
		ModuleOpts: []api.RouteModuleOption{
			api.WithMiddleware(apimiddleware.RequireAdminToken(security, zap.NewNop())),
		},
		Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	engine := gin.New()
	descriptor.RegisterRoutes(engine.Group("/api/admin"))

	found := false
	for _, route := range engine.Routes() {
		if route.Method == http.MethodPost && route.Path == "/api/admin/drive/canary-upload" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("admin canary route was not mounted; routes=%v", engine.Routes())
	}

	request := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/drive/canary-upload", strings.NewReader(`{"folder_id":"explicit-folder-id"}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-Velox-Admin-Token", token)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		return response
	}

	unauthorized := request("")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("without admin token status = %d, want %d; body=%s", unauthorized.Code, http.StatusUnauthorized, unauthorized.Body.String())
	}
	if publisher.publishCalls != 0 {
		t.Fatalf("unauthorized request reached publisher: calls=%d", publisher.publishCalls)
	}

	authorized := request("admin-secret")
	if authorized.Code != http.StatusOK {
		t.Fatalf("with admin token status = %d, want %d; body=%s", authorized.Code, http.StatusOK, authorized.Body.String())
	}
	for _, expected := range []string{`"ok":true`, `"file_id":"canary-file-id"`, `"folder_id":"explicit-folder-id"`} {
		if !strings.Contains(authorized.Body.String(), expected) {
			t.Fatalf("authorized response missing %s: body=%s", expected, authorized.Body.String())
		}
	}
	if publisher.publishCalls != 1 {
		t.Fatalf("authorized request publish calls = %d, want 1", publisher.publishCalls)
	}
}
