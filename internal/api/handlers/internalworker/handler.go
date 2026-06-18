package internalworker

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

type Broker interface {
	RegisterWorker(ctx context.Context, cmd job.RegisterWorkerCommand) (*job.WorkerSession, error)
	Heartbeat(ctx context.Context, cmd job.HeartbeatCommand) error
	Claim(ctx context.Context, cmd job.ClaimCommand) (*job.Lease, error)
	Renew(ctx context.Context, cmd job.RenewCommand) (*job.Lease, error)
	Progress(ctx context.Context, cmd job.ProgressCommand) error
	Complete(ctx context.Context, cmd job.CompleteCommand) error
	Fail(ctx context.Context, cmd job.FailCommand) error
	IsCancelled(ctx context.Context, jobID string, leaseID string) (bool, error)
}

type Handler struct {
	broker Broker
	log    *zap.Logger
}

func NewHandler(broker Broker, log *zap.Logger) *Handler {
	return &Handler{broker: broker, log: log}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/workers/register", h.RegisterWorker)
	r.POST("/workers/heartbeat", h.Heartbeat)
	r.POST("/jobs/claim", h.Claim)
	r.POST("/jobs/:id/renew", h.Renew)
	r.POST("/jobs/:id/progress", h.Progress)
	r.POST("/jobs/:id/complete", h.Complete)
	r.POST("/jobs/:id/fail", h.Fail)
	r.GET("/jobs/:id/cancelled", h.IsCancelled)
}

type registerWorkerRequest struct {
	WorkerID     string             `json:"worker_id"`
	Name         string             `json:"name,omitempty"`
	Version      string             `json:"version,omitempty"`
	Hostname     string             `json:"hostname,omitempty"`
	Capabilities job.WorkerCapabilities `json:"capabilities"`
}

func (h *Handler) RegisterWorker(c *gin.Context) {
	req, ok := apiutil.BindJSON[registerWorkerRequest](c)
	if !ok {
		return
	}
	session, err := h.broker.RegisterWorker(c.Request.Context(), job.RegisterWorkerCommand{
		WorkerID:     req.WorkerID,
		Name:         req.Name,
		Version:      req.Version,
		Hostname:     req.Hostname,
		Capabilities: req.Capabilities,
		SessionTTL:   90 * time.Second,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, session)
}

func (h *Handler) Heartbeat(c *gin.Context) {
	req, ok := apiutil.BindJSON[struct {
		WorkerID        string `json:"worker_id"`
		WorkerSessionID string `json:"worker_session_id"`
		SessionTTL      int64  `json:"session_ttl_seconds,omitempty"`
	}](c)
	if !ok {
		return
	}
	if err := h.broker.Heartbeat(c.Request.Context(), job.HeartbeatCommand{
		WorkerID:        req.WorkerID,
		WorkerSessionID: req.WorkerSessionID,
		SessionTTL:      time.Duration(req.SessionTTL) * time.Second,
	}); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true})
}

func (h *Handler) Claim(c *gin.Context) {
	req, ok := apiutil.BindJSON[job.ClaimCommand](c)
	if !ok {
		return
	}
	lease, err := h.broker.Claim(c.Request.Context(), req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, lease)
}

func (h *Handler) Renew(c *gin.Context) {
	req, ok := apiutil.BindJSON[job.RenewCommand](c)
	if !ok {
		return
	}
	req.JobID = c.Param("id")
	lease, err := h.broker.Renew(c.Request.Context(), req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, lease)
}

func (h *Handler) Progress(c *gin.Context) {
	req, ok := apiutil.BindJSON[job.ProgressCommand](c)
	if !ok {
		return
	}
	req.JobID = c.Param("id")
	if err := h.broker.Progress(c.Request.Context(), req); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true})
}

func (h *Handler) Complete(c *gin.Context) {
	req, ok := apiutil.BindJSON[job.CompleteCommand](c)
	if !ok {
		return
	}
	req.JobID = c.Param("id")
	if err := h.broker.Complete(c.Request.Context(), req); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true})
}

func (h *Handler) Fail(c *gin.Context) {
	req, ok := apiutil.BindJSON[job.FailCommand](c)
	if !ok {
		return
	}
	req.JobID = c.Param("id")
	if err := h.broker.Fail(c.Request.Context(), req); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true})
}

func (h *Handler) IsCancelled(c *gin.Context) {
	cancelled, err := h.broker.IsCancelled(c.Request.Context(), c.Param("id"), c.Query("lease_id"))
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"cancelled": cancelled})
}

var _ = http.StatusOK
