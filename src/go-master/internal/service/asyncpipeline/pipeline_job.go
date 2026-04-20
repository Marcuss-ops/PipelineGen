package asyncpipeline

import (
	"context"
	"sync"
	"time"

	"velox/go-master/internal/service/scriptclips"
)

type PipelineJob struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"` // pending, running, completed, failed, cancelled
	Title       string     `json:"title"`
	Language    string     `json:"language"`
	Duration    int        `json:"duration"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Error       string     `json:"error,omitempty"`
	Progress    int        `json:"progress"` // 0-100

	// Progress details
	CurrentStep   string `json:"current_step,omitempty"` // script_generation, entity_extraction, clip_search, clip_download, clip_upload, completed
	ClipsFound    int    `json:"clips_found"`
	ClipsMissing  int    `json:"clips_missing"`
	TotalClips    int    `json:"total_clips"`
	CurrentEntity string `json:"current_entity,omitempty"`

	// Result (solo quando completed)
	Result *scriptclips.ScriptClipsResponse `json:"result,omitempty"`
}

// AsyncPipelineService gestisce l'esecuzione asincrona della pipeline
type AsyncPipelineService struct {
	service     *scriptclips.ScriptClipsService
	jobsDir     string
	mu          sync.RWMutex
	jobs        map[string]*PipelineJob
	cancelFuncs map[string]context.CancelFunc // Per cancellare job in esecuzione
}

// NewAsyncPipelineService crea un nuovo servizio
