package primitives

// WorkspaceID is the canonical nominal type for the workspace
// identifier. Workspaces are the multi-tenancy isolation unit in the
// platform: every request scope carries a WorkspaceID that the
// application layer uses to namespace data and authorize access.
//
// godlike/06 SSOT (narrow port doctrine): WorkspaceID is the *only*
// typed identifier the domain/application layer accepts/exposes for
// workspaces. Boundary code (HTTP middleware, CLI) is responsible for
// converting raw strings into WorkspaceID via NewWorkspaceID.
//
// Wire identity: JSON marshaling preserves the underlying string
// value with zero overhead because `type WorkspaceID string` is a
// Go-defined named string type (no MarshalJSON/UnmarshalJSON needed).
type WorkspaceID string

// NewWorkspaceID wraps a raw string into a canonical WorkspaceID. The
// constructor is pure: empty input is allowed and surfaces via
// IsEmpty at the handler boundary, where richer error mapping exists.
//
// Reserved values: the literal "default" is the singleton
// un-namespaced workspace and is treated as empty by the middleware
// (see internal/platform/httpserver/middleware/middleware_workspace_scope.go). The
// canonical predicate is `IsEmpty() || s == "default"`.
func NewWorkspaceID(s string) WorkspaceID { return WorkspaceID(s) }

// IsEmpty reports whether the WorkspaceID is the zero value. Middleware
// short-circuits empty WorkspaceIDs to the "default" workspace without
// failing (this is the multi-tenancy escape hatch for unscoped routes
// like /healthz); handlers that require an explicit scope must reject
// empty WorkspaceIDs via the middleware's strict-mode flag.
func (id WorkspaceID) IsEmpty() bool { return id == "" }

// String returns the underlying string form. Required by the fmt
// contract; deliberately NOT a `MarshalJSON`/`UnmarshalJSON` so the
// JSON wire format stays byte-identical with the raw-string version.
func (id WorkspaceID) String() string { return string(id) }
