package apiutil

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func BindJSON[T any](c *gin.Context) (T, bool) {
	var req T
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return req, false
	}
	return req, true
}
