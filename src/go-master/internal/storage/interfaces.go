// Package storage fornisce interfacce e implementazioni per la persistenza dei dati
package storage

import (
	"context"
	"time"

	"velox/go-master/pkg/models"
)

// JobStore definisce le operazioni di storage per i job
type JobStore interface {
	// LoadQueue carica la coda completa dei job
	LoadQueue(ctx context.Context) (*models.Queue, error)

	// SaveQueue salva la coda completa dei job
	SaveQueue(ctx context.Context, queue *models.Queue) error

	// GetJob recupera un job per ID
	GetJob(ctx context.Context, id string) (*models.Job, error)

	// SaveJob salva un job (crea o aggiorna)
	SaveJob(ctx context.Context, job *models.Job) error

	// DeleteJob elimina un job per ID
	DeleteJob(ctx context.Context, id string) error

	// ListJobs restituisce una lista di job filtrati
	ListJobs(ctx context.Context, filter models.JobFilter) ([]*models.Job, error)

	// GetNextPendingJob restituisce il prossimo job in stato pending
	GetNextPendingJob(ctx context.Context) (*models.Job, error)

	// UpdateJobStatus aggiorna lo stato di un job
	UpdateJobStatus(ctx context.Context, id string, status models.JobStatus, errorMsg string) error

	// AssignJobToWorker assegna un job a un worker
	AssignJobToWorker(ctx context.Context, jobID, workerID string) error

	// CompleteJob marca un job come completato con il risultato
	CompleteJob(ctx context.Context, jobID string, result *models.JobResult) error

	// IncrementJobRetries incrementa il contatore dei retry di un job
	IncrementJobRetries(ctx context.Context, jobID string) error

	// LogJobEvent registra un evento per un job
	LogJobEvent(ctx context.Context, event *models.JobEvent) error

	// GetJobEvents restituisce gli eventi di un job
	GetJobEvents(ctx context.Context, jobID string, limit int) ([]*models.JobEvent, error)
}

// WorkerStore definisce le operazioni di storage per i worker
type WorkerStore interface {
	// LoadWorkers carica tutti i worker registrati
	LoadWorkers(ctx context.Context) (map[string]*models.Worker, error)

	// SaveWorkers salva tutti i worker
	SaveWorkers(ctx context.Context, workers map[string]*models.Worker) error

	// GetWorker recupera un worker per ID
	GetWorker(ctx context.Context, id string) (*models.Worker, error)

	// SaveWorker salva un worker (crea o aggiorna)
	SaveWorker(ctx context.Context, worker *models.Worker) error

	// DeleteWorker elimina un worker per ID
	DeleteWorker(ctx context.Context, id string) error

	// GetActiveWorkers restituisce tutti i worker attivi (online)
	GetActiveWorkers(ctx context.Context, timeout time.Duration) (map[string]*models.Worker, error)

	// GetWorkersByCapability restituisce i worker con una specifica capability
	GetWorkersByCapability(ctx context.Context, cap models.WorkerCapability) ([]*models.Worker, error)

	// UpdateWorkerStatus aggiorna lo stato di un worker
	UpdateWorkerStatus(ctx context.Context, id string, status models.WorkerStatus) error

	// UpdateWorkerJob assegna o rimuove un job da un worker
	UpdateWorkerJob(ctx context.Context, workerID, jobID string) error

	// TouchWorker aggiorna il timestamp last_seen di un worker
	TouchWorker(ctx context.Context, id string) error

	// GetAvailableWorkers restituisce i worker disponibili per accettare job
	GetAvailableWorkers(ctx context.Context) ([]*models.Worker, error)
}

