// Package apiutil provides shared, leaf-only HTTP helpers for Gin handlers.
//
// pkg/apiutil is the canonical home for: BindJSON[T], the standard response helpers
// (OK / Error / BadRequest / InternalError / NotFound / Accepted), and ClampLimit.
// It is intentionally leaf-only (no imports from internal/) so any handler can
// consume it without dragging in transitive weight.
//
// Three redundant copies of these helpers used to live in internal/api/
// (internal/api/helpers.go and the now-deleted internal/api/sources/*/helpers.go
// duplicates). This package is the single source of truth.
package apiutil

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

// BindJSONInto binds JSON into an existing pointer target. Returns false (and
// writes a 400 response) on error. Use BindJSON[T] in new code.
func BindJSONInto(c *gin.Context, obj any) bool {
	if err := c.ShouldBindJSON(obj); err != nil {
		BadRequest(c, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// BindJSON decodes JSON from the request body into a new value of type T.
// On error it writes a 400 response and returns false. This is the canonical
// idiom for type-safe request binding across all handlers:
//
//	req, ok := apiutil.BindJSON[FooRequest](c)
//	if !ok { return }
//
// All call sites previously using literal c.ShouldBindJSON should migrate to
// this helper so the 400 contract is uniform.
func BindJSON[T any](c *gin.Context) (T, bool) {
	var req T
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return req, false
	}
	return req, true
}

// ParseJSON decodes raw JSON bytes into a map. Returns nil on error.
// Convenience helper for "passthrough" payloads that have already been
// received as []byte (e.g. job results, async payloads).
func ParseJSON(data []byte) map[string]any {
	var m map[string]any
	if len(data) == 0 {
		return nil
	}
	_ = json.Unmarshal(data, &m)
	return m
}

// ClampLimit clamps a value to [def, max]; returns def if v <= 0 or v > max.
// Used by list/pagination endpoints to bound response sizes.
func ClampLimit(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// OK writes a 200 response with the given data payload.
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, data)
}

// Accepted writes a 202 response (used by async job enqueue endpoints).
func Accepted(c *gin.Context, data any) {
	c.JSON(http.StatusAccepted, data)
}

// BadRequest writes a 400 response with the given message.
func BadRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": msg})
}

// NotFound writes a 404 response with the given message.
func NotFound(c *gin.Context, msg string) {
	c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": msg})
}

// InternalError writes a 500 response with err.Error() as the message.
func InternalError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
}

// Error writes a response with a custom HTTP status and message.
// Use this for status codes outside the standard 400/404/500 aliases
// (e.g. 503 Service Unavailable, 409 Conflict, 401 Unauthorized).
func Error(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"ok": false, "error": msg})
}
