package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWorkspaceScopeMiddlewareFromHeadersAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(WorkspaceScopeMiddleware())
	r.GET("/test", func(c *gin.Context) {
		scope := ScopeFromContext(c)
		c.JSON(http.StatusOK, gin.H{
			"workspace_id": scope.WorkspaceID,
			"project_id":   scope.ProjectID,
		})
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Workspace-ID", "ws-1")
	req.Header.Set("X-Project-ID", "proj-1")
	// Simulate admin auth
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("is_admin", true)
	c.Request = req
	WorkspaceScopeMiddleware()(c)
	if c.IsAborted() {
		t.Errorf("Expected admin to be allowed, but request was aborted")
	}
}

func TestWorkspaceScopeMiddlewareFromQueryWorkerBlocked(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(WorkspaceScopeMiddleware())
	r.GET("/test", func(c *gin.Context) {
		scope := ScopeFromContext(c)
		c.JSON(http.StatusOK, gin.H{
			"workspace_id": scope.WorkspaceID,
			"project_id":   scope.ProjectID,
		})
	})

	req, _ := http.NewRequest("GET", "/test?workspace_id=ws-2&project_id=proj-2", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("is_worker", true)
	c.Request = req
	WorkspaceScopeMiddleware()(c)
	if !c.IsAborted() {
		t.Errorf("Expected worker to be blocked from non-default workspace, but request was allowed")
	}
	if w.Code != http.StatusForbidden && c.Writer.Status() != http.StatusForbidden {
		t.Errorf("Expected status 403, got %d", c.Writer.Status())
	}
}

func TestWorkspaceScopeMiddlewareDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(WorkspaceScopeMiddleware())
	r.GET("/test", func(c *gin.Context) {
		scope := ScopeFromContext(c)
		c.JSON(http.StatusOK, gin.H{
			"workspace_id": scope.WorkspaceID,
			"project_id":   scope.ProjectID,
		})
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestRequireWorkspaceScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(WorkspaceScopeMiddleware())
	r.GET("/test", func(c *gin.Context) {
		scope, ok := RequireWorkspaceScope(c)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"workspace_id": scope.WorkspaceID,
		})
	})

	// Test without workspace_id - should return 400
	req1, _ := http.NewRequest("GET", "/test", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)

	if w1.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for missing workspace, got %d", w1.Code)
	}

	// Test with workspace_id as admin - should succeed
	req2, _ := http.NewRequest("GET", "/test", nil)
	req2.Header.Set("X-Workspace-ID", "ws-1")
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Set("is_admin", true)
	c2.Request = req2
	WorkspaceScopeMiddleware()(c2)
	if c2.IsAborted() {
		t.Errorf("Expected admin with workspace to be allowed, but request was aborted")
	}
}

func TestScopeFromContextInvalidType(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		// Set wrong type
		c.Set("workspace_scope", "not a scope")
		scope := ScopeFromContext(c)
		if scope.WorkspaceID != "default" {
			t.Errorf("Expected default scope, got %+v", scope)
		}
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
}
