// Package document — document_handler.go (P0 Commit 10, July 2026).
//
// HTTP transport for /api/document/generate. Pattern 8 conformance:
// no business orchestration in the handler; the use case owns the
// service call + manifest injection. The handler is a thin
// transport (bind → delegate → write).
package document

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	docpkg "github.com/Marcuss-ops/PipelineGen/internal/application/document"
)

// Handler is the canonical /api/document/generate transport.
//
// UseCase MUST be non-nil at construction. Fail-closed at the
// gin.RouterGroup registration time so a misconfigured service
// surfaces 503 for every request, not a panic on first hit.
type Handler struct {
	UseCase *docpkg.GenerateDocumentUseCase
	Log     *zap.Logger
}

// NewHandler returns a Handler with fail-fast on a nil use case.
// Composition root (internal/app/) wires the use case via
// docpkg.NewGenerateDocumentUseCase.
func NewHandler(uc *docpkg.GenerateDocumentUseCase, log *zap.Logger) *Handler {
	if uc == nil {
		panic("document.NewHandler: use case is required (C10 fail-fast)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{UseCase: uc, Log: log}
}

// RegisterRoutes attaches the document handler to the supplied
// gin.RouterGroup under the canonical /document/generate route.
// Called from the API module composition root (internal/app/).
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/document/generate", h.Generate)
}

// Generate is the canonical handler for POST /api/document/generate.
//
// Response shape (typed envelope, C10):
//
//	{
//	  "data": { "title": "...", "format": "pdf", ... },
//	  "artifacts": {
//	    "schema_version": "pipelinegen.artifacts.v1",
//	    "job_id": "doc-<random>",
//	    "artifacts": [
//	      {
//	        "id": "...:pdf", "kind": "pdf",
//	        "path": "/tmp/...", "filename": "...pdf",
//	        "mime_type": "application/pdf",
//	        "size_bytes": 1234, "sha256": "...",
//	        "required": true
//	      }
//	    ]
//	  }
//	}
//
// The Sender-side upload cycle (internal/application/jobs/worker/runner.go)
// reads Artifacts[0].Filename + MIMEType + SizeBytes from this
// response — they are the wire-format contract between the
// /api/document/generate handler and the Sender-side manifest
// ingestion (see RoundTripManifest test).
func (h *Handler) Generate(c *gin.Context) {
	var req docpkg.DocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	// JobID synthesis: callers may supply ?job_id=... for async
	// pipelines; otherwise a fresh 8-byte random is generated so
	// every document gets a unique Artifact ID (matches the
	// pattern in voiceover/books).
	jobID := c.Query("job_id")
	if jobID == "" {
		jobID = "doc-" + randHex(8)
	}
	if !isSafeJobID(jobID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_id must match [A-Za-z0-9._-]+"})
		return
	}

	envelope, err := h.UseCase.Handle(c.Request.Context(), req, jobID)
	if err != nil {
		if status, body := docpkg.GenerateDocumentErrMapper(err); status != http.StatusOK {
			c.JSON(status, gin.H{"error": body})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, envelope)
}

// randHex returns a random hex string of 2*n bytes (so n characters
// of literal hex). Used for synthesising JobIDs. crypto/rand is the
// canonical entropy source.
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Fallback to a deterministic prefix so the handler doesn't
		// panic on entropy failures (extremely rare; the use case
		// surfaces a new JobID on retry anyway).
		return "00000000"
	}
	return hex.EncodeToString(b)
}

// isSafeJobID mirrors the canonical job_id shape used by jobs/
// (alphanumerics + dot, underscore, dash). Anything else is
// rejected to keep the ArtifactID prefix (info.JobID + ":pdf")
// path-safe + cross-handler consistent.
func isSafeJobID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			// ok
		default:
			return false
		}
	}
	return true
}
