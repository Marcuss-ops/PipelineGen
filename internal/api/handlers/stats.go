package handlers

import (
"net/http"
"time"

"github.com/gin-gonic/gin"
"velox/go-master/internal/core/job"
"velox/go-master/internal/core/worker"
)

type StatsHandler struct {
jobService    *job.Service
workerService *worker.Service
}

func NewStatsHandler(jobService *job.Service, workerService *worker.Service) *StatsHandler {
return &StatsHandler{jobService: jobService, workerService: workerService}
}

func (h *StatsHandler) RegisterRoutes(rg *gin.RouterGroup) {
stats := rg.Group("/stats")
{
stats.GET("/jobs", h.GetJobStats)
stats.GET("/workers", h.GetWorkerStats)
stats.GET("/performance", h.GetPerformanceStats)
stats.GET("/errors", h.GetErrorStats)
}
}

func (h *StatsHandler) GetJobStats(c *gin.Context) {
jobs := h.jobService.GetAllJobs()
byStatus := map[string]int{}
byType := map[string]int{}
byProject := map[string]int{}
completed := 0
var totalDuration float64

for _, j := range jobs {
byStatus[string(j.Status)]++
byType[string(j.Type)]++
if j.Project != "" {
byProject[j.Project]++
}
if j.Status == "completed" && j.StartedAt != nil && j.CompletedAt != nil {
completed++
totalDuration += j.CompletedAt.Sub(*j.StartedAt).Seconds()
}
}

avg := 0.0
if completed > 0 {
avg = totalDuration / float64(completed)
}

c.JSON(http.StatusOK, gin.H{"ok": true, "stats": gin.H{
"total": len(jobs), "by_status": byStatus, "by_type": byType, "by_project": byProject,
"average_duration_seconds": avg,
}})
}

func (h *StatsHandler) GetWorkerStats(c *gin.Context) {
workers := h.workerService.ListWorkers()
byStatus := map[string]int{}
activeJobs := 0
var totalDisk float64

for _, w := range workers {
byStatus[string(w.Status)]++
totalDisk += w.DiskFreeGB
if w.CurrentJobID != "" {
activeJobs++
}
}

avgDisk := 0.0
if len(workers) > 0 {
avgDisk = totalDisk / float64(len(workers))
}

c.JSON(http.StatusOK, gin.H{"ok": true, "stats": gin.H{
"total": len(workers), "by_status": byStatus, "active_jobs": activeJobs,
"total_disk_free_gb": totalDisk, "average_disk_free_gb": avgDisk,
}})
}

func (h *StatsHandler) GetPerformanceStats(c *gin.Context) {
jobs := h.jobService.GetAllJobs()
var processTotal float64
processed := 0
lastHour := 0
cutoff := time.Now().Add(-time.Hour)

for _, j := range jobs {
if j.CreatedAt.After(cutoff) {
lastHour++
}
if j.StartedAt != nil && j.CompletedAt != nil {
processTotal += j.CompletedAt.Sub(*j.StartedAt).Seconds()
processed++
}
}

avgProcess := 0.0
if processed > 0 {
avgProcess = processTotal / float64(processed)
}

c.JSON(http.StatusOK, gin.H{"ok": true, "stats": gin.H{
"jobs_per_hour": lastHour,
"jobs_per_minute": float64(lastHour) / 60.0,
"average_process_time_seconds": avgProcess,
"throughput_jobs_per_hour": lastHour,
}})
}

func (h *StatsHandler) GetErrorStats(c *gin.Context) {
jobs := h.jobService.GetAllJobs()
errors := make([]gin.H, 0, 20)
count := 0
for _, j := range jobs {
if j.Error == "" {
continue
}
count++
if len(errors) < 20 {
timestamp := ""
if j.CompletedAt != nil {
timestamp = j.CompletedAt.Format(time.RFC3339)
}
errors = append(errors, gin.H{"job_id": j.ID, "worker_id": j.WorkerID, "error": j.Error, "timestamp": timestamp})
}
}
c.JSON(http.StatusOK, gin.H{"ok": true, "stats": gin.H{"total_errors": count, "recent_errors": errors}})
}
