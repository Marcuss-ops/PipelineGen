package assets

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
)

// Handler is the HTTP projection of the stock batch coordinator.
type Handler struct {
	coordinator *stockplan.Coordinator
	log         *zap.Logger
}

// NewHandler constructs the stock-batches handler.
func NewHandler(coordinator *stockplan.Coordinator, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{coordinator: coordinator, log: log}
}

// RegisterRoutes mounts POST /run and GET /:id under the supplied group.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("registering stock-batches routes")
	r.POST("/run", h.Run)
	r.GET("/:id", h.Get)
}

// runRequest is the JSON body for POST /api/stock-batches/run.
type runRequest struct {
	SourceURL   string                    `json:"source_url"`
	Destination stockplan.DestinationSpec `json:"destination"`
	Sampling    stockplan.SamplingPolicy  `json:"sampling"`
	Groups      []stockplan.GroupSpec     `json:"groups"`
}

// runResponse is the JSON body for a successful /api/stock-batches/run.
type runResponse struct {
	BatchID string `json:"batch_id"`
	Status  string `json:"status"`
}

// errorResponse is the JSON body for an error response.
type errorResponse struct {
	Status    string `json:"status"`
	Error     string `json:"error"`
	ErrorCode string `json:"error_code"`
}

const (
	StatusError           = "error"
	ErrCodeInvalidPayload = "INVALID_PAYLOAD"
	ErrCodeNotFound       = "NOT_FOUND"
	ErrCodeInternal       = "INTERNAL_ERROR"
)

// Run handles POST /api/stock-batches/run.
func (h *Handler) Run(c *gin.Context) {
	var req runRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("stock-batches: invalid request payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, errorResponse{
			Status:    StatusError,
			Error:     "invalid JSON payload: " + err.Error(),
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}

	spec := stockplan.BatchSpec{
		SourceURL:   req.SourceURL,
		Destination: req.Destination,
		Sampling:    req.Sampling,
		Groups:      req.Groups,
	}

	result, err := h.coordinator.Run(c.Request.Context(), spec)
	if err != nil {
		h.log.Error("stock-batches: run failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errorResponse{
			Status:    StatusError,
			Error:     err.Error(),
			ErrorCode: ErrCodeInternal,
		})
		return
	}

	c.JSON(http.StatusAccepted, runResponse{
		BatchID: result.BatchID,
		Status:  result.Status,
	})
}

// Get handles GET /api/stock-batches/:id.
func (h *Handler) Get(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, errorResponse{
			Status:    StatusError,
			Error:     "batch id is required",
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}

	status, err := h.coordinator.Get(c.Request.Context(), id)
	if err != nil {
		h.log.Error("stock-batches: get failed", zap.String("batch_id", id), zap.Error(err))
		c.JSON(http.StatusInternalServerError, errorResponse{
			Status:    StatusError,
			Error:     err.Error(),
			ErrorCode: ErrCodeInternal,
		})
		return
	}
	if status == nil || status.Batch == nil {
		c.JSON(http.StatusNotFound, errorResponse{
			Status:    StatusError,
			Error:     "batch not found",
			ErrorCode: ErrCodeNotFound,
		})
		return
	}

	c.JSON(http.StatusOK, status)
}
