# API Package

The `api` package provides the HTTP server and routing infrastructure for PipelineGen. It is built on top of the **Gin** web framework and uses a modular approach for route registration.

## Core Components

### Server
The `Server` struct manages the lifecycle of the HTTP server. It handles:
- Initialization with configuration and module registry.
- Graceful shutdown with configurable timeouts.
- Signal handling (SIGINT, SIGTERM).

### Router
The `Router` struct is responsible for:
- Configuring global middleware (Logging, Recovery, Gzip).
- Building and applying CORS policies.
- Serving static assets (media assets and the React-based web-admin).
- Managing protected API routes through authentication and rate limiting.
- **Dynamic Route Registration**: Leveraging the `module.Registry` to allow different system modules to register their own API endpoints.

## Middleware

The API uses several specialized middlewares:
- **Auth**: Validates administrative tokens for protected endpoints.
- **RateLimit**: Protects against abuse using a token bucket algorithm.
- **WorkspaceScope**: Manages data isolation by scoping requests to specific workspace contexts.
- **Logger/Recovery**: Standard Gin middleware for request tracing and crash protection.

## Static Serving

- `/assets`: Serves media files from the data directory.
- `/admin`: Serves the built React application for system administration.
- `/health`: Public endpoint for system health monitoring.

## Modular Routing

Routes are not hardcoded in the `Router`. Instead, they are registered via the `module.Registry`. Each module (e.g., `Artlist`, `YouTube`, `Jobs`) defines its own `RegisterRoutes` method, which is called during server initialization. This allows for a flexible and extensible API structure.
