// Package clips contains compatibility delegators from the aggregate Handler
// to the focused HTTP handlers. Business dependencies remain on those focused
// handlers, never on the aggregate.
package clips

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

func (h *Handler) CreateClip(c *gin.Context) {
	if h.ingest == nil {
		apiutil.Error(c, 503, "ingest sub-handler not wired")
		return
	}
	h.ingest.CreateClip(c)
}

func (h *Handler) UpdateClip(c *gin.Context) {
	if h.ingest == nil {
		apiutil.Error(c, 503, "ingest sub-handler not wired")
		return
	}
	h.ingest.UpdateClip(c)
}

func (h *Handler) UploadVideoClip(c *gin.Context) {
	if h.ingest == nil {
		apiutil.Error(c, 503, "ingest sub-handler not wired")
		return
	}
	h.ingest.UploadVideoClip(c)
}

func (h *Handler) CreateAIStockClip(c *gin.Context) {
	if h.ingest == nil {
		apiutil.Error(c, 503, "ingest sub-handler not wired")
		return
	}
	h.ingest.CreateAIStockClip(c)
}

func (h *Handler) VerifyClip(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.VerifyClip(c)
}

func (h *Handler) HandleFixHash(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.HandleFixHash(c)
}

func (h *Handler) TrashClip(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.TrashClip(c)
}

func (h *Handler) DeleteClip(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.DeleteClip(c)
}

func (h *Handler) Reconcile(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.Reconcile(c)
}

func (h *Handler) Cleanup(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.Cleanup(c)
}

func (h *Handler) ListFolders(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.ListFolders(c)
}

func (h *Handler) FolderStatus(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.FolderStatus(c)
}

func (h *Handler) RegenerateManifest(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.RegenerateManifest(c)
}

func (h *Handler) TrashFolder(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.TrashFolder(c)
}

func (h *Handler) DeleteFolder(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.DeleteFolder(c)
}

func (h *Handler) GetFolderChildren(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.GetFolderChildren(c)
}

func (h *Handler) GetTree(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.GetTree(c)
}

func (h *Handler) GetBreadcrumb(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.GetBreadcrumb(c)
}

func (h *Handler) DownloadClip(c *gin.Context) {
	if h.actions == nil {
		apiutil.Error(c, 503, "action sub-handler not wired")
		return
	}
	h.actions.DownloadClip(c)
}

func (h *Handler) ReuploadClip(c *gin.Context) {
	if h.actions == nil {
		apiutil.Error(c, 503, "action sub-handler not wired")
		return
	}
	h.actions.ReuploadClip(c)
}

func (h *Handler) FindDuplicates(c *gin.Context) {
	if h.actions == nil {
		apiutil.Error(c, 503, "action sub-handler not wired")
		return
	}
	h.actions.FindDuplicates(c)
}
