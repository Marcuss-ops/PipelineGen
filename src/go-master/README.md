# VeloxEditing Go Master - Agent 1 (Job Master Core)

This is the **Agent 1** implementation of the VeloxEditing backend migration to Go. This agent is responsible for the Job Master Core, including HTTP API, routing, business logic for job and worker management, and background goroutines orchestration.

## 📋 Responsibilities

As per the migration document, Agent 1 handles:

- ✅ **HTTP Server** (Gin framework)
- ✅ **Routing and Handlers** for all endpoints
- ✅ **Middleware** (CORS, Gzip, auth, logging, rate limiting)
- ✅ **Business Logic Job Management** (CRUD job, scheduling)
- ✅ **Business Logic Worker Management** (registration, heartbeat, commands)
- ✅ **Background Goroutines** orchestration
- ✅ **API Documentation** (Swagger)

## 📁 Project Structure

```
go-master/
├── cmd/
│   └── server/
│       └── main.go                 # Entry point
├── internal/
│   ├── api/
│   │   ├── handlers/               # HTTP handlers
│   │   │   ├── job.go              # Job handlers
│   │   │   ├── worker.go           # Worker handlers
│   │   │   └── health.go           # Health check handlers
│   │   ├── middleware/             # HTTP middleware
│   │   │   └── middleware.go       # Logger, auth, rate limit
│   │   ├── routes.go               # Route configuration
│   │   └── server.go               # HTTP server setup
│   ├── core/
│   │   ├── job/
│   │   │   ├── interfaces.go       # Interfaces for other agents
│   │   │   └── service.go          # Job business logic
│   │   └── worker/
│   │       ├── interfaces.go       # Interfaces for other agents
│   │       └── service.go          # Worker business logic
├── pkg/
│   ├── models/                     # Shared data models
│   │   ├── job.go                  # Job models
│   │   └── worker.go               # Worker models
│   ├── config/                     # Configuration
│   │   └── config.go
│   └── logger/                     # Logging
│       └── logger.go
├── go.mod
└── README.md                       # This file
```

## 🔌 Interfaces for Other Agents

Agent 1 defines the following interfaces that other agents must implement:

### Agent 2 (Storage Layer) - MUST IMPLEMENT:

```go
// job.StorageInterface
type StorageInterface interface {
    LoadQueue(ctx context.Context) (*models.Queue, error)
    SaveQueue(ctx context.Context, queue *models.Queue) error
    GetJob(ctx context.Context, id string) (*models.Job, error)
    SaveJob(ctx context.Context, job *models.Job) error
    DeleteJob(ctx context.Context, id string) error
    ListJobs(ctx context.Context, filter models.JobFilter) ([]*models.Job, error)
    LogJobEvent(ctx context.Context, event *models.JobEvent) error
    GetJobEvents(ctx context.Context, jobID string, limit int) ([]*models.JobEvent, error)
}

// worker.StorageInterface  
type StorageInterface interface {
    LoadWorkers(ctx context.Context) (map[string]*models.Worker, error)
    SaveWorkers(ctx context.Context, workers map[string]*models.Worker) error
    GetWorker(ctx context.Context, id string) (*models.Worker, error)
    SaveWorker(ctx context.Context, worker *models.Worker) error
    DeleteWorker(ctx context.Context, id string) error
    SaveWorkerCommand(ctx context.Context, command *models.WorkerCommand) error
    GetWorkerCommands(ctx context.Context, workerID string) ([]*models.WorkerCommand, error)
    AckWorkerCommand(ctx context.Context, commandID string) error
    LoadRevokedWorkers(ctx context.Context) (map[string]bool, error)
    SaveRevokedWorkers(ctx context.Context, revoked map[string]bool) error
    LoadQuarantinedWorkers(ctx context.Context) (map[string]*QuarantineInfo, error)
    SaveQuarantinedWorkers(ctx context.Context, quarantined map[string]*QuarantineInfo) error
}
```

### Agent 4 (Video Processing) - MUST IMPLEMENT:

```go
type VideoProcessorInterface interface {
    GenerateVideo(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error)
    GetGenerationStatus(ctx context.Context, generationID string) (*GenerationStatus, error)
    CancelGeneration(ctx context.Context, generationID string) error
    ValidatePayload(payload map[string]interface{}) error
}
```

### Agent 5 (External Integrations) - MUST IMPLEMENT:

```go
type UploadServiceInterface interface {
    UploadToDrive(ctx context.Context, videoPath string, folderID string) (string, error)
    UploadToYouTube(ctx context.Context, videoPath string, metadata YouTubeMetadata) (string, error)
    CreateDriveFolder(ctx context.Context, name string, parentID string) (string, error)
    GetOrCreateDriveFolder(ctx context.Context, name string, parentID string) (string, error)
}

type ScriptGeneratorInterface interface {
    GenerateFromText(ctx context.Context, source, title, lang string, duration int) (string, error)
    GenerateFromYouTube(ctx context.Context, url, title, lang string, duration int) (string, error)
}
```

