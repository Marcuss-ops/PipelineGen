package middleware

import (
	"net/http"
	"strings"

	mw "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ── M2M (machine-to-machine) client auth ────────────────────────────
//
// jobClientAuthMiddleware is the M2M counterpart of RequireAdminToken
// / WorkerAuth. It gates the public job surface (POST /api/v1/jobs,
// GET /api/v1/jobs/:id) on a per-client secret model: a remote computer
// (PipelineGen / Agent / second PC) submits a Bearer VELOX_M2M_SECRET;
// the Master resolves it to a registered m2m_clients row
// (client_id, scopes, quota, rate limit) and authorizes the specific
// scope each route requires (jobs.submit / jobs.read).
//
// Threat model + the deliberate split from Auth():
//
//   - Auth() accepts BOTH admin and worker tokens on /api/* (the
//     single shared-secret model). That is fine for an operator-driven
//     dashboard but WRONG for a second computer that should NOT know
//     VELOX_ADMIN_TOKEN (admin grants) or VELOX_WORKER_TOKEN (worker
//     broker internals). A leaked M2M secret must NOT let the remote
//     call /api/v1/admin/* or POST /internal/v1/jobs/:id/complete.
//
//   - WorkerAuth accepts ONLY worker tokens on /internal/v1/* and
//     explicitly REJECTS admin tokens (defense in depth). The M2M
//     secret is a THIRD principal, distinct from both: the remote
//     computer is neither an operator nor a worker — it is a job
//     submitter. Its secret is per-client, revocable, and scoped.
//
//   - The plaintext secret is NEVER stored. The Master persists only
//     SHA-256(secret) (m2m_clients.secret_hash). The runtime path
//     hashes the inbound Bearer and looks the digest up via the port —
//     so a DB read cannot leak the secret even to an admin with direct
//     SQLite access. The hash primitive is owned by M2MSecurityPort so
//     the key-creation admin endpoint and the runtime auth path agree.
//
// Failure modes (mirror admin_token.go / WorkerAuth):
//
//   - sec == nil OR EnableM2M() == false → pass-through (set is_admin
//     so downstream handlers treat the request as admin-context —
//     dev/test/E2E fixtures that have not provisioned an m2m_clients
//     row still work, matching Auth()'s EnableAuth bypass).
//
//   - No Bearer token in the request → 401 (no credential provided).
//
//   - LookupClient returns (nil, nil) → 401 (no matching client row —
//     either the secret is wrong or the client_id was never created).
//     Constant-time is NOT applied here because the lookup is by hash,
//     not by byte-compare against a configured secret; a missing row is
//     the same cost as a wrong-secret row, so there is no timing oracle.
//
//   - LookupClient returns a non-nil error → 500 (store unreachable;
//     fail-closed rather than 401, which would imply the secret was
//     wrong when the real cause is a DB outage).
//
//   - Client found but Enabled == false → 403 (the secret is correct
//     but the operator has disabled the client; 403, not 401, so the
//     client knows its credential is valid and the revocation is
//     administrative).
//
// SECURITY: never logs the secret value, the Bearer header, or the
// secret_hash; only the client_id (a non-secret identifier) and bool
// has_credential. The hash is also never logged — it is a request
// fingerprint.

// jobClientContextKey is the gin.Context key under which the resolved
// M2MClient (client_id + scopes) is stored for downstream requireScope
// checks and the future idempotency-key uniqueness constraint.
const jobClientContextKey = "m2m_client"

// jobClientAuthMiddleware returns a gin middleware that enforces M2M
// client-secret auth on the /api/v1/jobs surface. It resolves the
// Bearer VELOX_M2M_SECRET to a registered client row and stores the
// resolved *M2MClient in the gin context for the per-route requireScope
// gate. Distinct from Auth() (shared admin/worker) and WorkerAuth
// (worker-only): the M2M principal is a third, scoped submitter.
//
// PG-M2M (Aug 2026): the middleware takes M2MSecurityPort + *zap.Logger
// at registration time — zero infra imports, matching the PG-006
// typed-port convention for AuthSecurityPort.
func jobClientAuthMiddleware(sec mw.M2MSecurityPort, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sec == nil || !sec.EnableM2M() {
			// Pass-through for dev/test/E2E without a provisioned
			// m2m_clients row. Matches Auth()'s EnableAuth bypass so
			// existing fixtures keep working until M2M is wired.
			c.Set("is_admin", true)
			c.Next()
			return
		}

		token := extractBearerToken(c)

		if log != nil {
			log.Info("jobClientAuthMiddleware check",
				zap.String("path", c.Request.URL.Path),
				zap.Bool("has_credential", token != ""))
		}

		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"ok":    false,
				"error": "m2m bearer token required",
			})
			c.Abort()
			return
		}

		// Hash the inbound secret via the canonical SSOT so the digest
		// shape matches the stored secret_hash column exactly.
		secretHash := sec.HashClientSecret(token)

		client, err := sec.LookupClient(c.Request.Context(), secretHash)
		if err != nil {
			if log != nil {
				log.Error("jobClientAuthMiddleware store unavailable",
					zap.String("path", c.Request.URL.Path),
					zap.Error(err))
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "m2m client store unavailable",
			})
			c.Abort()
			return
		}
		if client == nil {
			if log != nil {
				log.Warn("jobClientAuthMiddleware rejected request (no matching client)",
					zap.String("path", c.Request.URL.Path),
					zap.String("client_ip", c.ClientIP()))
			}
			c.JSON(http.StatusUnauthorized, gin.H{
				"ok":    false,
				"error": "invalid m2m credentials",
			})
			c.Abort()
			return
		}
		if !client.Enabled {
			if log != nil {
				log.Warn("jobClientAuthMiddleware rejected request (client disabled)",
					zap.String("path", c.Request.URL.Path),
					zap.String("client_id", client.ClientID))
			}
			c.JSON(http.StatusForbidden, gin.H{
				"ok":    false,
				"error": "m2m client is disabled",
			})
			c.Abort()
			return
		}

		// Store the resolved client for downstream requireScope + the
		// future (client_id, idempotency_key) UNIQUE constraint. The
		// plaintext token is deliberately NOT stored — only the
		// non-secret client_id projection.
		c.Set(jobClientContextKey, client)
		c.Next()
	}
}

