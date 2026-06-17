# Asset Operations (assetop)

The `assetop` package defines the low-level operations and policies that govern the lifecycle of an asset. It provides the building blocks for deduplication, uploading, and reconciliation.

## Policies

The system is highly configurable via policies defined in `policy.go`:

- **DuplicatePolicy**: Controls how the system identifies existing assets. It can check by file hash, Drive file ID, or filename.
- **UploadPolicy**: Configures the behavior of the Google Drive uploader, including strategies, retries, and timeouts.
- **PersistPolicy**: Determines which data stores should be updated when an asset is finalized (Central Registry, Asset Index, or Domain-specific DB).
- **ReconcilePolicy**: Defines how to handle discrepancies between the database and cloud storage (e.g., if a file is deleted from Drive).

## Services

### DedupeService
Handles the logic of querying an `AssetRecordStore` to find duplicates based on the active `DuplicatePolicy`.

### Uploader
A wrapper around the Google Drive API specialized for uploading media files and returning standardized links and file IDs.

### ReconcileService
Provides functionality to synchronize the state of the database with the reality of Google Drive. It can identify "orphaned" database records whose cloud files have been removed.

## Interfaces

### AssetRecordStore
An interface used by `DedupeService` to search for existing assets. This allows the deduplication logic to work across different repository implementations.