## 🚀 Getting Started

### Prerequisites

- Go 1.21 or higher
- Make (optional)

### Installation

```bash
# Clone the repository
cd /home/pierone/Pyt/VeloxEditing/refactored/go-master

# Download dependencies
go mod download

# Build the server
go build -o server ./cmd/server
```

### Running the Server

```bash
# Run with default configuration
./server

# Or with custom configuration
export VELOX_PORT=8000
export VELOX_HOST=0.0.0.0
export VELOX_LOG_LEVEL=debug
./server
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VELOX_HOST` | `0.0.0.0` | Server host |
| `VELOX_PORT` | `8000` | Server port |
| `VELOX_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `VELOX_LOG_FORMAT` | `json` | Log format (json, console) |
| `VELOX_DATA_DIR` | `./data` | Data directory |
| `VELOX_ENABLE_AUTH` | `false` | Enable authentication |
| `VELOX_ADMIN_TOKEN` | `` | Admin API token |
| `VELOX_MAX_PARALLEL_PER_PROJECT` | `2` | Max parallel jobs per project |

## 📚 API Documentation

Once the server is running, you can access the Swagger documentation at:

```
http://localhost:8000/api/docs/index.html
```

### Key Endpoints

#### Jobs
- `GET /api/jobs` - List jobs
- `POST /api/jobs` - Create job
- `GET /api/jobs/:id` - Get job
- `PUT /api/jobs/:id/status` - Update job status
- `DELETE /api/jobs/:id` - Delete job
- `POST /api/jobs/:id/assign` - Assign job to worker
- `POST /api/jobs/:id/lease` - Renew job lease

#### Workers
- `GET /api/workers` - List workers
- `POST /api/workers/register` - Register worker
- `GET /api/workers/:id` - Get worker
- `POST /api/workers/:id/heartbeat` - Worker heartbeat
- `GET /api/workers/:id/commands` - Get pending commands
- `POST /api/workers/:id/commands/:command_id/ack` - Ack command
- `POST /api/workers/:id/revoke` - Revoke worker
- `POST /api/workers/:id/quarantine` - Quarantine worker
- `POST /api/workers/:id/unquarantine` - Unquarantine worker
- `POST /api/workers/:id/command` - Send command to worker
- `POST /api/worker/poll` - Worker polling endpoint

#### Health & Status
- `GET /health` - Health check
- `GET /status` - Server status
- `GET /metrics` - Server metrics
- `GET /api/docs` - Swagger documentation

## 🧪 Testing

```bash
# Run tests
go test ./...

# Run with race detection
go test -race ./...

# Generate coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 🔗 Integration with Other Agents

### Integration with Agent 2 (Storage)

Agent 2 must implement the `StorageInterface` defined in:
- `internal/core/job/interfaces.go`
- `internal/core/worker/interfaces.go`

The current implementation uses a mock storage. To use the real storage:

```go
// In cmd/server/main.go, replace:
storage := NewMockStorage()

// With:
storage := agent2storage.NewJSONStorage(cfg.Storage.DataDir)
```

### Integration with Agent 4 (Video Processing)

Inject the video processor into the job service:

```go
videoProcessor := agent4video.NewProcessor(cfg)
jobService.SetVideoProcessor(videoProcessor)
```

### Integration with Agent 5 (External Integrations)

Inject the upload service:

```go
uploadService := agent5upload.NewService(cfg)
jobService.SetUploadService(uploadService)
```

## 📦 Checklist Pre-Consegna

- [x] Codice nel mio package compilato (`go build ./...`)
- [x] Interfacce che esporto sono documentate
- [x] Non ho import diretti da package di altri agenti (solo interfacce)
- [x] Non ho duplicato logica di altri agenti
- [x] README.md nel mio package spiega l'uso
- [ ] Unit tests (to be completed)
- [ ] Esempi di codice per interfacce pubbliche (to be completed)

## 🚨 Note per gli Altri Agenti

### Agente 2 (Storage)
- Implementare le interfacce in `internal/core/job/interfaces.go` e `internal/core/worker/interfaces.go`
- Mantenere compatibilità formato JSON con Python
- Assicurare thread-safety con `sync.RWMutex`

### Agente 3 (Worker)
- Usare l'endpoint `POST /api/worker/poll` per polling
- Implementare heartbeat con `POST /api/workers/:id/heartbeat`
- Gestire comandi ricevuti dal master

### Agente 4 (Video)
- Implementare `VideoProcessorInterface` in `internal/core/job/interfaces.go`
- Fornire metodi per generazione video, controllo stato, cancellazione

### Agente 5 (Integrazioni)
- Implementare `UploadServiceInterface` e `ScriptGeneratorInterface`
- Gestire OAuth2 per Google Drive/YouTube
- Implementare retry logic con exponential backoff

## 📝 License

Apache 2.0