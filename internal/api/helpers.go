package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ClampLimit clamps a value to [def, max]; returns def if v <= 0 or v > max.
func ClampLimit(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// BindJSONInto binds JSON into an existing pointer target. Returns false on error.
// This is the non-generic variant used by the apiutil compat shim.
func BindJSONInto(c *gin.Context, obj any) bool {
	if err := c.ShouldBindJSON(obj); err != nil {
		BadRequest(c, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// BindJSON decodes JSON from the request body into a new value of type T.
// On error it writes a 400 response and returns false.
func BindJSON[T any](c *gin.Context) (T, bool) {
	var req T
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return req, false
	}
	return req, true
}
