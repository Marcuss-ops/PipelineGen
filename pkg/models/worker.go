// Package models contiene i tipi di dati condivisi tra tutti i package
package models

import (
	"encoding/json"
	"time"
)

// WorkerStatus rappresenta lo stato di un worker
type WorkerStatus string

const (
	WorkerIdle         WorkerStatus = "idle"
	WorkerBusy         WorkerStatus = "busy"
	WorkerOffline      WorkerStatus = "offline"
	WorkerError        WorkerStatus = "error"
	WorkerUpdating     WorkerStatus = "updating"
	WorkerShuttingDown WorkerStatus = "shutting_down"
)

// Alias per compatibilità
const (
	WorkerStatusOnline  = WorkerIdle
	WorkerStatusIdle    = WorkerIdle
	WorkerStatusBusy    = WorkerBusy
	WorkerStatusOffline = WorkerOffline
)

// WorkerCapability rappresenta una capability del worker
type WorkerCapability string

const (
	CapVideoGeneration WorkerCapability = "video_generation"
	CapAudioProcessing WorkerCapability = "audio_processing"
	CapRemotion        WorkerCapability = "remotion"
	CapFFmpeg          WorkerCapability = "ffmpeg"
	CapUpload          WorkerCapability = "upload"
	CapTTS             WorkerCapability = "tts"
	CapStockDownload   WorkerCapability = "stock_download"
)

// Alias per compatibilità
const (
	WorkerCapabilityVideoGen  = CapVideoGeneration
	WorkerCapabilityAudio     = CapAudioProcessing
	WorkerCapabilityRemotion  = CapRemotion
	WorkerCapabilityFFmpeg    = CapFFmpeg
	WorkerCapabilityUpload    = CapUpload
	WorkerCapabilityTTS       = CapTTS
	WorkerCapabilityStockClip = CapStockDownload
	WorkerCapabilityVoiceover = CapTTS
	WorkerCapabilityScript    = CapAudioProcessing
)

// Worker rappresenta un worker nel sistema
type Worker struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	Status            WorkerStatus       `json:"status"`
	Capabilities      []WorkerCapability `json:"capabilities"`
	CurrentJobID      string             `json:"current_job_id,omitempty"`
	Host              string             `json:"host"`
	Port              int                `json:"port"`
	Version           string             `json:"version"`
	LastSeen          time.Time          `json:"last_seen"`
	RegisteredAt      time.Time          `json:"registered_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
	Metadata          map[string]string  `json:"metadata,omitempty"`
	Stats             WorkerStats        `json:"stats"`
	Token             string             `json:"token,omitempty"`
	Hostname          string             `json:"hostname,omitempty"`
	IP                string             `json:"ip,omitempty"`
	CodeHash          string             `json:"code_hash,omitempty"`
	LastHeartbeat     time.Time          `json:"last_heartbeat"`
	DiskTotalGB       float64            `json:"disk_total_gb,omitempty"`
	DiskFreeGB        float64            `json:"disk_free_gb,omitempty"`
	MemoryTotalMB     int                `json:"memory_total_mb,omitempty"`
	MemoryFreeMB      int                `json:"memory_free_mb,omitempty"`
	CPUUsage          float64            `json:"cpu_usage,omitempty"`
	MaxConcurrentJobs int                `json:"max_concurrent_jobs"`
	Priority          int                `json:"priority"`
	AutoUpdateEnabled bool               `json:"auto_update_enabled"`
	Tags              []string           `json:"tags,omitempty"`
	MaintenanceMode   bool               `json:"maintenance_mode"`
}

// WorkerRegistrationRequest richiesta di registrazione worker
type WorkerRegistrationRequest struct {
	Name           string             `json:"name" binding:"required,min=1"`
	Token          string             `json:"token"`
	Host           string             `json:"host" binding:"required,min=1"`
	Port           int                `json:"port" binding:"required"`
	Capabilities   []WorkerCapability `json:"capabilities"`
	Version        string             `json:"version"`
	Hostname       string             `json:"hostname,omitempty"`
	IP             string             `json:"ip,omitempty"`
	CodeHash       string             `json:"code_hash,omitempty"`
	DiskTotalGB    float64            `json:"disk_total_gb,omitempty"`
	MemoryTotalMB  int                `json:"memory_total_mb,omitempty"`
}

// WorkerHeartbeat heartbeat dal worker
type WorkerHeartbeat struct {
	WorkerID     string            `json:"worker_id" binding:"required,min=1"`
	Status       WorkerStatus      `json:"status"`
	CurrentJobID string            `json:"current_job_id,omitempty"`
	Stats        WorkerStats       `json:"stats"`
	Logs         []WorkerLogEntry  `json:"logs,omitempty"`
	Errors       []WorkerErrorEntry `json:"errors,omitempty"`
	DiskFreeGB   float64           `json:"disk_free_gb,omitempty"`
	MemoryFreeMB int               `json:"memory_free_mb,omitempty"`
	CPUUsage     float64           `json:"cpu_usage,omitempty"`
	Version      string            `json:"version,omitempty"`
	CodeHash     string            `json:"code_hash,omitempty"`
	Capabilities []WorkerCapability `json:"capabilities,omitempty"`
}

// WorkerLogEntry log entry dal worker
type WorkerLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	JobID     string    `json:"job_id,omitempty"`
}

// WorkerErrorEntry error entry dal worker
type WorkerErrorEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Error     string    `json:"error"`
	JobID     string    `json:"job_id,omitempty"`
	Stack     string    `json:"stack,omitempty"`
}

// WorkerStats contiene le statistiche del worker
type WorkerStats struct {
	JobsCompleted    int        `json:"jobs_completed"`
	JobsFailed       int        `json:"jobs_failed"`
	TotalWorkTime    int64      `json:"total_work_time_ms"`
	AvgJobTime       int64      `json:"avg_job_time_ms"`
	DiskFree         int64      `json:"disk_free_bytes"`
	MemoryFree       int64      `json:"memory_free_bytes"`
	CPUUsage         float64    `json:"cpu_usage_percent"`
	LastJobStarted   *time.Time `json:"last_job_started,omitempty"`
	LastJobCompleted *time.Time `json:"last_job_completed,omitempty"`
}

// Heartbeat rappresenta un heartbeat inviato dal worker
type Heartbeat struct {
	WorkerID     string            `json:"worker_id"`
	Status       WorkerStatus      `json:"status"`
	CurrentJobID string            `json:"current_job_id,omitempty"`
	Timestamp    time.Time         `json:"timestamp"`
	Stats        WorkerStats       `json:"stats"`
	Logs         []WorkerLogEntry  `json:"logs,omitempty"`
	Errors       []WorkerErrorEntry `json:"errors,omitempty"`
}

// LogEntry rappresenta una voce di log
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	JobID     string    `json:"job_id,omitempty"`
}

// ErrorEntry rappresenta una voce di errore
type ErrorEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Error     string    `json:"error"`
	JobID     string    `json:"job_id,omitempty"`
	Stack     string    `json:"stack,omitempty"`
}

// WorkerCommand rappresenta un comando da inviare al worker
type WorkerCommand struct {
	ID           string                 `json:"id"`
	Type         string                 `json:"type"`
	WorkerID     string                 `json:"worker_id,omitempty"`
	Payload      map[string]interface{} `json:"payload,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
	Acknowledged bool                   `json:"acknowledged"`
}

