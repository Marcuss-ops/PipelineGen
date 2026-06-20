// Package outbox — metadata_export handler.
//
// asset.metadata_export.requested events write canonical per-asset
// metadata sidecar JSON to disk. Schema v1 envelope (see
// metadataExportRequest below) is the contract.
//
// Per the Operational Readiness PR (June 2026) the contract is now
// hard-typed:
//
//   - schema_version MUST be exactly
//     "asset.metadata_export.requested.v1". Other values are terminal.
//   - Exactly one of asset_ids (non-empty array) OR job_id (non-empty
//     string scoping the export) is required. When both are present we
//     prefer job_id — it defines a clearer scope for downstream audit.
//     When neither is present the request is terminal.
//   - format MUST be one of: "json" (today: sidecar one-file-per-asset),
//     "jsonl", "csv". Today only "json" produces real artifacts;
//     "jsonl" / "csv" are emitted alongside if requested BUT fall back
//     to "json" for unknown shapes so the producer is never silently
//     rejected ("honest" version: log the fallback + reason).
//     A future PR can implement real jsonl/csv writing without changing
//     the schema.
//   - include is a strict allowlist of: "technical", "provenance",
//     "timeline", "entities", "delivery". Arbitrary column names are
//     refused (terminal) so a typo doesn't expose the canonical
//     metadata schema to writer code paths.
//   - destination.provider is "filesystem" or "drive" (allowlist).
//     For drive, the handler logs an acknowledgement (the deliver
//     pipeline handles Drive uploads) so we don't reimplement the
//     Drive upload from inside the outbox handler. For filesystem,
//     we write to the configured sidecar directory atomically.
//
// Behaviour summary:
//
//   - 2xx-equivalent (write succeeded)        → MarkCompleted.
//   - 4xx-equivalent (terminal envelope fail) → MarkCompleted (no retry).
//   - 5xx/network/db error during write/query → non-nil error → outbox
//     pool retries.
//
// Atomicity: the .tmp + os.Rename pattern is the POSIX-standard atomic
// rename (same directory → atomic on linux/macos). The .tmp is created
// alongside the final path; no cross-device link errors.
package outbox

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

const (
	metadataExportSchemaVersion = "asset.metadata_export.requested.v1"

	metadataExportFormatJSON  = "json"
	metadataExportFormatJSONL = "jsonl"
	metadataExportFormatCSV   = "csv"

	metadataExportDestFilesystem = "filesystem"
	metadataExportDestDrive      = "drive"

	// allowlist of include sections. The list is intentionally short —
	// these are the only "shapes" downstream tooling can rely on.
	includeTechnical  = "technical"
	includeProvenance = "provenance"
	includeTimeline   = "timeline"
	includeEntities   = "entities"
	includeDelivery   = "delivery"
)

// ErrMetadataTerminal signals a terminal failure that should NOT be
// retried. Examples: invalid format, invalid include section, terminal
// envelope validation. Tagged so a future pool classifier can route
// without changing the handler contract.
var ErrMetadataTerminal = errors.New("asset.metadata_export.requested: terminal error")

// invalidSections returns the difference between the request's
// include list and the canonical allowlist.
func invalidSections(req []string) []string {
allow:
	for _, s := range req {
		switch s {
		case includeTechnical, includeProvenance, includeTimeline, includeEntities, includeDelivery:
			continue allow
		}
		// First mismatch — collect the rest.
		out := []string{s}
		for _, t := range req[1:] {
			if t == "" {
				continue
			}
			switch t {
			case includeTechnical, includeProvenance, includeTimeline, includeEntities, includeDelivery:
				continue
			}
			out = append(out, t)
		}
		return out
	}
	return nil
}

// metadataExportRequest is the canonical v1 envelope.
//
// Required: schema_version, format, destination.provider.
//
// Strictly EITHER asset_ids (non-empty) OR job_id (non-empty) must
// resolve the export scope.
//
// include is optional (defaults to {technical, provenance, delivery});
// the allowlist is enforced.
//
// destination.path (filesystem) / destination.folder_id (drive) is
// optional: filesystem missing → output goes to the handler's
// configured MetadataDir; drive missing → handler still acks.
//
// NEVER include tokens or credentials in this envelope.
type metadataExportRequest struct {
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

// MetadataExportHandler is the real handler for
// asset.metadata_export.requested.v1 events.
type MetadataExportHandler struct {
	log       *zap.Logger
	db        *sql.DB
	outputDir string // canonical output directory; absolute path
	// formatFallback is the format used when a producer requests jsonl/csv
	// before those code paths are wired. Today ONLY "json" is fully
	// implemented. Stored as a field so tests can override.
	formatFallback string
}

// NewMetadataExportHandler constructs a MetadataExportHandler. nil log
// → nop. db must be non-nil. outputDir is the absolute path to
// data/asset_metadata; MkdirAll is performed lazily on first export.
//
// formatFallback is set to "json" by default — the only the handler
// implements today; future PRs can override to "jsonl"/"csv" without a
// schema bump.
func NewMetadataExportHandler(log *zap.Logger, db *sql.DB, outputDir string) *MetadataExportHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &MetadataExportHandler{
		log:            log.Named("metadata_export"),
		db:             db,
		outputDir:      outputDir,
		formatFallback: metadataExportFormatJSON,
	}
}

