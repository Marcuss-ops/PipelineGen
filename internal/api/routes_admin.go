package api

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/web"
)

// registerAdminUIRoutes serves the embedded admin SPA on /admin and
// installs the engine-level NoRoute fallback for client-side routing.
// The SPA itself is public so the browser can load the login page; the
// API routes remain protected by RequireAdminToken / Auth. The static
// assets are embedded at build time via web.DistFS().
func (r *Router) registerAdminUIRoutes(engine *gin.Engine) {
	// Serve admin UI static files on /admin. The SPA itself is public so
	// that the browser can load the login page; the API routes remain
	// protected by RequireAdminToken / Auth. The static assets are
	// embedded at build time via web.DistFS().
	adminUIFS := web.DistFS()
	adminUIGroup := engine.Group("/admin")
	{
		adminUIGroup.StaticFS("/", http.FS(adminUIFS))
	}
	engine.NoRoute(func(c *gin.Context) {
		// RouterGroup has no NoRoute hook in Gin. Serve the SPA fallback
		// for any unknown path under /admin so react-router can handle
		// client-side routing.
		if strings.HasPrefix(c.Request.URL.Path, "/admin/") || c.Request.URL.Path == "/admin" {
			serveAdminUISPA(c, adminUIFS)
			return
		}
		c.Status(http.StatusNotFound)
	})
}

// registerAdminAuthRoutes wires the /api/admin/auth surface for the
// React SPA. Login/logout are intentionally public; /me is protected by
// RequireAdminToken so the frontend can verify its session cookie.
func (r *Router) registerAdminAuthRoutes(api *gin.RouterGroup) {
	// Admin authentication surface for the React SPA. Login/logout are
	// intentionally public; /me is protected by RequireAdminToken so the
	// frontend can verify its session cookie.
	adminAuth := api.Group("/admin/auth")
	{
		secureCookie := r.cfg.ServerGinMode == gin.ReleaseMode

		adminAuth.POST("/login", func(c *gin.Context) {
			var req struct {
				Token string `json:"token"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request body"})
				return
			}
			if r.cfg.Auth == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "auth not configured"})
				return
			}
			if !middleware.CompareTokens(req.Token, r.cfg.Auth.AdminToken()) {
				c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "invalid token"})
				return
			}
			c.SetSameSite(http.SameSiteLaxMode)
			c.SetCookie(
				"velox_admin_session",
				req.Token,
				86400,
				"/",
				"",
				secureCookie,
				true,
			)
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})

		adminAuth.POST("/logout", func(c *gin.Context) {
			c.SetSameSite(http.SameSiteLaxMode)
			c.SetCookie(
				"velox_admin_session",
				"",
				-1,
				"/",
				"",
				secureCookie,
				true,
			)
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})

		adminAuth.GET("/me", middleware.RequireAdminToken(r.cfg.Auth, r.cfg.Log), func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true, "role": "admin"})
		})
	}
}

// serveAdminUISPA serves the embedded index.html for SPA fallback.
func serveAdminUISPA(c *gin.Context, fsys fs.FS) {
	file, err := fsys.Open("index.html")
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	// http.ServeContent sets Content-Type, handles Range requests,
	// and respects If-Modified-Since headers. fs.File is not guaranteed
	// to implement io.ReadSeeker, so serve an in-memory reader.
	http.ServeContent(c.Writer, c.Request, "index.html", stat.ModTime(), bytes.NewReader(data))
}
