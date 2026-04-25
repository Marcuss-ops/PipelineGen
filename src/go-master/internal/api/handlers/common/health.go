package common

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HealthHandler handles health check requests
type HealthHandler struct{}

// NewHealthHandler creates a new health handler.
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// Health godoc
// @Summary Health check
// @Description Check if the server is healthy
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /health [get]
func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"ok":     true,
	})
}

// Status godoc
// @Summary Server status
// @Description Get detailed server status
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /status [get]
func (h *HealthHandler) Status(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"status": "running",
		"mode":   "minimal",
	})
}
