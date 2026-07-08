// Package clips — handler_delegators.go contains the thin delegator methods
// that forward to the Search/Ingest/Ops sub-handlers, plus the Action
// cluster helper methods (driveRootForSource, idemWriter).
//
// NonOps methods (BulkAddTags, BulkRemoveTags, RegisterJobHandlers) +
// helper methods (driveRootForSource, RegisterJobHandlers, idemWriter) used
// to live here; per PR-CLIPS-NONOPS-EXTRACT (July 2026) the 9 NonOps
// methods + the 1 BulkTag helper were extracted to the nonops sub-package
// (internal/api/assets/clips/nonops). This file now hosts ONLY the thin
// sub-handler delegators (CreateClip / UpdateClip / UploadVideoClip /
// VerifyClip / HandleFixHash / TrashClip / DeleteClip / Reconcile /
// Cleanup / ListFolders / FolderStatus / RegenerateManifest /
// TrashFolder / DeleteFolder / GetFolderChildren / GetTree /
// GetBreadcrumb) + driveRootForSource + idemWriter.
//
// Extracted from handler.go (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #7).
package clips

import (
	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ── Thin delegators (Split 2, June 2026): Ingest sub-handler ──────────

// CreateClip thin-delegates to IngestHandler.CreateClip.
func (h *Handler) CreateClip(c *gin.Context) {
	if h.ingest == nil {
		apiutil.Error(c, 503, "ingest sub-handler not wired")
		return
	}
	h.ingest.CreateClip(c)
}

// UpdateClip thin-delegates to IngestHandler.UpdateClip.
func (h *Handler) UpdateClip(c *gin.Context) {
	if h.ingest == nil {
		apiutil.Error(c, 503, "ingest sub-handler not wired")
		return
	}
	h.ingest.UpdateClip(c)
}

// UploadVideoClip thin-delegates to IngestHandler.UploadVideoClip.
func (h *Handler) UploadVideoClip(c *gin.Context) {
	if h.ingest == nil {
		apiutil.Error(c, 503, "ingest sub-handler not wired")
		return
	}
	h.ingest.UploadVideoClip(c)
}

// ── Thin delegators (Step 5 Split 2, June 2026): Ops sub-handler ──────

// VerifyClip thin-delegates to OpsHandler.VerifyClip.
func (h *Handler) VerifyClip(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.VerifyClip(c)
}

// HandleFixHash thin-delegates to OpsHandler.HandleFixHash.
func (h *Handler) HandleFixHash(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.HandleFixHash(c)
}

// TrashClip thin-delegates to OpsHandler.TrashClip.
func (h *Handler) TrashClip(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.TrashClip(c)
}

// DeleteClip thin-delegates to OpsHandler.DeleteClip.
func (h *Handler) DeleteClip(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.DeleteClip(c)
}

// Reconcile thin-delegates to OpsHandler.Reconcile.
func (h *Handler) Reconcile(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.Reconcile(c)
}

// Cleanup thin-delegates to OpsHandler.Cleanup.
func (h *Handler) Cleanup(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.Cleanup(c)
}

// ListFolders thin-delegates to OpsHandler.ListFolders.
func (h *Handler) ListFolders(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.ListFolders(c)
}

// FolderStatus thin-delegates to OpsHandler.FolderStatus.
func (h *Handler) FolderStatus(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.FolderStatus(c)
}

// RegenerateManifest thin-delegates to OpsHandler.RegenerateManifest.
func (h *Handler) RegenerateManifest(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.RegenerateManifest(c)
}

// TrashFolder thin-delegates to OpsHandler.TrashFolder.
func (h *Handler) TrashFolder(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.TrashFolder(c)
}

// DeleteFolder thin-delegates to OpsHandler.DeleteFolder.
func (h *Handler) DeleteFolder(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.DeleteFolder(c)
}

// GetFolderChildren thin-delegates to OpsHandler.GetFolderChildren.
func (h *Handler) GetFolderChildren(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.GetFolderChildren(c)
}

// GetTree thin-delegates to OpsHandler.GetTree.
func (h *Handler) GetTree(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.GetTree(c)
}

// GetBreadcrumb thin-delegates to OpsHandler.GetBreadcrumb.
func (h *Handler) GetBreadcrumb(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.GetBreadcrumb(c)
}

// ── Helper methods ────────────────────────────────────────────────────

// driveRootForSource returns the Drive root folder for a clip source
// along with the URL marker the source-checker uses. Used by Action
// cluster methods (DownloadClip / ReuploadClip).
func (h *Handler) driveRootForSource(source string) (string, string) {
	if h.cfg == nil {
		return "", ""
	}
	canonical := artifacts.CanonicalSource(source)
	switch canonical {
	case "clips", "youtube":
		return h.cfg.Drive.ClipsFolder(), "/clips/"
	case "artlist":
		return h.cfg.Drive.ArtlistFolder(), "/artlist/"
	case "stock":
		return h.cfg.Drive.StockFolder(), "/stock/"
	default:
		return "", ""
	}
}

// idemWriter returns h.Idempotency if set, else a no-op pass-through handler.
func (h *Handler) idemWriter() gin.HandlerFunc {
	if h.Idempotency == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return h.Idempotency
}
