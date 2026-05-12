# Lifecycle Core Service

The `lifecycle` package orchestrates the end-to-end processing of media assets. It sits at a higher level than the individual services like `mediaasset` or `assetregistry`, acting as the "brain" that coordinates deduplication, cloud uploads, and database persistence.

## Responsibility

The `Service` in this package is responsible for:
1.  **Deduplication**: Using `assetop.DedupeService` to check if an asset already exists in the system based on file hash, ID, or filename.
2.  **Cloud Upload**: Using `assetop.Uploader` to synchronize local files with Google Drive if required by policy.
3.  **Persistence**: Using `assetregistry.Finalizer` to ensure the asset is correctly recorded in both the primary database and the `asset_index`.
4.  **Reconciliation**: Using `assetop.ReconcileService` to find and fix inconsistencies between local storage, databases, and cloud storage.

## Key Methods

### ProcessAsset
The primary entry point for finalizing a downloaded and processed asset.
- Checks the deduplication policy.
- Performs Google Drive upload if enabled.
- Finalizes the record in the registry.

### Reconcile
Triggers a reconciliation process to find assets that are in the database but missing from Google Drive (or vice versa) and attempts to fix them.

## Policies
The service's behavior is heavily controlled by policies defined in `internal/core/assetop`:
- `DuplicatePolicy`: How to handle existing assets (skip, replace, etc.).
- `UploadPolicy`: Whether and where to upload files to Google Drive.
- `PersistPolicy`: Which databases to update.
- `ReconcilePolicy`: How to handle reconciliation.

## Data Flow
1.  **Input**: `FinalizeInput` containing metadata and local path.
2.  **Dedupe**: Query `AssetRecordStore` for existing records.
3.  **Upload**: (Optional) Transfer file to Google Drive.
4.  **Finalize**: Write to `Registry` and `AssetIndex`.
5.  **Output**: `FinalizeResult` with the final status and links.
