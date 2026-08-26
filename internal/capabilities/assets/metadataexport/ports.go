// Package metadataexport — typed application-layer ports.
//
// Step 2 of the post-architettura 2026 plan (June 2026): the legacy
// the former outbox metadata export implementation reached directly
// into `database/sql` (for asset_id resolution + section loading) and
// into `os` (for FS writes) — both forbidden in the application
// layer per AGENTS.md Pattern 0 + Pattern 8. After split, this file
// declares the narrow surface so the handler depends ONLY on the two
// ports, not the concrete infra. The compile-time assertions live at
// the adapters (internal/platform/sqlite/metadataexport/
// + internal/infrastructure/files/metadataexport/).
//
// Two ports:
//
//   - AssetResolver  — narrow read surface for both the export-scope
//     resolution (given a job_id, return its asset_ids) and the
//     per-asset section loading (technical / provenance / delivery).
//     The legacy file inlined SQL queries for these; the new port
//     moves SQL into the SQLite adapter.
//
//   - ExportWriter  — narrow write surface for filesystem outputs.
//     Three format-specific methods plus an idempotent EnsureDir so
//     the service layer never reaches into "os" itself.
//
// Each port is the smallest surface this handler needs; expand it
// only when a new call site emerges.
package metadataexport

import (
)

import "context"

// AssetResolver is the typed-port surface the metadata export handler
// reads through. Two concerns:
//
//  1. ResolveAssetIDs — given a job_id (from the export envelope) and
//     any explicit asset_ids in the envelope, return the canonical
//     asset_id list the export should walk. The legacy implementation
//     queried outbox_events for aggregate_ids matching "asset.%". The
//     port returns the same value; only the SQL location moves.
//
//  2. Load<X>Section — load one section of the on-disk snapshot for
//     a given asset_id. Each section has its own column projection
//     so the resolvers don't pile onto a single mega-SELECT. Missing
//     rows are valid (return nil, nil) — section errors are
//     non-fatal at the snapshot layer; the caller logs and writes
//     "null" so the on-disk schema stays uniform.
//
// timeline + entities are intentionally NOT port methods: today's
// writers leave them as empty maps. When a future PR adds a real
// timeline/entities writer, the corresponding LoadXSection method
// lands here in the same PR.
type AssetResolver interface {
	// ResolveAssetIDs returns the asset_ids the export should walk.
	// Both inputs may be empty (signals: caller already supplied IDs
	// OR no job scope). The method merges them with job-scope taking
	// precedence when both are present (Audit-friendly ordering).
	ResolveAssetIDs(ctx context.Context, jobID string, explicitIDs []string) ([]string, error)

	// LoadTechnicalSection returns the technical-metadata block for
	// one asset (mediatype / source / Drive refs / quality).
	// Returns (nil, nil) for sql.ErrNoRows so the caller writes a
	// "null" placeholder in the snapshot.
	LoadTechnicalSection(ctx context.Context, assetID string) (map[string]any, error)

	// LoadProvenanceSection returns the minimal provenance block
	// (just source today; future PR adds ingestion lineage).
	LoadProvenanceSection(ctx context.Context, assetID string) (map[string]any, error)

	// LoadDeliverySection returns the 50-row delivery_log slice for
	// the asset, newest-first. Empty slice (not nil) when no rows
	// exist so the JSON shape stays stable.
	LoadDeliverySection(ctx context.Context, assetID string) ([]any, error)
}

// ExportWriter is the typed-port surface for filesystem outputs. The
// service layer (service.go) marshals JSON / JSONL / CSV bytes using
// the canonical stdlib encoders then hands the bytes to this writer;
// the writer owns FS-shaped concerns:
//
//   - EnsureDir  — idempotent mkdir -p with the canonical 0o755 perms.
//   - WriteJSON  — per-asset sidecar (assetID + ".json"), atomic via
//     .tmp + os.Rename inside the same directory
//     (POSIX-atomic on linux/macos).
//   - WriteJSONL — combine file (jobID + ".jsonl"), one encoded
//     object per call via json.Encoder.
//   - WriteCSV   — combine file (jobID + ".csv") with the canonical
//     header [asset_id, exported_at, includes| sections_json]
//   - caller-supplied rows.
//
// The 3 write methods are split even though they could share a single
// AtomicWrite primitive — keeping them distinct ensures future
// format-specific additions (e.g. Parquet) land as new methods on
// this port, not as new sibling writers with separate port sets.
type ExportWriter interface {
	// EnsureDir is idempotent: exists → no-op, missing → MkdirAll.
	EnsureDir(dir string) error

	// WriteJSON writes a single JSON document to dir + "/" + assetID
	// + ".json", atomically. body is the already-marshalled JSON.
	WriteJSON(dir string, assetID string, body []byte) error

	// WriteJSONL writes a JSONL sequence (one marshalled object per
	// element of items) to dir + "/" + jobID + ".jsonl", atomically.
	// Each entry is a complete JSON object that the writer encodes
	// verbatim (newline-delimited).
	WriteJSONL(dir string, jobID string, items [][]byte) error

	// WriteCSV writes a header row + caller-supplied data rows to
	// dir + "/" + jobID + ".csv", atomically. Each row is a []string;
	// the writer applies csv.Writer's standard quoting.
	WriteCSV(dir string, jobID string, header []string, rows [][]string) error
}