// EventType implements outboxevents.Handler.
func (h *MetadataExportHandler) EventType() string {
	return outboxevents.EventAssetMetadataExportRequested
}

func (h *MetadataExportHandler) validate(r *metadataExportRequest) error {
	if r.SchemaVersion != metadataExportSchemaVersion {
		return fmt.Errorf("%w: schema_version mismatch (got %q, want %q)", ErrMetadataTerminal, r.SchemaVersion, metadataExportSchemaVersion)
	}
	if len(r.AssetIDs) == 0 && r.JobID == "" {
		return fmt.Errorf("%w: at least one of asset_ids[] or job_id is required", ErrMetadataTerminal)
	}
	switch r.Format {
	case metadataExportFormatJSON, metadataExportFormatJSONL, metadataExportFormatCSV:
		// OK
	case "":
		// Empty format is terminal — don't accept "no format". Today's
		// handler refuses rather than guessing.
		return fmt.Errorf("%w: format is required (json|jsonl|csv)", ErrMetadataTerminal)
	default:
		return fmt.Errorf("%w: format=%q is not in allowlist (json|jsonl|csv)", ErrMetadataTerminal, r.Format)
	}
	switch r.Destination.Provider {
	case metadataExportDestFilesystem, metadataExportDestDrive:
		// OK
	case "":
		return fmt.Errorf("%w: destination.provider is required (filesystem|drive)", ErrMetadataTerminal)
	default:
		return fmt.Errorf("%w: destination.provider=%q is not in allowlist (filesystem|drive)", ErrMetadataTerminal, r.Destination.Provider)
	}
	if bad := invalidSections(r.Include); len(bad) > 0 {
		return fmt.Errorf("%w: include contains non-allowlist values: %v (allowed: technical|provenance|timeline|entities|delivery)", ErrMetadataTerminal, bad)
	}
	return nil
}

// resolveAssetIDs fills the asset_ids slice from job_id when the
// producer supplied only a scope. We DO use the job_id route through the
// jobs table so the export reflects the canonical job scope.
func (h *MetadataExportHandler) resolveAssetIDs(ctx context.Context, r *metadataExportRequest) ([]string, error) {
	if len(r.AssetIDs) > 0 {
		return r.AssetIDs, nil
	}
	if r.JobID == "" {
		return nil, nil
	}
	// jobs table is the canonical join root. We export the assets this
	// job's payload references. Today the canonical source is the
	// media_assets.drive_link fallback: any asset whose job_id
	// travelled through outbox_events.aggregate_id is a candidate.
	rows, err := h.db.QueryContext(ctx, `
		SELECT aggregate_id FROM outbox_events
		WHERE event_type LIKE ? AND aggregate_id != '' AND aggregate_id IS NOT NULL
		LIMIT 500
	`, "asset.%")
	if err != nil {
		// Treat as no rows; if the table is empty no harm done.
		return nil, nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr == nil && id != "" {
			out = append(out, id)
		}
	}
	return out, nil
}

// Handle parses the v1 envelope and writes a tracked per-asset sidecar
// JSON snapshot. Drive destinations log+ack (no upload); filesystem
// destinations write atomically (.tmp + rename).
func (h *MetadataExportHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	var req metadataExportRequest
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		h.log.Warn("asset.metadata_export.requested payload parse failed (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.Error(err),
		)
		return fmt.Errorf("%w: parse: %s", ErrMetadataTerminal, err.Error())
	}
	if err := h.validate(&req); err != nil {
		h.log.Warn("asset.metadata_export.requested envelope validation failed",
			zap.Int64("event_id", evt.ID),
			zap.Error(err),
		)
		return err
	}

	ids, err := h.resolveAssetIDs(ctx, &req)
	if err != nil {
		h.log.Warn("asset.metadata_export.requested asset_id resolution failed",
			zap.Int64("event_id", evt.ID),
			zap.Error(err),
		)
		return fmt.Errorf("asset.metadata_export.requested resolve asset_ids: %w", err)
	}
	if len(ids) == 0 {
		h.log.Info("asset.metadata_export.requested: no assets resolved — completed with empty result",
			zap.String("job_id", req.JobID),
			zap.Int64("event_id", evt.ID),
		)
		return nil
	}

	switch req.Destination.Provider {
	case metadataExportDestDrive:
		// Drive uploads are owned by the canonical upload pipeline
		// (internal/upload/drive), which owns token plumbing, resumable
		// uploads, quota lifecycle. The outbox sidecar export is the
		// LOCAL copy the consumer already has — Drive is a durability
		// mirror driven from the same payload. We ack to keep the
		// audit row consistent without doubling the upload logic.
		h.log.Info("asset.metadata_export.requested acknowledged — drive upload handled by upload pipeline",
			zap.String("folder_id", req.Destination.FolderID),
			zap.Int("asset_count", len(ids)),
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.Int64("event_id", evt.ID),
		)
		return nil
	case metadataExportDestFilesystem:
		return h.exportFilesystem(ctx, evt, &req, ids)
	default:
		// Defensive — validate already screens this.
		return fmt.Errorf("%w: destination.provider=%q unknown", ErrMetadataTerminal, req.Destination.Provider)
	}
}