// requireScope returns a gin middleware that enforces that the resolved
// M2M client was granted the named scope. It MUST run AFTER
// jobClientAuthMiddleware (it reads the *M2MClient the auth middleware
// stored in the context). Mount per-route:
//
//	jobsAPI.POST("", requireScope("jobs.submit"), createJob)
//	jobsAPI.GET("/:id", requireScope("jobs.read"), getJob)
//
// Failure modes:
//   - no *M2MClient in context (auth middleware did not run, or ran in
//     pass-through mode) → 500 (mis-wired route chain; the auth
//     middleware MUST precede requireScope).
//   - client lacks the scope → 403 (the credential is valid but the
//     granted scopes do not cover this action; 403, not 401, so the
//     client can distinguish a missing scope from a wrong secret).
//
// Pass-through when EnableM2M()==false is handled by the auth
// middleware (it sets is_admin and skips storing a client); in that
// case requireScope finds no client and would 500 — so the auth
// middleware instead stores the client only when it actually resolved
// one. To keep requireScope safe under pass-through too, it treats a
// missing client AND is_admin==true as pass-through (dev/E2E).
func requireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, exists := c.Get(jobClientContextKey)
		if !exists || raw == nil {
			// Dev/test/E2E pass-through: auth middleware ran in
			// EnableM2M()==false mode and set is_admin without storing
			// a client. Allow through so fixtures without a provisioned
			// m2m_clients row keep working.
			if admin, _ := c.Get("is_admin"); admin == true {
				c.Next()
				return
			}
			// Production mis-wire: requireScope mounted without a
			// preceding jobClientAuthMiddleware. Fail-closed 500.
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "m2m scope check mounted without jobClientAuthMiddleware",
			})
			c.Abort()
			return
		}
		client, ok := raw.(*mw.M2MClient)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "m2m context value is not an *M2MClient",
			})
			c.Abort()
			return
		}
		if !client.HasScope(scope) {
			c.JSON(http.StatusForbidden, gin.H{
				"ok":    false,
				"error": "missing required scope: " + scope,
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// extractBearerToken pulls the M2M secret from the Authorization
// header. It accepts ONLY the Bearer scheme (the M2M surface does not
// share the X-Velox-Admin-Token channel, by design — that header is
// admin-only and a remote submitter must not reuse it). Empty on
// missing/malformed header.
//
// Mirrors extractAuthToken's Bearer branch but deliberately drops the
// X-Velox-Admin-Token fallback so an admin token cannot be replayed
// on the M2M surface via header confusion.
func extractBearerToken(c *gin.Context) string {
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if authHeader == "" {
		return ""
	}
	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(authHeader, bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authHeader, bearerPrefix))
}

// JobClientAuthMiddleware is the exported factory so the composition
// root (internal/app/wiring) can mount the M2M guard on the /api/v1/jobs
// group without the package-internal lower-case name leaking. The
// signature mirrors RequireAdminToken / WorkerAuth (port + logger).
func JobClientAuthMiddleware(sec mw.M2MSecurityPort, log *zap.Logger) gin.HandlerFunc {
	return jobClientAuthMiddleware(sec, log)
}

// RequireScope is the exported factory counterpart for per-route
// scope gating. Mirrors the unexported requireScope; exported so the
// wiring layer can name it in a RouterGroup.POST/GET chain.
func RequireScope(scope string) gin.HandlerFunc {
	return requireScope(scope)
}
