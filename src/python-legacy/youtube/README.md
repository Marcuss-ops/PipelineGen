# YouTube Modules Architecture

This directory contains the modularized backend logic for YouTube integration, following Clean Architecture principles.

## Structure

### `core/`
Contains pure Python business logic and domain services.
- **`uploads_service.py`**: The main entry point for upload operations, caching, and data enrichment. It orchestrates the flow between the repository and the API adapter.

### `infrastructure/`
Contains the implementation details for external dependencies.
- **`repository.py`**: Handles database interactions (VideoUploadStorage) and internal channel data retrieval.
- **`youtube_api.py`**: wrappers for direct YouTube API calls (using `youtube_uploader` scripts).

### `models/`
Contains Data Transfer Objects (DTOs) and Pydantic models.
- **`dtos.py`**: Request/Response models shared between routes and services.

## Usage

Routes should strictly use the `UploadsService` (via Dependency Injection or factory) and DTOs.
Avoid importing `infrastructure` classes directly in the routes if possible, to maintain separation of concerns.

## Adding New Features
1. Define the Request/Response model in `models/dtos.py`.
2. Implement the business logic in `core/uploads_service.py`.
3. If new DB/API access is needed, add methods to `infrastructure/repository.py` or `infrastructure/youtube_api.py`.
4. Expose via `routes/youtube/uploads.py`.
