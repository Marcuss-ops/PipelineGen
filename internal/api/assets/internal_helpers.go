// Package internal contains shared HTTP helpers used by all api/sources/
// subpackages (youtube, artlist, clips, root) and the legacy sources
// root. Lives here (rather than in the sources root or pkg/apiutil) to:
//  1. Avoid the import cycle that pkg/apiutil would create if any
//     sources subpackage tried to import the api root package.
//  2. Let each subpackage import the same binding helpers from one
//     canonical location, instead of duplicating bindJSON/apiutil
//     across each subpackage's own helpers.go.
//
// The legacy sources/helpers.go keeps duplicate definitions for
// callers that still use the package sources directly; new handlers
// should prefer this internal package.
package assets

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// BindJSON decodes JSON from the request body into a new value of type T.
// On error it writes a 400 response and returns false.
//
// Generic-local copy (instead of importing pkg/apiutil) to bridge the
// import-cycle concerns described at the package top.
func BindJSON[T any](c *gin.Context) (T, bool) {
	var req T
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return req, false
	}
	return req, true
}

// APIUtil is a drop-in compat shim that mirrors the apiutil var from
// the legacy sources/helpers.go. Subpackages can call it via e.g.
// internal.APIUtil.OK(c, data) without importing the api root package.
var APIUtil = struct {
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
