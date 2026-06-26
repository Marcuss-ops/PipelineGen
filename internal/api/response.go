package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, data)
}

func Accepted(c *gin.Context, data any) {
	c.JSON(http.StatusAccepted, data)
}

func BadRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": msg})
}

// InternalError is an alias to the canonical apiutil.InternalError. Callers
// inside internal/api/ can use this package-level name without importing apiutil
// themselves. Duplicate removed in favour of pkg/apiutil (AGENTS.md Pattern 5).
var InternalError = apiutil.InternalError

func NotFound(c *gin.Context, msg string) {
	c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": msg})
}

func Error(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"ok": false, "error": msg})
}