// QueueStore definisce le operazioni specifiche per la gestione della coda
type QueueStore interface {
	// GetQueueStats restituisce le statistiche della coda
	GetQueueStats(ctx context.Context) (QueueStats, error)

	// GetJobsByStatus restituisce tutti i job con uno specifico stato
	GetJobsByStatus(ctx context.Context, status models.JobStatus) ([]*models.Job, error)

	// GetJobsByWorker restituisce tutti i job assegnati a un worker
	GetJobsByWorker(ctx context.Context, workerID string) ([]*models.Job, error)

	// RequeueFailedJobs rimette in coda i job falliti che possono essere riprovati
	RequeueFailedJobs(ctx context.Context) (int, error)

	// CleanupOldJobs elimina i job vecchi oltre una certa data
	CleanupOldJobs(ctx context.Context, before time.Time) (int, error)

	// ArchiveCompletedJobs archivia i job completati in una directory separata
	ArchiveCompletedJobs(ctx context.Context, before time.Time) (int, error)
}

// QueueStats contiene le statistiche della coda
type QueueStats struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	Processing int `json:"processing"`
	Completed  int `json:"completed"`
	Failed     int `json:"failed"`
	Cancelled  int `json:"cancelled"`
	Paused     int `json:"paused"`
}

// StateManager gestisce lo stato globale con accesso thread-safe
type StateManager interface {
	// GetActiveWorkersState restituisce i worker attivi (versione StateManager)
	GetActiveWorkersState(ctx context.Context) map[string]*models.Worker

	// UpdateWorkerStatus aggiorna lo stato di un worker
	UpdateWorkerStatus(ctx context.Context, id string, status models.WorkerStatus) error

	// GetWorkerStatus restituisce lo stato corrente di un worker
	GetWorkerStatus(ctx context.Context, id string) (models.WorkerStatus, error)

	// AcquireLock acquisisce un lock distribuito
	AcquireLock(ctx context.Context, name string, ttl time.Duration) (Lock, error)

	// GetGlobalState restituisce lo stato globale completo
	GetGlobalState(ctx context.Context) (*GlobalState, error)

	// UpdateGlobalState aggiorna lo stato globale
	UpdateGlobalState(ctx context.Context, state *GlobalState) error

	// Watch chiude un canale per ricevere aggiornamenti di stato
	Watch(ctx context.Context) (<-chan StateChange, error)
}

// Lock rappresenta un lock acquisito
type Lock interface {
	// Release rilascia il lock
	Release() error

	// Refresh rinnova il lock
	Refresh(ttl time.Duration) error

	// IsValid verifica se il lock è ancora valido
	IsValid() bool
}

// GlobalState rappresenta lo stato globale del sistema
type GlobalState struct {
	Workers      map[string]*models.Worker `json:"workers"`
	ActiveJobs   int                       `json:"active_jobs"`
	PendingJobs  int                       `json:"pending_jobs"`
	LastUpdated  time.Time                 `json:"last_updated"`
	Version      int64                     `json:"version"`
	MasterNodeID string                    `json:"master_node_id"`
}

// StateChange rappresenta un cambiamento di stato
type StateChange struct {
	Type      ChangeType  `json:"type"`
	EntityID  string      `json:"entity_id"`
	OldValue  interface{} `json:"old_value,omitempty"`
	NewValue  interface{} `json:"new_value,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// ChangeType rappresenta il tipo di cambiamento
type ChangeType string

const (
	ChangeWorkerStatus  ChangeType = "worker_status"
	ChangeJobStatus     ChangeType = "job_status"
	ChangeJobAssigned   ChangeType = "job_assigned"
	ChangeWorkerAdded   ChangeType = "worker_added"
	ChangeWorkerRemoved ChangeType = "worker_removed"
)

// Storage è l'interfaccia principale che combina tutti gli store
type Storage interface {
	JobStore
	WorkerStore
	QueueStore
	StateManager

	// Close chiude tutte le connessioni e rilascia le risorse
	Close() error

	// HealthCheck verifica lo stato dello storage
	HealthCheck() error

	// Backup crea un backup dei dati
	Backup(dest string) error

	// Restore ripristina i dati da un backup
	Restore(src string) error
}