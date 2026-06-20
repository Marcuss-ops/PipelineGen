// Package transport is the canonical entry point for HTTP handlers.
//
// It enforces the contract declared in AGENTS.md (Pattern 1) and
// the API surface directives in docs/FUTURE_IMPLEMENTATIONS.md:
//
//	bind  →  validate  →  invoke  →  map error  →  respond
//
// Handlers MUST NOT import database/sql, os/exec, mattn/go-sqlite3,
// or any google.golang.org/api/drive/* package directly. All side
// effects are delegated to a UseCase defined in internal/service/
// or internal/application/, executed here via JSON.
//
// The canonical flow is:
//
//	func (h *Handler) Action(c *gin.Context) {
//	    transport.JSON(c, h.useCase, h.errorMapper)
//	}
//
// with `useCase` constructed in WireRegistry (constructor injection)
// and `errorMapper` defined alongside the use case so domain errors
// map to consistent HTTP status codes.
package transport

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
)

// JSONBound is satisfied by request types that need structural
// validation after JSON binding (required fields, value ranges, etc.).
// It is opt-in: a request type without a Validate() method is still
// accepted by JSON.
type JSONBound interface {
	Validate() error
}

// UseCase is a domain operation invoked from the API transport layer.
// Implementations MUST live in internal/service/... or
// internal/application/... and MUST NOT import gin, database/sql,
// os/exec, or any media pipeline driver. The transport layer is the
// only place allowed to talk to JSON and gin.Context.
//
// The type parameters `In` and `Out` are checked at the import-site;
// when `In = any`, the runtime concrete type is what feeds Validate().
// This is intentional so a single UseCase may serve multiple request
// types in the future without losing the validator assertion.
type UseCase[In any, Out any] interface {
	Handle(ctx context.Context, req In) (Out, error)
}

// ErrorMapper translates a domain error to (HTTP status, public message).
// Returning status == 0 falls back to 500; returning msg == "" falls
// back to a safe default ("request rejected" for 4xx; err.Error() for
// 5xx). The mapper is the single source of truth for status code
// selection; transport never overrides it.
type ErrorMapper func(err error) (status int, msg string)

// JSON is the canonical handler pipeline for JSON-bound use cases:
//
//	1. bind   — parse JSON body (400 on failure)
//	2. validate — call Validate() if implemented (400 on failure)
//	3. invoke — call uc.Handle with the request context
//	4. map    — translate domain errors via mapper (or 500)
//	5. respond — write JSON via api.OK (2xx) or api.Error (mapped status)
//
// Handlers in internal/api/<domain>/ should be one-liners that call
// transport.JSON with a use case injected by the composition root.
//
// Both 4xx and 5xx responses go through api.Error so the mapped status
// is preserved on the wire. api.InternalError is intentionally NOT used
// here because it hardcodes the wire status to http.StatusInternalServerError,
// which would defeat mappers returning 502 / 503 / 504 for upstream
// failures (e.g. AI gateway timeout → BadGateway).
//
// Panic recovery is delegated to the gin Recovery() middleware wired
// in internal/api/routes.go::Setup. Adding a recover() inside JSON
// would double-log and silently swallow real errors; do not introduce
// one here.
func JSON[In any, Out any](c *gin.Context, uc UseCase[In, Out], mapper ErrorMapper) {
	var req In
	if !bindJSON(c, &req) {
		return
	}
	if v, ok := any(req).(JSONBound); ok {
		if err := v.Validate(); err != nil {
			api.BadRequest(c, err.Error())
			return
		}
	}
	resp, err := uc.Handle(c.Request.Context(), req)
	if err != nil {
		status, msg := mapErr(err, mapper)
		api.Error(c, status, msg)
		return
	}
	api.OK(c, resp)
}

// bindJSON is a thin wrapper to keep the JSON pipeline single-purpose.
// Centralising the BadRequest call here means all handlers return the
// same error envelope for malformed bodies.
func bindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		api.BadRequest(c, err.Error())
		return false
	}
	return true
}

// mapErr applies the optional ErrorMapper with sensible fallbacks.
// Mapping rules:
//
//   - nil mapper           → 500 + err.Error() (msg only used for tracing;
//                              the wire body comes from api.Error).
//   - mapper status == 0   → fallback 500.
//   - mapper msg    == ""   → status >= 500 → err.Error() (safe for ops
//                              since 5xx body is an error string anyway
//                              and the mapped public message has already
//                              been overridden by err.Error()).
//                            status <  500 → safe generic message
//                              ("request rejected") — NEVER err.Error(),
//                              since 4xx responses go through api.Error
//                              which echoes the string directly to clients.
//
// The 4xx safe default prevents wrapped DB / driver error messages
// from leaking to clients when a mapper returns an empty message.
func mapErr(err error, mapper ErrorMapper) (int, string) {
	const safePublic = "request rejected"
	if mapper == nil {
		return http.StatusInternalServerError, err.Error()
	}
	status, msg := mapper(err)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if msg == "" {
		if status >= http.StatusInternalServerError {
			msg = err.Error()
		} else {
			msg = safePublic
		}
	}
	return status, msg
}