// CommandType rappresenta il tipo di comando
type CommandType string

const (
	CmdShutdown  CommandType = "shutdown"
	CmdRestart   CommandType = "restart"
	CmdUpdate    CommandType = "update"
	CmdCancelJob CommandType = "cancel_job"
	CmdPause     CommandType = "pause"
	CmdResume    CommandType = "resume"
)

// WorkerRegistry contiene tutti i worker registrati
type WorkerRegistry struct {
	Workers   map[string]*Worker `json:"workers"`
	UpdatedAt time.Time          `json:"updated_at"`
	Version   int                `json:"version"`
}

// QuarantineInfo info su worker in quarantena
type QuarantineInfo struct {
	WorkerID      string `json:"worker_id"`
	Reason        string `json:"reason"`
	QuarantinedAt int64  `json:"quarantined_at"`
	ErrorCount    int    `json:"error_count"`
}

// FailHistoryEntry entry nella storia dei fallimenti
type FailHistoryEntry struct {
	WorkerID  string `json:"worker_id"`
	Timestamp int64  `json:"timestamp"`
	Error     string `json:"error"`
	JobID     string `json:"job_id,omitempty"`
}

// NewWorker crea un nuovo worker con valori di default
func NewWorker(id, name, host string, port int, capabilities []WorkerCapability) *Worker {
	now := time.Now()
	return &Worker{
		ID:                id,
		Name:              name,
		Status:            WorkerIdle,
		Capabilities:      capabilities,
		Host:              host,
		Port:              port,
		Version:           "1.0.0",
		LastSeen:          now,
		RegisteredAt:      now,
		UpdatedAt:         now,
		LastHeartbeat:     now,
		Metadata:          make(map[string]string),
		MaxConcurrentJobs: 2,
		Priority:          5,
		AutoUpdateEnabled: true,
		Tags:              []string{},
		Stats: WorkerStats{
			JobsCompleted: 0,
			JobsFailed:    0,
		},
	}
}

// IsOnline restituisce true se il worker è online
func (w *Worker) IsOnline(timeout time.Duration) bool {
	return time.Since(w.LastSeen) < timeout
}

// HasCapability restituisce true se il worker ha la capability specificata
func (w *Worker) HasCapability(cap WorkerCapability) bool {
	for _, c := range w.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// CanAcceptJob restituisce true se il worker può accettare un job
func (w *Worker) CanAcceptJob() bool {
	return w.Status == WorkerIdle && w.CurrentJobID == ""
}

// Clone crea una copia profonda del worker
func (w *Worker) Clone() *Worker {
	data, _ := json.Marshal(w)
	var clone Worker
	json.Unmarshal(data, &clone)
	return &clone
}

// UpdateStats aggiorna le statistiche del worker
func (w *Worker) UpdateStats(stats WorkerStats) {
	w.Stats = stats
	w.UpdatedAt = time.Now()
}

// Touch aggiorna il timestamp last_seen
func (w *Worker) Touch() {
	w.LastSeen = time.Now()
	w.UpdatedAt = time.Now()
}