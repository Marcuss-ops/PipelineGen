package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWorkspaceScopeMiddlewareFromHeaders(t *testing.T) {
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

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestWorkspaceScopeMiddlewareFromQuery(t *testing.T) {
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
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
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

	// Test with workspace_id - should succeed
	req2, _ := http.NewRequest("GET", "/test", nil)
	req2.Header.Set("X-Workspace-ID", "ws-1")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("Expected status 200 with workspace, got %d", w2.Code)
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
