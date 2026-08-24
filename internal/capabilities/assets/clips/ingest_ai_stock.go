package clips

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips/aistock"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ingestAIStockRequest is the HTTP request body for POST /ingest/ai-stock.
type ingestAIStockRequest struct {
	Document json.RawMessage `json:"document"`
	DriveURL string          `json:"drive_url"`
}

// CreateAIStockClip handles POST /api/clips/ingest/ai-stock.
// It ingests an new AI-generated stock clip from a visual analysis document
// and a Google Drive video reference.
func (ih *IngestHandler) CreateAIStockClip(c *gin.Context) {
	if ih.aiStockUC == nil {
		ih.log.Error("CreateAIStockClip: AI stock use case not wired")
		apiutil.Error(c, http.StatusServiceUnavailable, "AI stock ingestion unavailable: use case not wired")
		return
	}

	var req ingestAIStockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	docStr := strings.TrimSpace(string(req.Document))
	if docStr == "" || docStr == "null" {
		apiutil.BadRequest(c, "document is required")
		return
	}
	if strings.TrimSpace(req.DriveURL) == "" {
		apiutil.BadRequest(c, "drive_url is required")
		return
	}

	cmd := aistock.CreateAIStockCommand{
		DocumentJSON: string(req.Document),
		DriveURL:     req.DriveURL,
	}

	res, err := ih.aiStockUC.Execute(c.Request.Context(), cmd)
	if err != nil {
		ih.log.Error("CreateAIStockClip: execution failed", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("ai stock ingestion failed: %w", err))
		return
	}

	apiutil.OK(c, gin.H{
		"ok":            true,
		"clip_id":       res.ClipID,
		"drive_file_id": res.DriveFileID,
		"local_path":    res.LocalPath,
	})
}
