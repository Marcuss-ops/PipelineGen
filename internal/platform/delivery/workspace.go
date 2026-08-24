package delivery

// WorkspaceContext is the canonical tenant-identity envelope
// consumed by the asset-delivery signer + Verify helpers.
type WorkspaceContext struct {
	WorkspaceID string // tenant workspace; empty disables semantic backend
	UserID      string // optional user-level identifier for audit
	IsAdmin     bool   // admin principals may pick arbitrary workspaces
	IsSystem    bool   // explicit cross-workspace/system scope for admin/reconcile paths
}

// IsZero reports whether the WorkspaceContext has no identity fields set.
func (w WorkspaceContext) IsZero() bool {
	return w.WorkspaceID == "" && w.UserID == "" && !w.IsAdmin && !w.IsSystem
}
