package api

import (
	"encoding/json"

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

// ParseJSON decodes raw JSON bytes into a map. Returns nil on error.
func ParseJSON(data []byte) map[string]any {
	var m map[string]any
	if len(data) == 0 {
		return nil
	}
	_ = json.Unmarshal(data, &m)
	return m
}
