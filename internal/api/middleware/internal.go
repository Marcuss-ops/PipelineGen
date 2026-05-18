package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/pkg/apiutil"
)

// RequireInternalHeader returns a middleware that checks for X-Internal or X-Velox-Internal header.
// Internal endpoints should use this middleware to prevent external access.
func RequireInternalHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Allow if internal header is present
		if strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Internal")), "true") ||
			strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Velox-Internal")), "true") {
			c.Next()
			return
		}

		// Allow if already authenticated as admin or worker
		isAdmin, _ := c.Get("is_admin")
		isWorker, _ := c.Get("is_worker")
		if isAdmin == true || isWorker == true {
			c.Next()
			return
		}

		apiutil.Error(c, http.StatusForbidden, "internal endpoint")
		c.Abort()
	}
}
