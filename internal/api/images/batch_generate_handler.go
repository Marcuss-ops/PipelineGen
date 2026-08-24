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
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/primitives"
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

// resolveBatchItems normalizes the request into the flat item list that
// GenerateBatch enqueues. mode "" | "items" consumes req.Items verbatim;
// mode "sections" composes one item per section from the canonical
// section→prompt composer (fullimages.BuildPrimaryPrompt) using the
// section-image dimensions (1344×768).
func resolveBatchItems(req GenerateBatchRequest) ([]GenerateBatchItem, error) {
	switch req.Mode {
	case "", "items":
		if len(req.Items) == 0 {
			return nil, fmt.Errorf("items is required when mode is empty or \"items\"")
		}
		return req.Items, nil
	case "sections":
		if len(req.Sections) == 0 {
			return nil, fmt.Errorf("sections is required when mode is \"sections\"")
		}
		items := make([]GenerateBatchItem, 0, len(req.Sections))
		for i, sec := range req.Sections {
			prompt := fullimages.BuildPrimaryPrompt(sec, req.Topic)
			if prompt == "" {
				return nil, fmt.Errorf("section[%d] has no title, text, or topic — cannot build a prompt", i)
			}
			style := strings.TrimSpace(sec.Style)
			subject := sec.Title
			if subject == "" {
				subject = fmt.Sprintf("section_%d", i)
			}
			items = append(items, GenerateBatchItem{
				Prompt: prompt,
				Style:  style,
				Width:  fullimages.SectionImageWidth,
				Height: fullimages.SectionImageHeight,
				Tags:   []string{subject, "style:" + style},
			})
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported mode %q (want \"items\" or \"sections\")", req.Mode)
	}
}

// GenerateBatch handles POST /api/images/batch-generate — async
// batch image generation. Accepts a list of prompts (mode empty or
// "items") or a list of text sections (mode "sections", the retired
// fullimages shape) and enqueues each as an independent
// image.generate.google job. Returns 202 Accepted with batch_id and
// per-job status entries.
//
// Concurrency is controlled server-side by the worker pool, not
// by the client. Default dimensions: 1920×1080 (1344×768 for
// mode="sections").
func (h *ImagesHandler) GenerateBatch(c *gin.Context) {
	var req GenerateBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	items, err := resolveBatchItems(req)
	if err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	// Canonical per-item validation: whitespace-only prompt and negative
	// dimensions must be rejected before enqueue.
	for i, item := range items {
		if err := item.Validate(); err != nil {
			apiutil.BadRequest(c, fmt.Sprintf("item %d: %v", i, err))
			return
		}
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
	for i := range items {
		if items[i].Width == 0 {
			items[i].Width = 1920
		}
		if items[i].Height == 0 {
			items[i].Height = 1080
		}
	}

	jobs := make([]batchJobResponse, len(items))
	for i, item := range items {
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
			apiutil.InternalError(c, fmt.Errorf("failed to enqueue job %d/%d: %w", i+1, len(items), err))
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
