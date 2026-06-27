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
	"fmt"
	"net/http"
	"strconv"

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
//  1. bind   — parse JSON body (400 on failure)
//  2. validate — call Validate() if implemented (400 on failure)
//  3. invoke — call uc.Handle with the request context
//  4. map    — translate domain errors via mapper (or 500)
//  5. respond — write JSON via api.OK (2xx) or api.Error (mapped status)
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

// ── Request pipeline (path + query parameters) ─────────────────────────────
//
// transport.Request mirrors the transport.JSON contract for handlers that
// read URL path / query-string parameters instead of a JSON body. The
// binder function owns the parsing contract and returns a typed `In`
// ready for the UseCase. This signature was chosen over struct-tag
// reflection (gin's ShouldBindQuery / mapstructure) because:
//
//  1. No silent drops: malformed values produce a typed error which
//     maps to 400 via the standard pipeline.
//  2. No third-party dependency: stays within gin + stdlib.
//  3. No "magic": every binding site is grep-able and explicit.
//
// Canonical usage:
//
//	type ListClipsReq struct {
//	    Source string
//	    transport.PageParams
//	    Q string
//	}
//	func (r ListClipsReq) Validate() error { return nil }
//
//	func bindListClips(c *gin.Context) (ListClipsReq, error) {
//	    page, err := transport.BindPagination(c)
//	    if err != nil { return ListClipsReq{}, err }
//	    return ListClipsReq{
//	        Source: c.Param("source"),
//	        PageParams: page,
//	        Q: strings.TrimSpace(c.Query("q")),
//	    }, nil
//	}
//
//	transport.Request(c, h.listClipsUseCase, h.errMap, bindListClips)
type Binder[In any] func(c *gin.Context) (In, error)

// Request is the canonical handler pipeline for path + query parameters.
// Same contract as JSON: bind → validate → invoke → map → respond.
//
// A handler MUST use exactly one of JSON or Request per route: mixing
// the two pipelines in a single handler is undefined (both attempt to
// write the response, the second is silent no-op). Use JSON for
// POST/PUT/PATCH (body-bound) handlers; use Request for GET/DELETE
// collection + detail handlers where the input is in the URL.
func Request[In any, Out any](
	c *gin.Context,
	uc UseCase[In, Out],
	mapper ErrorMapper,
	bind Binder[In],
) {
	req, err := bind(c)
	if err != nil {
		api.BadRequest(c, err.Error())
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

// PageParams is the standard pagination pair returned by BindPagination.
// UseCases that paginate must embed PageParams as a sub-struct of their
// request type to be idiomatic with the binder.
type PageParams struct {
	Limit  int
	Offset int
}

// Defaults for pagination. DefaultPageLimit is what most list endpoints
// expect when ?limit= is missing. MaxPageLimit caps ?limit= to prevent
// runaway scans on expensive queries. MaxPageLimit is bounded to ≤500
// by int32 safety on 32-bit platforms; opt for BindPaginationWithLimits
// if higher caps are needed on a specific endpoint (e.g. asset exports).
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 500
)

// BindPagination extracts `?limit` and `?offset` with defaults and enforces
// min/max bounds. Returns a 400-mapped error if either value is not a
// valid integer or falls outside [min, max]. Intended to be EMBEDDED
// inside larger binders (see binder example in transport.Request godoc).
func BindPagination(c *gin.Context) (PageParams, error) {
	limit, err := parseIntQuery(c, "limit", DefaultPageLimit, 1, MaxPageLimit)
	if err != nil {
		return PageParams{}, fmt.Errorf("limit: %w", err)
	}
	offset, err := parseIntQuery(c, "offset", 0, 0, MaxPageLimit)
	if err != nil {
		return PageParams{}, fmt.Errorf("offset: %w", err)
	}
	return PageParams{Limit: limit, Offset: offset}, nil
}

// BindPaginationWithLimits lets callers override the per-endpoint
// defaults (e.g. a clip-enumeration endpoint can request limit up to
// 1000, a less expensive search endpoint stays at 50). Bounds are
// inclusive; the function returns a 400-mapped error if `maxLimit < 1`
// or `defaultLimit < 1`, or if `?limit=` is outside [1, maxLimit].
func BindPaginationWithLimits(c *gin.Context, defaultLimit, maxLimit int) (PageParams, error) {
	if defaultLimit < 1 {
		return PageParams{}, fmt.Errorf("BindPaginationWithLimits: defaultLimit=%d must be >=1", defaultLimit)
	}
	if maxLimit < defaultLimit {
		return PageParams{}, fmt.Errorf("BindPaginationWithLimits: maxLimit=%d must be >= defaultLimit=%d", maxLimit, defaultLimit)
	}
	limit, err := parseIntQuery(c, "limit", defaultLimit, 1, maxLimit)
	if err != nil {
		return PageParams{}, fmt.Errorf("limit: %w", err)
	}
	offset, err := parseIntQuery(c, "offset", 0, 0, maxLimit)
	if err != nil {
		return PageParams{}, fmt.Errorf("offset: %w", err)
	}
	return PageParams{Limit: limit, Offset: offset}, nil
}

// parseIntQuery reads a query param, applies default, enforces min/max.
// Empty string returns defaultVal. Anything else is parsed as a base-10
// int and bounds-checked. Returns a 400-mapped error otherwise.
func parseIntQuery(c *gin.Context, key string, defaultVal, min, max int) (int, error) {
	raw := c.Query(key)
	if raw == "" {
		return defaultVal, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a valid integer", key, raw)
	}
	if v < min {
		return 0, fmt.Errorf("%s=%d below minimum %d", key, v, min)
	}
	if v > max {
		return 0, fmt.Errorf("%s=%d above maximum %d", key, v, max)
	}
	return v, nil
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
//     the wire body comes from api.Error).
//   - mapper status == 0   → fallback 500.
//   - mapper msg    == ""   → status >= 500 → err.Error() (safe for ops
//     since 5xx body is an error string anyway
//     and the mapped public message has already
//     been overridden by err.Error()).
//     status <  500 → safe generic message
//     ("request rejected") — NEVER err.Error(),
//     since 4xx responses go through api.Error
//     which echoes the string directly to clients.
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
