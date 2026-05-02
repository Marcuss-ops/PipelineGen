package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/core/workspace"
)

// WorkspaceScopeMiddleware extracts workspace and project IDs from headers or query params.
// It sets the workspace.Scope in the gin context.
func WorkspaceScopeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		workspaceID := c.GetHeader("X-Workspace-ID")
		projectID := c.GetHeader("X-Project-ID")

		if workspaceID == "" {
			workspaceID = c.Query("workspace_id")
		}
		if projectID == "" {
			projectID = c.Query("project_id")
		}

		scope := workspace.NewScope(workspaceID, projectID)
		c.Set("workspace_scope", scope)

		c.Next()
	}
}

// ScopeFromContext retrieves the workspace.Scope from the gin context.
// Returns the default scope if not set or invalid.
func ScopeFromContext(c *gin.Context) workspace.Scope {
	value, ok := c.Get("workspace_scope")
	if !ok {
		return workspace.DefaultScope()
	}

	scope, ok := value.(workspace.Scope)
	if !ok {
		return workspace.DefaultScope()
	}

	return scope
}

// RequireWorkspaceScope is a helper to get scope or return 400 if missing.
// This is useful for endpoints that require explicit workspace/project IDs.
func RequireWorkspaceScope(c *gin.Context) (workspace.Scope, bool) {
	scope := ScopeFromContext(c)
	if scope.WorkspaceID == "" || scope.WorkspaceID == "default" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace_id is required"})
		return scope, false
	}
	return scope, true
}
