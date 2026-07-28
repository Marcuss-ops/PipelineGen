package middleware

import (
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/primitives"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job/workspace"
	"github.com/gin-gonic/gin"
)

// WorkspaceScopeMiddleware extracts workspace and project IDs from headers or query params.
// It validates the requested workspace against the authenticated principal:
//   - Admin principals may request any workspace (the header indicates a choice).
//   - Worker principals are restricted to the default workspace (no multi-tenant escape).
//   - Unauthenticated users cannot override the workspace at all.
//
// PR-DOMAIN-PRIMITIVES-NOMINAL (July 2026): the local workspaceID
// variable is typed as primitives.WorkspaceID at the boundary so
// the empty/reserved-sentinel checks use the canonical .IsEmpty()
// method and the NewWorkspaceID reserved-sentinel comparator. The
// downstream workspace.NewScope call is converted back to string
// via .String() so the kernel.Scope API stays untouched (deep
// package change is intentionally out of scope for this gradual
// PR — kernel.Scope will migrate in a followup).
func WorkspaceScopeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		isAdmin, _ := c.Get("is_admin")

		workspaceID := primitives.NewWorkspaceID(c.GetHeader("X-Workspace-ID"))
		projectID := c.GetHeader("X-Project-ID")

		if workspaceID.IsEmpty() {
			workspaceID = primitives.NewWorkspaceID(c.Query("workspace_id"))
		}
		if projectID == "" {
			projectID = c.Query("project_id")
		}

		// Derive the allowed workspace from the principal, not from the request alone.
		if isAdmin != true {
			// Workers and anonymous users are NOT allowed to select arbitrary workspaces.
			// They get the default scope regardless of what the request says.
			if !workspaceID.IsEmpty() && workspaceID != primitives.NewWorkspaceID("default") {
				c.JSON(http.StatusForbidden, gin.H{"error": "workspace selection not permitted for this principal"})
				c.Abort()
				return
			}
			workspaceID = primitives.NewWorkspaceID("default")
			projectID = "default"
		}

		scope := workspace.NewScope(workspaceID.String(), projectID)
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
