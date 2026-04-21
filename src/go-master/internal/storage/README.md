# Storage Package - Agente 2

Questo package implementa lo **Storage & Persistence Layer** per il sistema VeloxEditing Backend Go.

## Responsabilità

Come Agente 2, questo package è responsabile di:

- **Persistenza JSON** (compatibilità con Python)
- **SQLite database** (migrazione futura)
- **Stato globale** management con `sync.RWMutex`
- **Backup e recovery**
- **Migration tools** (da JSON a SQLite)

## Struttura

```
internal/storage/
├── interfaces.go          # Interfacce principali (JobStore, WorkerStore, QueueStore, StateManager)
├── factory.go             # Factory per creare istanze di storage
├── README.md             # Questo file
└── jsondb/               # Implementazione JSON
    ├── job_store.go      # Storage per i job
    ├── worker_store.go   # Storage per i worker
    ├── queue_store.go    # Gestione coda
    ├── state_manager.go  # Gestione stato globale
    ├── storage.go        # Implementazione completa Storage interface
    ├── job_store_test.go # Test per JobStore
    └── worker_store_test.go # Test per WorkerStore
```

## Interfacce Principali

### JobStore
```go
type JobStore interface {
    LoadQueue() (*models.Queue, error)
    SaveQueue(queue *models.Queue) error
    GetJob(id string) (*models.Job, error)
    SaveJob(job *models.Job) error
    DeleteJob(id string) error
    ListJobs(filter models.JobFilter) ([]*models.Job, error)
    GetNextPendingJob() (*models.Job, error)
    UpdateJobStatus(id string, status models.JobStatus, error string) error
    AssignJobToWorker(jobID, workerID string) error
    CompleteJob(jobID string, result *models.JobResult) error
    IncrementJobRetries(jobID string) error
}
```

### WorkerStore
```go
type WorkerStore interface {
    LoadWorkers() (map[string]*models.Worker, error)
    SaveWorkers(workers map[string]*models.Worker) error
    GetWorker(id string) (*models.Worker, error)
    SaveWorker(worker *models.Worker) error
    DeleteWorker(id string) error
    GetActiveWorkers(timeout time.Duration) (map[string]*models.Worker, error)
    GetWorkersByCapability(cap models.WorkerCapability) ([]*models.Worker, error)
    UpdateWorkerStatus(id string, status models.WorkerStatus) error
    UpdateWorkerJob(workerID, jobID string) error
    TouchWorker(id string) error
    GetAvailableWorkers() ([]*models.Worker, error)
}
```

### StateManager
```go
type StateManager interface {
    GetActiveWorkers() map[string]*models.Worker
    UpdateWorkerStatus(id string, status models.WorkerStatus) error
    GetWorkerStatus(id string) (models.WorkerStatus, error)
    AcquireLock(name string, ttl time.Duration) (Lock, error)
    GetGlobalState() (*GlobalState, error)
    UpdateGlobalState(state *GlobalState) error
    Watch(ctx context.Context) (<-chan StateChange, error)
}
```

## Utilizzo

### Creazione Storage

```go
import "velox/go-master/internal/storage"

// Con configurazione di default (JSON)
store, err := storage.NewDefaultStorage()
if err != nil {
    log.Fatal(err)
}
defer store.Close()

// Con configurazione personalizzata
config := &storage.Config{
    Type:    storage.StorageTypeJSON,
    DataDir: "./mydata",
}
store, err := storage.NewStorage(config)
```

### Operazioni sui Job

```go
// Creare un nuovo job
job := models.NewJob(models.TypeVideoGeneration, map[string]interface{}{
    "title": "My Video",
    "duration": 60,
})

// Salvare il job
err := store.SaveJob(job)

// Recuperare un job
job, err := store.GetJob("job_123")

// Aggiornare lo stato
err := store.UpdateJobStatus(job.ID, models.StatusProcessing, "")

// Assegnare a un worker
err := store.AssignJobToWorker(job.ID, "worker-1")

// Completare un job
result := &models.JobResult{
    Success:  true,
    VideoURL: "https://example.com/video.mp4",
}
err := store.CompleteJob(job.ID, result)

// Ottenere il prossimo job pending
nextJob, err := store.GetNextPendingJob()
```