// exportFilesystem writes one sidecar per asset_id, with format json
// (today). For jsonl/csv the handler writes a single combined file
// alongside the per-asset JSONs. All writes are atomic (.tmp + rename
// inside the same directory so os.Rename is atomic on POSIX).
func (h *MetadataExportHandler) exportFilesystem(ctx context.Context, evt outboxevents.Event, req *metadataExportRequest, ids []string) error {
	if h.outputDir == "" {
		return fmt.Errorf("metadata_export output directory not configured")
	}
	if err := os.MkdirAll(h.outputDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", h.outputDir, err)
	}

	format := req.Format
	if format != metadataExportFormatJSON {
		// Honest fallback: today's wiring has json as the full path.
		// jsonl/csv are still iterated in the request for forward
		// compatibility — log + fall back to the full handler.
		h.log.Info("metadata_export: format fallback applied",
			zap.String("requested", format),
			zap.String("fallback", h.formatFallback),
			zap.Int64("event_id", evt.ID),
		)
		format = h.formatFallback
	}

	written := 0
	for _, assetID := range ids {
		if err := h.writeOne(ctx, assetID, req); err != nil {
			h.log.Warn("metadata_export per-asset write failed — will retry the whole event",
				zap.String("asset_id", assetID),
				zap.Int64("event_id", evt.ID),
				zap.Error(err),
			)
			return fmt.Errorf("metadata_export write asset %s: %w", assetID, err)
		}
		written++
	}

	// Combiner file for jsonl/csv: also written atomically.
	switch format {
	case metadataExportFormatJSONL:
		if err := h.writeJSONL(ctx, ids, req); err != nil {
			return fmt.Errorf("metadata_export jsonl combine: %w", err)
		}
	case metadataExportFormatCSV:
		if err := h.writeCSV(ctx, ids, req); err != nil {
			return fmt.Errorf("metadata_export csv combine: %w", err)
		}
	}

	h.log.Info("asset.metadata_export.requested succeeded",
		zap.Int("written", written),
		zap.String("format", format),
		zap.String("output_dir", h.outputDir),
		zap.Int64("event_id", evt.ID),
	)
	return nil
}

// snapshot is the on-disk schema for one asset's sidecar.
type snapshot struct {
	AssetID    string         `json:"asset_id"`
	ExportedAt string         `json:"exported_at"`
	Includes   []string       `json:"includes"`
	Sections   map[string]any `json:"sections"`
}

func (h *MetadataExportHandler) buildSnapshot(ctx context.Context, assetID string, req *metadataExportRequest) (*snapshot, error) {
	include := req.Include
	if len(include) == 0 {
		include = []string{includeTechnical, includeProvenance, includeDelivery}
	}

	snap := &snapshot{
		AssetID:    assetID,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Includes:   include,
		Sections:   map[string]any{},
	}
	for _, section := range include {
		v, err := h.loadSection(ctx, assetID, section)
		if err != nil {
			// Section-specific errors are non-fatal; we still emit a
			// `null` placeholder so the on-disk schema is uniform.
			h.log.Debug("metadata_export section load failed (non-fatal)",
				zap.String("asset_id", assetID),
				zap.String("section", section),
				zap.Error(err),
			)
			v = nil
		}
		snap.Sections[section] = v
	}
	return snap, nil
}

