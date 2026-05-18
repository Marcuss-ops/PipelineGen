# Asset Registry Service

The `assetregistry` package provides a unified interface for managing media assets across different storage backends and repositories. It serves as the central point for asset lifecycle management, including registration, verification, and conversion between different data models.

## Core Components

### Registry Interface
The `Registry` interface defines the standard operations for interacting with asset storage:
- `UpsertMedia`: Registers or updates an asset.
- `GetMedia`: Retrieves an asset by its unique identifier.
- `DeleteMedia`: Removes an asset from the registry.
- `GetAllWithDriveFileID`: Lists all assets that have an associated Google Drive file ID.
- `FindByPHash`: Searches for an asset using its Perceptual Hash (PHash) for deduplication.

### MediaRecord
The `MediaRecord` struct is the canonical representation of an asset within the registry. It consolidates fields from various sources (Artlist, YouTube, local storage) into a unified model.

### Finalizer
The `Finalizer` service handles the "finalization" of an asset, which includes:
- Verifying the existence of local files.
- Ensuring file hashes are calculated.
- Checking for the presence of Drive links.
- Updating the primary database and the `asset_index`.

### Source Resolver
The `SourceResolver` is a utility that maps high-level "source" identifiers (e.g., "artlist", "youtube", "stock") to their respective underlying repositories.

## Usage

```go
resolver := assetregistry.NewSourceResolver(artlistRepo, clipsRepo, stockRepo)
repo := resolver.ResolveRepo("artlist")
```

## Converters
The package includes converters to transform records from specialized repositories (like `voiceovers` or `images`) into the canonical `MediaRecord` or `models.Clip` formats.
