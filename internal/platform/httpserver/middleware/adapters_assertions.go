// Package middleware — compile-time assertions that the concrete
// HTTP-middleware adapters here satisfy the application-layer
// SecurityAdapter ports declared in
// internal/capabilities/middleware/ports.go.
//
// PG-006.1 round-2 (June 2026): the previous home of this assertion
// was internal/capabilities/middleware/ports.go (port-side). After
// the pkg/middleware → internal/platform/httpserver/middleware relocation, ports.go
// importing the api/middleware package would create an import cycle
// (admin_token.go already imports the ports the other direction).
// Per Pattern 0, the implementor side now self-attests the contract:
// the concrete defines its own var _ <Port> = (*Adapter)(nil) so any
// signature drift in either the port or the concrete fails at build
// time, before the first auth-gated request can panic.
//
// Rule: keep assertions minimal. New ports land here as additional
// `var _ <Port> = (*Adapter)(nil)` lines, one PR at a time — do NOT
// collapse multiple ports into a single assertion or widen the
// adapter surface to make every assertion trivial.
package middleware

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
)

// Compile-time assertion (PG-006.1 round-2, June 2026): the canonical
// HTTP-middleware concrete *TokenSecurityAdapter satisfies the
// application-layer AuthSecurityPort. Required by Pattern 0 — drift
// in either signature trips build at compile time, not at runtime
// under the first auth-gated request.
//
// Side note: the import alias `middleware` here refers to the
// application-layer ports package (not this file's own package).
// Go resolves the bare `middleware.AuthSecurityPort` to the import,
// and the unqualified `AuthSecurityPort` in the type assertion below
// resolves to the import — which is exactly why this file's import
// statement does not need an alias.
var _ middleware.AuthSecurityPort = (*TokenSecurityAdapter)(nil)
