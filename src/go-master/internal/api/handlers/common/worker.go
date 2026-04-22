package common

import (
"net/http"

"github.com/gin-gonic/gin"
coreworker "velox/go-master/internal/core/worker"
"velox/go-master/pkg/models"
)

type WorkerHandler struct {
service *coreworker.Service
}

func NewWorkerHandler(service *coreworker.Service) *WorkerHandler {
return &WorkerHandler{service: service}
}

func (h *WorkerHandler) RegisterRoutes(rg *gin.RouterGroup) {
workers := rg.Group("/workers")
{
workers.GET("", h.ListWorkers)
workers.POST("/register", h.RegisterWorker)
workers.GET(":id", h.GetWorker)
workers.POST(":id/heartbeat", h.Heartbeat)
workers.GET(":id/commands", h.GetCommands)
workers.POST(":id/commands/:command_id/ack", h.AckCommand)
workers.POST(":id/revoke", h.RevokeWorker)
workers.POST(":id/quarantine", h.QuarantineWorker)
workers.POST(":id/unquarantine", h.UnquarantineWorker)
workers.POST(":id/command", h.SendCommand)
}
rg.POST("/worker/poll", h.Poll)
}

func (h *WorkerHandler) ListWorkers(c *gin.Context) {
workers := h.service.ListWorkers()
c.JSON(http.StatusOK, gin.H{"ok": true, "workers": workers, "count": len(workers)})
}

func (h *WorkerHandler) RegisterWorker(c *gin.Context) {
var req models.WorkerRegistrationRequest
if err := c.ShouldBindJSON(&req); err != nil {
c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
return
}
w, token, err := h.service.RegisterWorker(c.Request.Context(), req)
if err != nil {
c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusCreated, gin.H{"ok": true, "worker_id": w.ID, "token": token, "worker": w})
}

func (h *WorkerHandler) GetWorker(c *gin.Context) {
w, err := h.service.GetWorker(c.Param("id"))
if err != nil {
status := http.StatusInternalServerError
if err == coreworker.ErrWorkerNotFound {
status = http.StatusNotFound
}
c.JSON(status, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true, "worker": w})
}

func (h *WorkerHandler) Heartbeat(c *gin.Context) {
var heartbeat models.WorkerHeartbeat
if err := c.ShouldBindJSON(&heartbeat); err != nil {
c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
return
}
heartbeat.WorkerID = c.Param("id")
if err := h.service.UpdateWorkerStatus(c.Request.Context(), &heartbeat); err != nil {
status := http.StatusInternalServerError
if err == coreworker.ErrWorkerRevoked || err == coreworker.ErrWorkerQuarantined {
status = http.StatusForbidden
}
c.JSON(status, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *WorkerHandler) Poll(c *gin.Context) {
var heartbeat models.WorkerHeartbeat
if err := c.ShouldBindJSON(&heartbeat); err != nil {
c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
return
}
if err := h.service.UpdateWorkerStatus(c.Request.Context(), &heartbeat); err != nil {
c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": err.Error()})
return
}
commands, err := h.service.GetPendingCommands(c.Request.Context(), heartbeat.WorkerID)
if err != nil {
c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Failed to get commands: " + err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true, "commands": commands})
}

func (h *WorkerHandler) GetCommands(c *gin.Context) {
commands, err := h.service.GetPendingCommands(c.Request.Context(), c.Param("id"))
if err != nil {
c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true, "commands": commands})
}

func (h *WorkerHandler) AckCommand(c *gin.Context) {
if err := h.service.AckCommand(c.Request.Context(), c.Param("command_id")); err != nil {
c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *WorkerHandler) RevokeWorker(c *gin.Context) {
	var req struct{ Reason string `json:"reason"` }
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Invalid request: " + err.Error()})
		return
	}
	if err := h.service.RevokeWorker(c.Request.Context(), c.Param("id"), req.Reason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *WorkerHandler) QuarantineWorker(c *gin.Context) {
	var req struct{ Reason string `json:"reason"` }
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Invalid request: " + err.Error()})
		return
	}
	if err := h.service.QuarantineWorker(c.Request.Context(), c.Param("id"), req.Reason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *WorkerHandler) UnquarantineWorker(c *gin.Context) {
if err := h.service.UnquarantineWorker(c.Request.Context(), c.Param("id")); err != nil {
c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *WorkerHandler) SendCommand(c *gin.Context) {
var req struct {
Type    string                 `json:"type" binding:"required"`
Payload map[string]interface{} `json:"payload"`
}
if err := c.ShouldBindJSON(&req); err != nil {
c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
return
}
cmd, err := h.service.SendCommand(c.Request.Context(), c.Param("id"), req.Type, req.Payload)
if err != nil {
c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
return
}
c.JSON(http.StatusOK, gin.H{"ok": true, "command": cmd})
}
