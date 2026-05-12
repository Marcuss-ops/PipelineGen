# Services (internal/service)

The `service` package contains the business logic of PipelineGen. Services are higher-level components that coordinate between repositories, external APIs, and other services to fulfill specific domain requirements.

## Categories of Services

### Media Processing
- `mediaasset`: Core pipeline for downloading and normalizing video.
- `youtubeclip`: Specialized logic for extracting segments from YouTube videos.
- `audioasset`: Management and processing of sound effects and music.
- `images`: Sourcing and tagging of visual assets.

### Asset Management
- `assetregistry`: Centralized management and verification of all assets.
- `assetindex`: Global search and indexing across different asset sources.
- `assettree`: Hierarchical organization and navigation of assets.
- `assetstore`: Policy-based storage and deduplication.

### External Integrations
- `artlist`: Integration with Artlist.io for professional media harvesting.
- `drivecleanup`: Maintenance tasks for Google Drive storage.
- `drivereconcile`: Consistency checks between local database and cloud storage.

### Automation & Workflows
- `contentpackage`: Orchestration for creating full content packages (script + assets).
- `scriptjob`: Execution and tracking of script-to-video generation tasks.
- `scheduler`: Management of periodic tasks and background workers.
- `indexing`: Automated indexing and embedding generation for media.

### Matching & Discovery
- `match`: Logic for pairing script segments with appropriate media assets.
- `matchingconfig`: Configuration and scoring rules for the matching engine.
- `visualquery`: Advanced discovery using visual embeddings and PHash.

## Service Patterns

1.  **Dependency Injection**: Services receive their dependencies (repositories, other services) via their constructor.
2.  **Context-Aware**: All major service methods accept `context.Context` for cancellation and timeout management.
3.  **Statelessness**: Most services are designed to be stateless, delegating persistence to the `repository` layer.
4.  **Logging**: Deep integration with `uber-go/zap` for observability into complex background processes.
