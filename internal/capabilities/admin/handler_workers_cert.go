// Package admin hosts system-level HTTP handlers accessible only to
// operators holding a valid admin token. Unlike the per-feature
// handlers in internal/api/assets/, internal/api/script/, etc., this
// surface exposes cross-cutting operational data: worker mTLS
// identities, broker state, cert inventory.
//
// RW-PROD-001 (June 2026): the cert-report endpoint is the canonical
// JSON inventory referenced in the runbook. It returns the mTLS
// identity extracted from the most recent worker registration (or
// the current session, when a session_id filter is supplied).
//
// Mounted under /api/v1/admin/* with RequireAdminToken middleware so a
// stolen worker token can NOT trigger it — admin token only.
package admin

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	tlsload "github.com/Marcuss-ops/PipelineGen/pkg/tlsload"
)

// WorkerStore is the narrow port for reading the current worker
// session row. Satisfied by an adapter over
// internal/platform/sqlite/assets/workernodes_repository.go
// in production; tests stub it.
type WorkerStore interface {
	GetCurrentCertIdentity(ctx context.Context, workerID string) (*CertReport, error)
}

// CertReport is the JSON shape returned by the admin endpoint.
// Mirrors tlsload.Identity but adds session metadata useful for the
// runbook ("scheda finale di certificazione per worker").
type CertReport struct {
	WorkerID      string `json:"worker_id"`
	Hostname      string `json:"hostname,omitempty"`
	WorkerVersion string `json:"worker_version,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	// SessionExpiresAt is RFC3339 UTC to keep the JSON stable across
	// servers with different timezone defaults.
	SessionExpiresAt string `json:"session_expires_at,omitempty"`
	// ServerCertFP is the SHA-256 fingerprint of the running server's
	// own listener cert (lookup table: which CA pair is in use).
	ServerCertFP string `json:"server_cert_fingerprint_sha256,omitempty"`

	// Worker cert fields (filled from the most recent register call).
	CertFingerprintSHA256 string   `json:"cert_fingerprint_sha256,omitempty"`
	CertSerialHex         string   `json:"cert_serial_hex,omitempty"`
	CertSubjectDN         string   `json:"cert_subject_dn,omitempty"`
	CertIssuerDN          string   `json:"cert_issuer_dn,omitempty"`
	CertDNSNames          []string `json:"cert_dns_names,omitempty"`
	CertNotAfter          string   `json:"cert_not_after,omitempty"`
	CertVerifiedAt        string   `json:"cert_verified_at,omitempty"`

	// Capabilities reported by the worker on register.
	Capabilities []string `json:"capabilities,omitempty"`

	// Hardware is the canonical WorkerHardwareStats POJO cached from
	// the worker's most recent heartbeat. nil == no telemetry yet;
	// typical between register and the first heartbeat, and any time a
	// heartbeat arrives without the optional Hardware payload. Drift
	// with impl.go::Stats is resolved by separation of concerns (this
	// endpoint = per-worker telemetry; /jobs/stats = aggregate broker
	// state); both consume the same Go type without cross-projecting.
	Hardware *job.WorkerHardwareStats `json:"hardware,omitempty"`

	// SchemaVersion = 1 (RW-PROD-001 v1, June 2026). Future additions
	// bump this so downstream parsers can branch. The runbook
	// explicitly asks for "Output stabile e versionato" — DO NOT
	// change the schema without bumping this field.
	SchemaVersion int `json:"schema_version"`
}

// CertReportHandler serves GET /api/v1/admin/workers/:id/cert-report.
//
// serverIdentity is a getter returning the parsed TLS identity of the
// running listener. It is a CLOSURE (NOT a snapshot) because the
// listener cert is loaded lazily by server.Start() via prepareTLSConfig
// AFTER this handler is constructed — passing a snapshot here would
// capture a stale nil. Callers should pass `srv.TLSIdentity` (a method
// value of type `func() *tlsload.Identity`). When the getter returns
// nil (TLS disabled / start-up race) the JSON omits
// server_cert_fingerprint_sha256; nil getter = always omit.
type CertReportHandler struct {
	store          WorkerStore
	serverIdentity func() *tlsload.Identity
	log            *zap.Logger
}

// NewCertReportHandler builds the handler with explicit deps. The
// server-side TLS identity getter is optional: pass nil and the JSON
// always omits server_cert_fingerprint_sha256.
func NewCertReportHandler(store WorkerStore, serverIdentity func() *tlsload.Identity, log *zap.Logger) *CertReportHandler {
	return &CertReportHandler{
		store:          store,
		serverIdentity: serverIdentity,
		log:            log,
	}
}

// RegisterRoutes mounts the cert-report handler under the supplied
// router group. Caller is responsible for attaching RequireAdminToken
// middleware to the group, e.g.:
//
//	adminGroup := engine.Group("/api/v1/admin")
//	adminGroup.Use(middleware.RequireAdminToken(r.cfg))
//	adminGroup.GET("/workers/:id/cert-report", h.Report)
func (h *CertReportHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/workers/:id/cert-report", h.Report)
}

// Report handles GET /api/v1/admin/workers/:id/cert-report.
//
// It returns the cert report for the given worker_id; 404 when no
// worker matches the ID. Never echoes the worker's HMAC token, cert
// key, or any non-metadata field — the audit list explicitly bans
// secret leaks in this surface.
func (h *CertReportHandler) Report(c *gin.Context) {
	workerID := c.Param("id")
	if workerID == "" {
		apiutil.BadRequest(c, "missing worker id")
		return
	}

	report, err := h.store.GetCurrentCertIdentity(c.Request.Context(), workerID)
	if err != nil {
		if _, ok := err.(*ErrWorkerNotFound); ok {
			c.JSON(http.StatusNotFound, gin.H{
				"ok":    false,
				"error": "worker not found",
				"id":    workerID,
			})
			return
		}
		h.log.Error("cert-report lookup failed",
			zap.String("worker_id", workerID),
			zap.Error(err),
		)
		apiutil.InternalError(c, err)
		return
	}

	if report.SchemaVersion == 0 {
		report.SchemaVersion = 1
	}
	if h.serverIdentity != nil {
		// Lazy getter (NOT snapshot) — the underlying listener cert is
		// loaded after construction; the getter reads the live value.
		if ident := h.serverIdentity(); ident != nil {
			report.ServerCertFP = ident.FingerprintSHA256
		}
	}
	apiutil.OK(c, report)
}

// ErrWorkerNotFound is returned by WorkerStore implementations when
// the requested worker has no current session. Routes 1:1 to a 404 in
// the handler.
type ErrWorkerNotFound struct {
	WorkerID string
}

func (e *ErrWorkerNotFound) Error() string {
	return "worker not found: " + e.WorkerID
}

// FromSessionCertIdentity builds a CertReport from a WorkerSession +
// WorkerCertIdentity pair plus the requested host/version metadata.
// Helper used by adapter implementations of WorkerStore — production
// repositories persist cert metadata in a separate row read alongside
// the session row.
//
// PR-0 (June 2026) split: cert fields (CertFingerprintSHA256,
// CertSerialHex, CertSubjectDN, CertIssuerDN, CertDNSNames,
// CertNotAfter, CertVerifiedAt) no longer pollute WorkerSession and
// no longer replicate via runtime reflection from the canonical
// WorkerSession struct. The cert identity flows in as a typed
// *appjobs.WorkerCertIdentity argument, nil-tolerant (when nil the
// CertReport still renders with session-only fields, matching the
// pre-split wire shape). The handler reads cert from a SIDECAR
// repository row independently, keeping WorkerSession narrowly
// scoped to session concerns (WorkerID, SessionID, SessionExpiresAt,
// Capabilities, Version, Hostname).
func FromSessionCertIdentity(s *appjobs.WorkerSession, cert *appjobs.WorkerCertIdentity, hostname, workerVersion string, capabilityTypes []string) *CertReport {
	if s == nil {
		return &CertReport{SchemaVersion: 1, Capabilities: capabilityTypes}
	}
	r := &CertReport{
		WorkerID:         s.WorkerID,
		Hostname:         hostname,
		WorkerVersion:    workerVersion,
		SessionID:        s.SessionID,
		SessionExpiresAt: s.SessionExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		SchemaVersion:    1,
		Capabilities:     capabilityTypes,
		Hardware:         s.Hardware,
	}
	if cert != nil {
		r.CertFingerprintSHA256 = cert.FingerprintSHA256
		r.CertSerialHex = cert.SerialHex
		r.CertSubjectDN = cert.SubjectDN
		r.CertIssuerDN = cert.IssuerDN
		r.CertDNSNames = cert.DNSNames
		if !cert.NotAfter.IsZero() {
			r.CertNotAfter = cert.NotAfter.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		if !cert.VerifiedAt.IsZero() {
			r.CertVerifiedAt = cert.VerifiedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
	}
	return r
}