### Operazioni sui Worker

```go
// Registrare un worker
worker := models.NewWorker(
    "worker-1",
    "Worker One",
    "192.168.1.100",
    8080,
    []models.WorkerCapability{models.CapVideoGeneration, models.CapFFmpeg},
)
err := store.SaveWorker(worker)

// Aggiornare heartbeat
err := store.TouchWorker(worker.ID)

// Ottenere worker disponibili
available, err := store.GetAvailableWorkers()

// Ottenere worker per capability
videoWorkers, err := store.GetWorkersByCapability(models.CapVideoGeneration)

// Assegnare job a worker
err := store.UpdateWorkerJob(worker.ID, job.ID)
```

### Gestione Stato

```go
// Ottenere stato globale
state, err := store.GetGlobalState()

// Acquisire lock
lock, err := store.AcquireLock("job-processing", 30*time.Second)
if err != nil {
    // Lock già acquisito
}
defer lock.Release()

// Watch per cambiamenti
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

changes, err := store.Watch(ctx)
for change := range changes {
    log.Printf("Change: %s - %s", change.Type, change.EntityID)
}
```

## Thread Safety

Tutte le operazioni sono **thread-safe** grazie all'uso di `sync.RWMutex`:

- **Letture**: Usano `RLock()` - multiple goroutine possono leggere contemporaneamente
- **Scritture**: Usano `Lock()` - accesso esclusivo

## Formato File JSON

### queue.json
```json
{
  "jobs": [
    {
      "id": "job_1234567890_abc123",
      "type": "video_generation",
      "status": "pending",
      "priority": 1,
      "created_at": "2024-01-15T10:30:00Z",
      "updated_at": "2024-01-15T10:30:00Z",
      "payload": {
        "title": "My Video"
      },
      "retries": 0,
      "max_retries": 3
    }
  ],
  "updated_at": "2024-01-15T10:30:00Z",
  "version": 1
}
```

### workers.json
```json
{
  "workers": {
    "worker-1": {
      "id": "worker-1",
      "name": "Worker One",
      "status": "idle",
      "capabilities": ["video_generation", "ffmpeg"],
      "host": "192.168.1.100",
      "port": 8080,
      "last_seen": "2024-01-15T10:30:00Z",
      "stats": {
        "jobs_completed": 10,
        "jobs_failed": 1
      }
    }
  },
  "updated_at": "2024-01-15T10:30:00Z",
  "version": 1
}
```

## Backup e Restore

```go
// Creare backup
err := store.Backup("./backups")

// Restore da backup
err := store.Restore("./backups/backup_20240115_103000")
```

## Testing

```bash
cd src/go-master
go test ./internal/storage/jsondb/... -v
```

## Compatibilità Python

Il formato JSON è compatibile con il sistema Python esistente:
- I campi usano `snake_case`
- I timestamp sono in formato RFC3339
- Gli enum sono salvati come stringhe

## Roadmap

- [x] Implementazione JSON completa
- [ ] Implementazione SQLite
- [ ] Migration tool da JSON a SQLite
- [ ] Redis cache per multi-master
- [ ] Ottimizzazioni performance

## Note per gli Altri Agenti

### Agente 1 (Master Core)
```go
// Usare le interfacce, non le implementazioni concrete
import "velox/go-master/internal/storage"

func NewJobService(store storage.Storage) *JobService {
    return &JobService{storage: store}
}
```

### Agente 3 (Worker Client)
```go
// Lo storage è gestito dal Master, il Worker usa solo il client HTTP
```

### Agente 4 (Video Processing)
```go
// Genera file temporanei, lo storage gestisce i metadati
```

### Agente 5 (Integrations)
```go
// Upload risultati, lo storage salva i riferimenti (fileID, videoURL)