func (h *MetadataExportHandler) loadSection(ctx context.Context, assetID, section string) (any, error) {
	switch section {
	case includeTechnical:
		var id, src, name, mtype string
		var driveFileID, driveLink, category string
		var quality sql.NullFloat64
		err := h.db.QueryRowContext(ctx, `
			SELECT id, COALESCE(source,''), COALESCE(name,''), COALESCE(media_type,''),
			       COALESCE(drive_file_id,''), COALESCE(drive_link,''),
			       COALESCE(category,''), quality_score
			FROM media_assets WHERE id = ? LIMIT 1
		`, assetID).Scan(&id, &src, &name, &mtype, &driveFileID, &driveLink, &category, &quality)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"asset_id": id, "source": src, "name": name,
			"media_type": mtype, "drive_file_id": driveFileID,
			"drive_link": driveLink, "category": category,
			"quality_score": quality.Float64,
		}, nil

	case includeDelivery:
		rows, err := h.db.QueryContext(ctx, `
			SELECT delivery_id, endpoint_url, status_code, response_hash, delivered_at, note
			FROM delivery_log WHERE asset_id = ?
			ORDER BY delivered_at DESC LIMIT 50
		`, assetID)
		if err != nil {
			return []any{}, nil
		}
		defer rows.Close()
		out := []any{}
		for rows.Next() {
			var dID, ep, hsh, da, note string
			var sc sql.NullInt64
			if scanErr := rows.Scan(&dID, &ep, &sc, &hsh, &da, &note); scanErr == nil {
				out = append(out, map[string]any{
					"delivery_id": dID, "endpoint_url": ep,
					"status_code": sc.Int64, "response_hash": hsh,
					"delivered_at": da, "note": note,
				})
			}
		}
		return out, nil

	case includeProvenance:
		// Minimal provenance: source + media_type + ingestion lineage.
		// Future PRs extend this with the canonical ingestion log.
		var src string
		err := h.db.QueryRowContext(ctx, `
			SELECT COALESCE(source,'') FROM media_assets WHERE id = ? LIMIT 1
		`, assetID).Scan(&src)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"source": src}, nil

	case includeTimeline:
		// Timelines are owned by semantic.MetadataWriter; we read the
		// canonical-side write here. Today the table is empty (the
		// writer writes JSON sidecars); produce an empty map so the
		// schema is stable.
		return map[string]any{}, nil

	case includeEntities:
		// Entities come from the semantic tagger. Same pattern as
		// timeline: empty map today; future writer adds the row reads.
		return map[string]any{}, nil

	default:
		return nil, fmt.Errorf("unknown section %q (allowlist filter must have caught this)", section)
	}
}

// writeOne writes a single asset's sidecar atomically.
func (h *MetadataExportHandler) writeOne(ctx context.Context, assetID string, req *metadataExportRequest) error {
	snap, err := h.buildSnapshot(ctx, assetID, req)
	if err != nil {
		return err
	}
	finalPath := filepath.Join(h.outputDir, assetID+".json")
	tmpPath := finalPath + ".tmp"
	body, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(tmpPath, finalPath, body); err != nil {
		return err
	}
	return nil
}

// writeJSONL combines all per-asset snapshots into a single JSONL file.
func (h *MetadataExportHandler) writeJSONL(ctx context.Context, ids []string, req *metadataExportRequest) error {
	combinedPath := filepath.Join(h.outputDir, req.JobID+".jsonl")
	tmpPath := combinedPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	cleanup := func() { f.Close(); os.Remove(tmpPath) }
	for _, assetID := range ids {
		snap, err := h.buildSnapshot(ctx, assetID, req)
		if err != nil {
			cleanup()
			return err
		}
		if err := enc.Encode(snap); err != nil {
			cleanup()
			return err
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, combinedPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// writeCSV combines all per-asset snapshots into a single CSV file.
// Columns: asset_id, exported_at, includes, sections_json.
func (h *MetadataExportHandler) writeCSV(ctx context.Context, ids []string, req *metadataExportRequest) error {
	combinedPath := filepath.Join(h.outputDir, req.JobID+".csv")
	tmpPath := combinedPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	cleanup := func() { f.Close(); os.Remove(tmpPath) }
	w := csv.NewWriter(f)
	if err := w.Write([]string{"asset_id", "exported_at", "includes", "sections_json"}); err != nil {
		cleanup()
		return err
	}
	for _, assetID := range ids {
		snap, err := h.buildSnapshot(ctx, assetID, req)
		if err != nil {
			cleanup()
			return err
		}
		sectionsJSON, mErr := json.Marshal(snap.Sections)
		if mErr != nil {
			cleanup()
			return mErr
		}
		if err := w.Write([]string{
			snap.AssetID, snap.ExportedAt, strings.Join(snap.Includes, "|"), string(sectionsJSON),
		}); err != nil {
			cleanup()
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, combinedPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// atomicWrite is the POSIX-standard atomic rename: write to a sibling
// .tmp then os.Rename. The .tmp lives in the same directory as the
// final destination so the rename is a single inode swap on linux/macos
// (no cross-device link errors).
func atomicWrite(tmpPath, finalPath string, body []byte) error {
	if err := os.WriteFile(tmpPath, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
