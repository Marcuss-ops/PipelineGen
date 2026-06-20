package sources

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"

	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"go.uber.org/zap"
)

// BatchRegisterRequest is the JSON body for batch registering clips from YouTube.
type BatchRegisterRequest struct {
	FolderID string                       `json:"folder_id"`
	Clips    []RegisterFromYouTubeRequest `json:"clips" binding:"required"`
}

// BatchClipResult is the result for a single clip in a batch registration.
type BatchClipResult struct {
	ClipID    string `json:"clip_id,omitempty"`
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Duplicate bool   `json:"duplicate,omitempty"`
}

// BatchRegisterResponse is the response for batch registration.
type BatchRegisterResponse struct {
	OK        bool              `json:"ok"`
	Total     int               `json:"total"`
	Succeeded int               `json:"succeeded"`
	Failed    int               `json:"failed"`
	Results   []BatchClipResult `json:"results"`
}

// BatchRegisterFromYouTube handles POST /api/media/register-batch
//
// Accepts an array of YouTube clips and processes them sequentially.
// Each clip goes through the same pipeline as register-from-youtube:
// download, Drive upload, metadata.json, DB save, Qdrant index.
func (h *Handler) BatchRegisterFromYouTube(c *gin.Context) {
	var req BatchRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if len(req.Clips) == 0 {
		apiutil.BadRequest(c, "clips list is empty")
		return
	}

	// Inject shared folder_id into each clip that doesn't have one
	for i := range req.Clips {
		if req.Clips[i].FolderID == "" && req.FolderID != "" {
			req.Clips[i].FolderID = req.FolderID
		}
	}

	ctx := c.Request.Context()
	log := h.log.With(zap.String("handler", "batch-register"), zap.Int("total", len(req.Clips)))

	results := make([]BatchClipResult, len(req.Clips))
	var succeeded, failed int

	log.Info("starting batch registration", zap.Int("clips", len(req.Clips)))

	for i, clip := range req.Clips {
		result := h.processBatchClip(ctx, clip)
		results[i] = result
		if result.OK || result.Duplicate {
			succeeded++
		} else {
			failed++
		}

		log.Info("batch clip processed",
			zap.Int("index", i+1),
			zap.Int("total", len(req.Clips)),
			zap.String("name", clip.Name),
			zap.Bool("ok", result.OK),
			zap.Bool("duplicate", result.Duplicate),
			zap.String("error", result.Error))
	}

	log.Info("batch registration completed",
		zap.Int("succeeded", succeeded),
		zap.Int("failed", failed))

	apiutil.OK(c, BatchRegisterResponse{
		OK:        true,
		Total:     len(req.Clips),
		Succeeded: succeeded,
		Failed:    failed,
		Results:   results,
	})
}

// processBatchClip processes a single clip within a 
// It captures the RegisterFromYouTube handler output by redirecting
// the response to a buffer, then parses the JSON result.
func (h *Handler) processBatchClip(ctx context.Context, clip RegisterFromYouTubeRequest) BatchClipResult {
	result := BatchClipResult{
		Name: clip.Name,
	}

	// Serialize clip request as JSON body
	body, err := json.Marshal(clip)
	if err != nil {
		result.Error = "failed to serialize clip: " + err.Error()
		return result
	}

	// Build a synthetic HTTP request
	httpReq := &gin.Context{}
	httpReq.Request, _ = http.NewRequestWithContext(ctx, "POST", "/api/media/register-from-youtube", bytes.NewReader(body))
	httpReq.Request.Header.Set("Content-Type", "application/json")
	httpReq.Set("_batch_mode", true)
	httpReq.Keys = make(map[string]any)

	// Use a ResponseRecorder to capture output
	w := &batchResponseWriter{body: &bytes.Buffer{}}
	httpReq.Writer = w

	// Call the existing handler
	h.RegisterFromYouTube(httpReq)

	// Parse response
	respBody, err := io.ReadAll(w.body)
	if err != nil {
		result.Error = "failed to read response"
		return result
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		result.Error = "failed to parse response"
		return result
	}

	if ok, exists := resp["ok"].(bool); exists && ok {
		result.OK = true
		result.ClipID, _ = resp["clip_id"].(string)
		if dup, exists := resp["duplicate"].(bool); exists && dup {
			result.Duplicate = true
			result.OK = false
		}
	} else if errMsg, exists := resp["error"].(string); exists {
		result.Error = errMsg
	} else if msg, exists := resp["message"].(string); exists {
		result.Error = msg
	}

	return result
}

// batchResponseWriter is a minimal gin.ResponseWriter that captures the body.
type batchResponseWriter struct {
	body *bytes.Buffer
}

func (w *batchResponseWriter) Header() http.Header                  { return http.Header{} }
func (w *batchResponseWriter) Write(b []byte) (int, error)          { return w.body.Write(b) }
func (w *batchResponseWriter) WriteHeader(statusCode int)           {}
func (w *batchResponseWriter) WriteHeaderNow()                      {}
func (w *batchResponseWriter) Written() bool                        { return w.body.Len() > 0 }
func (w *batchResponseWriter) WriteString(s string) (int, error)    { return w.body.WriteString(s) }
func (w *batchResponseWriter) Size() int                            { return w.body.Len() }
func (w *batchResponseWriter) Status() int                          { return 200 }
func (w *batchResponseWriter) Flush()                               {}
func (w *batchResponseWriter) CloseNotify() <-chan bool             { return make(chan bool) }
func (w *batchResponseWriter) Pusher() http.Pusher                  { return nil }
func (w *batchResponseWriter) SetReadDeadline(_ interface{}) error  { return nil }
func (w *batchResponseWriter) SetWriteDeadline(_ interface{}) error { return nil }
func (w *batchResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, nil
}
