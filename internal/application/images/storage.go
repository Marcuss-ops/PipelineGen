package images

// Storage concerns are split across domain-specific files:
//
//   - storage_service.go        — ImageStorageService struct + constructor
//   - storage_download.go       — download + ingest + filename helpers
//   - storage_drive.go          — Google Drive upload/export
//   - storage_file.go           — local filesystem read/write
//   - storage_bridge.go         — service bridge / wiring
//   - storage_ingest_direct.go  — direct-ingest path
//
// Search functions live in search.go (split from storage_search.go
// per PR-STORAGE-SEARCH-SPLIT, July 2026).
