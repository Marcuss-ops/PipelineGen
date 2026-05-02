package workspace

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
	if workspaceID == "" {
		workspaceID = "default"
	}
	if projectID == "" {
		projectID = "default"
	}
	return Scope{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
	}
}
