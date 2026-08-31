// Package script (api/script) — Auth middleware cluster
// (PR-SCRIPT-AUTH-EXTRACT, July 2026).
//
// Extracted per architecture/current.yaml#SCRIPT-FLOW-SPLIT.linked_issues[PR-SCRIPT-AUTH-EXTRACT]
// (AGENTS.md Pattern 5 + godlike/07 minimum-blast-radius). This file owns
// the 3-element auth cluster: AdminTokenProvider interface +
// RequireAdminToken free func + extractHeaderToken free func.
//
// godlike/07 minimum-blast-radius: the AdminTokenProvider interface
// stays in package script (NOT moved to internal/platform/httpserver/middleware/) so
// ScriptFlowHandler continues to satisfy the port structurally — moving
// it out of the package would break the downstream surface contract
// because every handler that wants the auth middleware is passed in as
// an AdminTokenProvider without an intermediate adapter struct.
//
// godlike/06 SSOT: this file is the canonical SOLE owner of the auth
// cluster; handler_flow.go retains registerJobRoutes (which calls
// RequireAdminToken(h)) and the EnableAuth/AdminToken methods on
// ScriptFlowHandler (which satisfy the AdminTokenProvider interface).

package script

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ── Local AdminTokenProvider port ──────────────────────────────────────────
//
// Two-method interface consumed by RequireAdminToken. The canonical
// concrete is internal/platform/httpserver/middleware.TokenSecurityAdapter;
// ScriptFlowHandler itself satisfies the port structurally so it can
// be passed in without an intermediate adapter struct.
type AdminTokenProvider interface {
	EnableAuth() bool
	AdminToken() string
}

// RequireAdminToken wraps middleware.RequireAdminToken accepting the local
// AdminTokenProvider interface instead of the dense configuration struct.
func RequireAdminToken(cfg AdminTokenProvider) gin.HandlerFunc {
	// Delegate to the canonical middleware via an adapter that bridges
	// AdminTokenProvider → adapter fields.
	return func(c *gin.Context) {
		if cfg == nil || !cfg.EnableAuth() {
			c.Set("is_admin", true)
			c.Next()
			return
		}
		expected := strings.TrimSpace(cfg.AdminToken())
		if expected == "" {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "RequireAdminToken misconfigured (VELOX_ADMIN_TOKEN is empty)",
			})
			c.Abort()
			return
		}
		// Read token from X-Velox-Admin-Token or Authorization: Bearer header
		provided := extractHeaderToken(c)
		if provided == expected {
			c.Set("is_admin", true)
			c.Next()
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "admin token required",
		})
		c.Abort()
	}
}

// extractHeaderToken reads the admin token from request headers,
// preferring X-Velox-Admin-Token and falling back to Authorization:
// Bearer. Helper for RequireAdminToken; lowercase = unexported,
// intra-package only.
func extractHeaderToken(c *gin.Context) string {
	tok := strings.TrimSpace(c.GetHeader("X-Velox-Admin-Token"))
	if tok != "" {
		return tok
	}
	bearer := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	return strings.TrimSpace(bearer)
}

// Compile-time assertion (godlike/06 SSOT + AGENTS.md Pattern 0):
// Lock the structural satisfaction contract that *ScriptFlowHandler
// satisfies AdminTokenProvider. If a future agent removes EnableAuth
// or AdminToken methods from ScriptFlowHandler, the build fails
// immediately rather than producing a runtime panic at first auth
// call (godlike/07 minimum-blast-radius fail-fast-at-boot contract).
var _ AdminTokenProvider = (*ScriptFlowHandler)(nil)
