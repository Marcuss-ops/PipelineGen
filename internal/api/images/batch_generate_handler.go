// Package images (api/images) — batch_generate_handler.go holds
// the async batch image generation handler (POST /api/images/batch-generate).
// Per the golden rule: this is AI GENERATED territory — each item
// becomes an independent image.generate.google async job.
//
// FASE 3 (June 2026): the legacy synchronous Google Slides API
// implementation was removed. This endpoint now orchestrates the
// canonical async job system.
package images

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/primitives"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// batchJobResponse is the per-job entry in the 202 response.
//
// PR-DOMAIN-PRIMITIVES-NOMINAL (July 2026): JobID is the canonical
// nominal type (zero-cost on the wire — Go's `type X string` emits
// the underlying string in JSON unchanged).
type batchJobResponse struct {
	JobID    primitives.JobID `json:"job_id"`
	Position int              `json:"position"`
	Status   string           `json:"status"`
}

// generateBatchID creates a short unique batch identifier.
func generateBatchID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("imgbatch_%d", time.Now().UnixNano())
	}
	return "imgbatch_" + hex.EncodeToString(b)
}

// GenerateBatch handles POST /api/images/batch-generate — async
// batch image generation. Accepts a list of prompts and enqueues
// each as an independent image.generate.google job. Returns 202
// Accepted with batch_id and per-job status entries.
//
// Concurrency is controlled server-side by the worker pool, not
// by the client. Default dimensions: 1920×1080.
func (h *ImagesHandler) GenerateBatch(c *gin.Context) {
	var req GenerateBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	if h.jobsSvc == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"error": "job service not wired — image generation requires the async job system",
		})
		return
	}

	batchID := generateBatchID()
	if req.RequestID != "" {
		batchID = req.RequestID + "_" + batchID
	}

	// Apply defaults
	for i := range req.Items {
		if req.Items[i].Width == 0 {
			req.Items[i].Width = 1920
		}
		if req.Items[i].Height == 0 {
			req.Items[i].Height = 1080
		}
	}

	jobs := make([]batchJobResponse, len(req.Items))
	for i, item := range req.Items {
		position := i
		correlationID := fmt.Sprintf("%s_%d", batchID, position)

		payload := map[string]any{
			"batch_id": batchID,
			"position": position,
			"prompt":   item.Prompt,
			"style":    item.Style,
			"width":    item.Width,
			"height":   item.Height,
			"tags":     item.Tags,
		}

		enqueued, err := h.jobsSvc.Enqueue(c.Request.Context(), &job.EnqueueRequest{
			Type:          appjobs.TypeImageGenerateGoogle,
			CorrelationID: correlationID,
			Payload:       payload,
			MaxRetries:    2,
		})
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to enqueue job %d/%d: %w", i+1, len(req.Items), err))
			return
		}

		jobs[i] = batchJobResponse{
			JobID:    primitives.NewJobID(enqueued.ID),
			Position: position,
			Status:   string(enqueued.Status),
		}
	}

	c.JSON(http.StatusAccepted, gin.H{
		"batch_id": batchID,
		"accepted": len(jobs),
		"jobs":     jobs,
	})
}
