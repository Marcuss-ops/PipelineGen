package script

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// SSEHandler handles Server-Sent Events for progress tracking
type SSEHandler struct {
	tracker *ProgressTracker
}

// NewSSEHandler creates a new SSE handler
func NewSSEHandler(tracker *ProgressTracker) *SSEHandler {
	return &SSEHandler{tracker: tracker}
}

// RegisterRoutes registers the SSE endpoints with Gin
func (h *SSEHandler) RegisterRoutes(router gin.IRouter) {
	router.GET("/api/script/progress/:operationId", h.HandleSSE)
	router.GET("/api/script/progress/:operationId/events", h.GetEvents)
}

// HandleSSE handles Server-Sent Events streaming
func (h *SSEHandler) HandleSSE(c *gin.Context) {
	operationID := c.Param("operationId")
	if operationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "operationId is required"})
		return
	}

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming unsupported"})
		return
	}

	logger.Info("SSE connection established", zap.String("operation_id", operationID))

	// Subscribe to progress events
	ch := h.tracker.Subscribe(operationID)
	defer h.tracker.Unsubscribe(operationID, ch)

	// Send existing events first
	events := h.tracker.GetEvents(operationID)
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		flusher.Flush()
	}

	// Stream new events
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				// Channel closed, operation complete
				return
			}

			data, err := json.Marshal(event)
			if err != nil {
				continue
			}

			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			flusher.Flush()

			// If complete event, close after a short delay
			if event.Type == "complete" {
				time.Sleep(1 * time.Second)
				return
			}

		case <-c.Request.Context().Done():
			logger.Info("SSE connection closed by client", zap.String("operation_id", operationID))
			return
		}
	}
}

// GetEvents returns all events for an operation as JSON
func (h *SSEHandler) GetEvents(c *gin.Context) {
	operationID := c.Param("operationId")
	if operationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "operationId is required"})
		return
	}

	events := h.tracker.GetEvents(operationID)

	c.JSON(http.StatusOK, gin.H{
		"operation_id": operationID,
		"events":       events,
		"count":        len(events),
	})
}

// WriteSSE is a helper to write SSE data
func WriteSSE(w http.ResponseWriter, eventType string, data interface{}) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported")
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	if eventType != "" {
		fmt.Fprintf(w, "event: %s\n", eventType)
	}
	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	flusher.Flush()
	return nil
}

// CloseSSE sends a close event and closes the connection
func CloseSSE(w http.ResponseWriter) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported")
	}

	fmt.Fprint(w, "event: close\ndata: null\n\n")
	flusher.Flush()
	if closer, ok := w.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}