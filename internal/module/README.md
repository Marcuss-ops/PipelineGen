# Module System

The `module` package provides a standardized way to extend PipelineGen with new features and services. It defines the `Module` interface and a `Registry` to manage their lifecycle.

## The Module Interface

Every feature in PipelineGen (Artlist, YouTube, Images, etc.) is implemented as a `Module`:

```go
type Module interface {
    Name() string
    Enabled(cfg *config.Config) bool
    RegisterRoutes(rg *gin.RouterGroup)
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}
```

- **Name**: Unique identifier for the module.
- **Enabled**: Logic to determine if the module should be active based on the system configuration.
- **RegisterRoutes**: Hook to attach API endpoints to the main router.
- **Start/Stop**: Lifecycle hooks for background tasks, watchers, or long-lived processes.

## Module Registry

The `Registry` acts as a central coordinator:
1.  **Discovery**: Collects all available modules.
2.  **Filtering**: Identifies which modules are enabled for the current run.
3.  **Routing**: Orchestrates the registration of all API endpoints.
4.  **Lifecycle**: Manages the concurrent startup and graceful shutdown of all active modules.

## Benefits of the Modular Approach

- **Decoupling**: Features can be developed and tested in isolation.
- **Pluggability**: New capabilities can be added by simply implementing the interface and adding them to the registry.
- **Clean Bootstrap**: The main application initialization stays clean and focused on infrastructure, while feature-specific logic lives within the modules.
- **Conditional Loading**: Modules can be completely disabled via configuration, reducing memory footprint and security surface area if certain features aren't needed.

## Core Modules

The system includes several built-in modules defined in `core_modules.go`:
- `ArtlistModule`: Handles integration with Artlist search and harvesting.
- `YouTubeModule`: Manages YouTube clip extraction and processing.
- `JobsModule`: Provides API access to background job status and logs.
- `AssetsModule`: Unified search and management across all asset repositories.
- `VoiceoverModule`: Integration with TTS and voiceover management.
- `ImagesModule`: Image sourcing and metadata management.
