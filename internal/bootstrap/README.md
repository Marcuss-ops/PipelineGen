# Bootstrap Package

The `bootstrap` package is responsible for the "wiring" and initialization of the entire PipelineGen system. It implements the **Dependency Injection (DI)** pattern (manually or via `wire`) to instantiate repositories, services, and handlers.

## Responsibility

1.  **Dependency Resolution**: Creating instances of all required services and connecting them.
2.  **Configuration Application**: Passing configuration values to the appropriate components.
3.  **Lifecycle Management**: Setting up background services, watchers, and periodic tasks.
4.  **Database Initialization**: Opening connections and running migrations.

## Core Components

### CoreDeps
A central struct that holds the "world" — all major service instances and database connections. This allows for passing a single bundle of dependencies between initialization steps.

### ServiceManager
Manages the concurrent lifecycle of background services. It ensures that when the system starts, all background processes (like Drive sync or orphan cleanup) are launched correctly, and when it stops, they are shut down gracefully.

### Wiring Logic
- `databases.go`: Opens all isolated SQLite databases.
- `registry.go`: Initializes the module registry and adds all active modules.
- `lifecycle.go`: Sets up the core asset lifecycle service.
- `media.go`, `artlist.go`, `youtubeclip.go`, etc.: Specialized wiring for feature-specific services.

## Initialization Flow (ExportInitCore)

The standard startup sequence follows these steps:
1.  **Logger Initialization**: Setting up the global logger.
2.  **Database Opening**: Connecting to all SQLite files.
3.  **Migration Running**: Ensuring all schemas are up to date.
4.  **Service Wiring**: Recursively creating all services (Repositories -> Services -> Handlers).
5.  **Module Registration**: Adding feature modules to the registry.
6.  **Background Launch**: Starting long-lived tasks via the `ServiceManager`.

## Testing

The package includes `wire_test.go` and `bootstrap_test.go` to ensure that the complex dependency graph can be built correctly even with minimal or mock dependencies, preventing runtime panics during startup.
