package workspace

import "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"

// Scope represents a workspace and project context.
type Scope struct {
	WorkspaceID string
	ProjectID   string
}

// DefaultScope returns a default scope with "default" for both workspace and project.
func DefaultScope() Scope {
	return Scope{
		WorkspaceID: "default",
		ProjectID:   "default",
	}
}

// NewScope creates a new Scope, normalizing empty values to "default".
func NewScope(workspaceID, projectID string) Scope {
	return Scope{
		WorkspaceID: platform.String(workspaceID, "default"),
		ProjectID:   platform.String(projectID, "default"),
	}
}
