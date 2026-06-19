package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireServiceToken returns a middleware that permits only authenticated
// admin or worker principals. It replaces the spoofable X-Internal header
// check with a proper token-based verification (already done by Auth middleware).
// Internal endpoints should use this middleware AFTER the standard Auth middleware.
func RequireServiceToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		isAdmin, _ := c.Get("is_admin")
		isWorker, _ := c.Get("is_worker")
		if isAdmin == true || isWorker == true {
			c.Next()
			return
		}

		apiutil.Error(c, http.StatusForbidden, "internal endpoint: admin or worker token required")
		c.Abort()
	}
}

// RequireAdmin returns a middleware that permits only authenticated admin principals.
// Worker tokens are explicitly rejected to prevent privilege collapse.
// Use this for destructive or sensitive operations (delete, config change, etc.).
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		isAdmin, _ := c.Get("is_admin")
		if isAdmin == true {
			c.Next()
			return
		}

		apiutil.Error(c, http.StatusForbidden, "admin token required")
		c.Abort()
	}
}
