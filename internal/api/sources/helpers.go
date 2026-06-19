// Package sources provides HTTP handlers and types for media/sources endpoints.
package sources

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// bindJSON decodes JSON from the request body into a new value of type T.
// On error it writes a 400 response and returns false.
// This is a local copy of api.BindJSON to avoid an import cycle between
// package sources and package api. DO NOT "clean up" by importing api here.
func bindJSON[T any](c *gin.Context) (T, bool) {
	var req T
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return req, false
	}
	return req, true
}

// apiutil provides a drop-in compat shim for handler_sources_*.go files
// that were migrated from package api (where apiutil was a package-level var)
// into package sources. These functions mirror api/response.go and avoid the
// import cycle. DO NOT import package api here.
var apiutil = struct {
	OK            func(c *gin.Context, data any)
	Error         func(c *gin.Context, status int, msg string)
	BadRequest    func(c *gin.Context, msg string)
	InternalError func(c *gin.Context, err error)
	NotFound      func(c *gin.Context, msg string)
	Accepted      func(c *gin.Context, data any)
}{
	OK: func(c *gin.Context, data any) {
		c.JSON(http.StatusOK, data)
	},
	Error: func(c *gin.Context, status int, msg string) {
		c.JSON(status, gin.H{"ok": false, "error": msg})
	},
	BadRequest: func(c *gin.Context, msg string) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": msg})
	},
	InternalError: func(c *gin.Context, err error) {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
	},
	NotFound: func(c *gin.Context, msg string) {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": msg})
	},
	Accepted: func(c *gin.Context, data any) {
		c.JSON(http.StatusAccepted, data)
	},
}
