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
