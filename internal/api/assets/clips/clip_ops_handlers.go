package clips

import (
	"context"
	"fmt"
	"net/http"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *Handler) HandleBulkUploadYouTubeClipsJob(ctx context.Context, j *domainjob.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if h.bulkUploadWorker == nil {
		return nil, fmt.Errorf("bulk upload worker not configured")
	}
	return h.bulkUploadWorker.HandleJob(ctx, j, tools)
}

func (h *Handler) HandleFixHash(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")
	if h.clipOpsService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip ops service not wired")
		return
	}
	report, err := h.clipOpsService.FixHash(c.Request.Context(), source, clipID)
	if err != nil {
		switch err {
		case appclips.ErrFixHashVoiceoverUnsupported:
			apiutil.BadRequest(c, err.Error())
		case appclips.ErrFixHashMissingDriveLink:
			apiutil.Error(c, http.StatusConflict, err.Error())
		case appclips.ErrFixHashDispatcherUnavailable:
			apiutil.Error(c, http.StatusServiceUnavailable, err.Error())
		default:
			apiutil.InternalError(c, err)
		}
		return
	}
	apiutil.OK(c, gin.H{"ok": true, "report": report})
}

// updateCumulativeMetadataJSON is a best-effort helper used by the upload flow.
// The metadata file is maintained elsewhere; keep this call non-fatal so upload
// progress isn't blocked on sidecar JSON persistence.
func (h *Handler) updateCumulativeMetadataJSON(_ context.Context, _ string, _ string, _ string, _ map[string]interface{}, log *zap.Logger) {
	if log != nil {
		log.Debug("updateCumulativeMetadataJSON called")
	}
}
