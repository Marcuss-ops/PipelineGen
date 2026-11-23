package httpserver

// routes_m2m_admin.go owns the admin key-minting surface for M2M clients:
//
//	POST   /api/v1/admin/m2m/keys
//	GET    /api/v1/admin/m2m/keys
//	DELETE /api/v1/admin/m2m/keys?client_id=...   (revoke: enabled=0)
//	DELETE /api/v1/admin/m2m/keys/:client_id
//
// It is mounted inside the admin-auth group (RequireAdminToken), NOT on the
// /api/v1/jobs or /api/v1/media M2M groups: a scoped submitter must never be
// able to mint or revoke keys. The concrete store is reached through the
// optional M2MKeyAdminPort so a deployment without an M2M store keeps booting
// (the routes simply are not registered).

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	mwmw "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

var m2mClientIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// knownM2MScopes is the closed grant set. An unknown scope is a 400 because it
// would be a silent no-op grant (nothing checks it).
var knownM2MScopes = map[string]bool{
	"jobs.submit": true,
	"jobs.read":   true,
	"jobs.cancel": true,
	"media.read":  true,
}

type createM2MKeyRequest struct {
	ClientID          string   `json:"client_id"`
	Description       string   `json:"description,omitempty"`
	Scopes            []string `json:"scopes"`
	RateLimitRPS      float64  `json:"rate_limit_rps,omitempty"`
	RateLimitBurst    int      `json:"rate_limit_burst,omitempty"`
	QuotaMaxScenes    int64    `json:"quota_max_scenes,omitempty"`
	QuotaMaxTotalSecs int64    `json:"quota_max_total_secs,omitempty"`
}

// registerM2MKeyAdminRoutes mounts the three admin routes on the supplied
// group (already rooted at /api/v1/admin/m2m and admin-authenticated).
func registerM2MKeyAdminRoutes(rg *gin.RouterGroup, store mwmw.M2MKeyAdminPort, log *zap.Logger) {
	if rg == nil || store == nil {
		return
	}
	rg.POST("/keys", func(c *gin.Context) {
		var req createM2MKeyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apiutil.BadRequest(c, "invalid JSON body: "+err.Error())
			return
		}
		req.ClientID = strings.TrimSpace(req.ClientID)
		if !m2mClientIDPattern.MatchString(req.ClientID) {
			apiutil.BadRequest(c, "client_id must match ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
			return
		}
		scopes := make([]string, 0, len(req.Scopes))
		seen := make(map[string]bool, len(req.Scopes))
		for _, raw := range req.Scopes {
			scope := strings.TrimSpace(raw)
			if scope == "" {
				apiutil.BadRequest(c, "scopes contains an empty entry")
				return
			}
			if !knownM2MScopes[scope] {
				apiutil.BadRequest(c, "unknown scope: "+scope)
				return
			}
			if !seen[scope] {
				seen[scope] = true
				scopes = append(scopes, scope)
			}
		}
		if len(scopes) == 0 {
			apiutil.BadRequest(c, "scopes is required")
			return
		}

		client, secret, err := store.CreateClient(c.Request.Context(), mwmw.M2MKeyCreateParams{
			ClientID:          req.ClientID,
			Description:       req.Description,
			Scopes:            scopes,
			RateLimitRPS:      req.RateLimitRPS,
			RateLimitBurst:    req.RateLimitBurst,
			QuotaMaxScenes:    req.QuotaMaxScenes,
			QuotaMaxTotalSecs: req.QuotaMaxTotalSecs,
		})
		if err != nil {
			switch {
			case errors.Is(err, mwmw.ErrM2MClientExists):
				apiutil.Error(c, http.StatusConflict, "m2m client already exists; revoke it or rotate the secret out of band")
			default:
				if log != nil {
					log.Error("m2m admin: create client failed", zap.String("client_id", req.ClientID), zap.Error(err))
				}
				apiutil.InternalError(c, err)
			}
			return
		}
		// The plaintext secret is returned exactly once. Never log it.
		c.JSON(http.StatusCreated, gin.H{
			"ok": true,
			"client": gin.H{
				"client_id":   client.ClientID,
				"scopes":      client.Scopes,
				"enabled":     client.Enabled,
				"description": req.Description,
			},
			"secret": secret,
			"note":   "store the secret now; it is not recoverable",
		})
	})

	rg.GET("/keys", func(c *gin.Context) {
		clients, err := store.ListClients(c.Request.Context())
		if err != nil {
			if log != nil {
				log.Error("m2m admin: list clients failed", zap.Error(err))
			}
			apiutil.InternalError(c, err)
			return
		}
		apiutil.OK(c, gin.H{"clients": clients})
	})

	revoke := func(c *gin.Context) {
		clientID := strings.TrimSpace(c.Param("client_id"))
		if clientID == "" {
			clientID = strings.TrimSpace(c.Query("client_id"))
		}
		if clientID == "" {
			apiutil.BadRequest(c, "client_id is required")
			return
		}
		if err := store.RevokeClient(c.Request.Context(), clientID); err != nil {
			if errors.Is(err, mwmw.ErrM2MClientNotFound) {
				apiutil.NotFound(c, "m2m client not found")
				return
			}
			if log != nil {
				log.Error("m2m admin: revoke client failed", zap.String("client_id", clientID), zap.Error(err))
			}
			apiutil.InternalError(c, err)
			return
		}
		apiutil.OK(c, gin.H{"client_id": clientID, "enabled": false})
	}
	rg.DELETE("/keys", revoke)
	rg.DELETE("/keys/:client_id", revoke)
}
