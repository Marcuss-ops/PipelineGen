// Package metadataexport — v1 envelope + on-disk schema for
// asset.metadata_export.requested events.
//
// Step 2 of the post-architettura 2026 plan (June 2026): the contract
// was previously embedded inline in
// the former outbox metadata export implementation (395 lines mixing
// contract + validator + handler + SQL queries + FS writers). The
// split lifts the contract to its own file so the application package
// has zero infra-shaped imports (database/sql, os, etc.) per AGENTS.md
// Pattern 8.
//
// The contract is the SCHEMA for two distinct surfaces:
//
//  1. The INPUT  envelope: env.MetadataExportRequest — JSON marshalled
//     to disk or wired into the outbox_events.payload. Producers
//     upgrade the schema by bumping the version literal; handlers
//     reject mismatches as terminal (ErrMetadataTerminal) so retry
//     storming is impossible.
//
//  2. The OUTPUT snapshot: type snapshot below — the on-disk JSON
//     schema for one asset's sidecar file. Stays stable across input
//     envelope upgrades; downstream tooling can rely on its shape.
//
// Neither surface uses gRPC, Protobuf, or Cap'n Proto — the project
// keeps the JSON sidecars for human- and grep-friendliness.
package metadataexport

import (
	"errors"
)

// metadataExportSchemaVersion is the LITERAL string that incoming
// asset.metadata_export.requested envelopes MUST carry. Strict match
// — producers bump this on schema changes and the handler rejects with
// ErrMetadataTerminal so the outbox pool dead-letters events that
// retry won't fix. Mirrors the IndexRequestSchemaVersion pattern in
// internal/capabilities/jobs/outbox/indexing.go.
const metadataExportSchemaVersion = "asset.metadata_export.requested.v1"

// MetadataExportRequestSchemaVersion is the public contract marker shared by
// producers and the technical outbox adapter. The event remains stable while
// the capability implementation moves out of jobs/outbox.
const MetadataExportRequestSchemaVersion = metadataExportSchemaVersion

// Format constants. Narrow allowlist: today's handler writes only JSON
// (per-asset sidecar), and jsonl/csv combined files only when the
// producer asks. Future PRs can wire real jsonl/csv emission without
// a schema bump — only the format literal in this file changes.
const (
	FormatJSON  = "json"
	FormatJSONL = "jsonl"
	FormatCSV   = "csv"
)

// Destination providers. Narrow allowlist. The drive provider is
// acked-and-logged (the deliver pipeline owns uploads); filesystem
// writes atomic .json sidecars. Add a new provider via a typed
// switch on handler.go's switch — never a string match in the
// validator.
const (
	DestinationFilesystem = "filesystem"
	DestinationDrive      = "drive"
)

// Include section allowlist. Strict — these are the only "shapes"
// downstream tooling can rely on. A typo in the producer's
// `include` array is REJECTED with ErrMetadataTerminal so the
// schema isn't silently widened.
const (
	IncludeTechnical  = "technical"
	IncludeProvenance = "provenance"
	IncludeTimeline   = "timeline"
	IncludeEntities   = "entities"
	IncludeDelivery   = "delivery"
)

// ErrMetadataTerminal signals a terminal failure that should NOT be
// retried. Examples: invalid format, invalid include section, terminal
// envelope validation. Tagged so a future pool classifier can route
// without changing the handler contract.
//
// The sentinel is package-exported so the composition root's
// IsTerminal classifier (if it grows one) can import the marker
// without a cross-package wrapper.
var ErrMetadataTerminal = errors.New("asset.metadata_export.requested: terminal error")

// MetadataExportRequest is the canonical v1 envelope.
//
// Required: schema_version, format, destination.provider.
//
// Strictly EITHER asset_ids (non-empty) OR job_id (non-empty) must
// resolve the export scope.
//
// Include is optional (defaults to {technical, provenance, delivery});
// the allowlist is enforced.
//
// destination.path (filesystem) / destination.folder_id (drive) is
// optional: filesystem missing → output goes to the handler's
// configured MetadataDir; drive missing → handler still acks.
//
// NEVER include tokens or credentials in this envelope.
type MetadataExportRequest struct {
	SchemaVersion string   `json:"schema_version"`
	EventID       string   `json:"event_id"`
	RequestedAt   string   `json:"requested_at,omitempty"` // RFC3339 UTC
	TraceID       string   `json:"trace_id,omitempty"`
	JobID         string   `json:"job_id,omitempty"`
	AssetIDs      []string `json:"asset_ids,omitempty"`
	Format        string   `json:"format"`            // json|jsonl|csv (allowlist)
	Include       []string `json:"include,omitempty"` // allowlist
	Destination   struct {
		Provider string `json:"provider"` // filesystem|drive
		Path     string `json:"path,omitempty"`
		FolderID string `json:"folder_id,omitempty"`
	} `json:"destination"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// snapshot is the on-disk JSON schema for one asset's sidecar
// (assetID.json). Package-private — external code reads the bytes
// not the type so the on-disk schema can evolve without a Go type
// migration. Sections is map[string]any so each include section
// (technical/provenance/…) shapes its data independently.
type snapshot struct {
	AssetID    string         `json:"asset_id"`
	ExportedAt string         `json:"exported_at"`
	Includes   []string       `json:"includes"`
	Sections   map[string]any `json:"sections"`
}
