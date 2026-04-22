package common

import (
"net/http"
"time"

"github.com/gin-gonic/gin"
"velox/go-master/internal/core/job"
"velox/go-master/internal/core/worker"
)

type DashboardHandler struct {
jobService    *job.Service
workerService *worker.Service
startTime     time.Time
}

func NewDashboardHandler(jobService *job.Service, workerService *worker.Service) *DashboardHandler {
return &DashboardHandler{jobService: jobService, workerService: workerService, startTime: time.Now()}
}

func (h *DashboardHandler) RegisterRoutes(rg *gin.RouterGroup) {
dashboard := rg.Group("/dashboard")
{
dashboard.GET("/metrics", h.GetMetrics)
dashboard.GET("/status", h.GetStatus)
dashboard.GET("/overview", h.GetOverview)
dashboard.GET("/jobs/recent", h.GetRecentJobs)
dashboard.GET("/workers/summary", h.GetWorkersSummary)
}
}

func (h *DashboardHandler) GetMetrics(c *gin.Context) {
jobs := h.jobService.GetAllJobs()
workers := h.workerService.ListWorkers()

jobStats := gin.H{"total": len(jobs), "pending": 0, "running": 0, "completed": 0, "failed": 0}
for _, j := range jobs {
switch string(j.Status) {
case "pending", "queued":
jobStats["pending"] = jobStats["pending"].(int) + 1
case "running", "processing":
jobStats["running"] = jobStats["running"].(int) + 1
case "completed":
jobStats["completed"] = jobStats["completed"].(int) + 1
case "failed", "cancelled":
jobStats["failed"] = jobStats["failed"].(int) + 1
}
}

workerStats := gin.H{"total": len(workers), "idle": 0, "busy": 0, "offline": 0}
for _, w := range workers {
switch w.Status {
case "busy":
workerStats["busy"] = workerStats["busy"].(int) + 1
case "offline":
workerStats["offline"] = workerStats["offline"].(int) + 1
default:
workerStats["idle"] = workerStats["idle"].(int) + 1
}
}

c.JSON(http.StatusOK, gin.H{
"ok":             true,
"uptime_seconds": int64(time.Since(h.startTime).Seconds()),
"jobs":           jobStats,
"workers":        workerStats,
"timestamp":      time.Now().UTC().Format(time.RFC3339),
})
}

func (h *DashboardHandler) GetStatus(c *gin.Context) {
c.JSON(http.StatusOK, gin.H{
"ok":             true,
"status":         "running",
"uptime_seconds": int64(time.Since(h.startTime).Seconds()),
})
}

func (h *DashboardHandler) GetOverview(c *gin.Context) {
jobs := h.jobService.GetAllJobs()
workers := h.workerService.ListActiveWorkers()

recentJobs := make([]gin.H, 0, minInt(len(jobs), 20))
for i := len(jobs) - 1; i >= 0 && len(recentJobs) < 20; i-- {
j := jobs[i]
recentJobs = append(recentJobs, gin.H{
"id":         j.ID,
"status":     j.Status,
"project":    j.Project,
"type":       j.Type,
"progress":   j.Progress,
"created_at": j.CreatedAt.Format(time.RFC3339),
"worker_id":  j.WorkerID,
})
}

activeWorkers := make([]gin.H, 0, len(workers))
for _, w := range workers {
activeWorkers = append(activeWorkers, gin.H{
"id":             w.ID,
"status":         w.Status,
"current_job_id": w.CurrentJobID,
"disk_free_gb":   w.DiskFreeGB,
"last_seen":      w.LastHeartbeat.Format(time.RFC3339),
})
}

c.JSON(http.StatusOK, gin.H{"ok": true, "recent_jobs": recentJobs, "active_workers": activeWorkers})
}

func (h *DashboardHandler) GetRecentJobs(c *gin.Context) {
jobs := h.jobService.GetAllJobs()
recent := make([]gin.H, 0, minInt(len(jobs), 50))
for i := len(jobs) - 1; i >= 0 && len(recent) < 50; i-- {
j := jobs[i]
recent = append(recent, gin.H{
"id":         j.ID,
"status":     j.Status,
"project":    j.Project,
"type":       j.Type,
"progress":   j.Progress,
"created_at": j.CreatedAt.Format(time.RFC3339),
"worker_id":  j.WorkerID,
})
}
c.JSON(http.StatusOK, gin.H{"ok": true, "jobs": recent, "count": len(recent)})
}

func (h *DashboardHandler) GetWorkersSummary(c *gin.Context) {
workers := h.workerService.ListWorkers()
summaries := make([]gin.H, 0, len(workers))
for _, w := range workers {
lastSeen := ""
if !w.LastHeartbeat.IsZero() {
lastSeen = w.LastHeartbeat.Format(time.RFC3339)
}
summaries = append(summaries, gin.H{
"id":             w.ID,
"status":         w.Status,
"current_job_id": w.CurrentJobID,
"disk_free_gb":   w.DiskFreeGB,
"last_seen":      lastSeen,
})
}
c.JSON(http.StatusOK, gin.H{"ok": true, "workers": summaries, "count": len(summaries)})
}

func minInt(a, b int) int {
if a < b {
return a
}
return b
}
