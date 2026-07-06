// Package indexing — payload_mapper_document.go: IndexDocument + Payload construction.
//
// Former monolithic file (344 LOC) decomposed July 2026 per AGENTS.md Pattern 5:
//
//   - payload_builder.go   — BuildPayloadFromDocument (canonical writer-side payload builder)
//   - index_airlock.go     — assetToIndexDocumentNoValidate, domainAssetLifecycle,
//     AssetToIndexDocument (AssetData → IndexDocument airlock)
//   - index_to_point.go    — IndexDocumentToPoint (IndexDocument → Qdrant schema.Point wire shaping)
//
// This file is intentionally empty — it serves as a landing page for the split topology.
package indexing
