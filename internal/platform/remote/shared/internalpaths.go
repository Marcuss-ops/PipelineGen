// Package shared hosts cross-client constants used by the remote-worker
// HTTP clients (jobbrokerclient, assettransferclient). The single
// source of truth lives here so the worker's HTTP calls line up with
// the server's `/internal/v1` router group.
//
// Adding a new request from the worker? Use `InternalPathPrefix + …`
// instead of hardcoded `/api/…` to avoid seeing this drift silently —
// every such mismatch surfaces as a 404 from the Gin router with no
// obvious breadcrumb back to the URL constant.
package shared

// InternalPathPrefix is the URL prefix the server mounts the worker
// broker handler on (see `internal/api/routes.go::Setup`,
// `engine.Group("/internal/v1")`). Kept in sync with the server's
// route registration; updating one without the other breaks every
// remote-worker claim.
//
// Format (per Gin's RuleGroup semantics): leading slash, no trailing
// slash.
const InternalPathPrefix = "/internal/v1"
