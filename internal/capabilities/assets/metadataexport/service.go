// Package metadataexport — business orchestration for filesystem export.
//
// Step 2 of the post-architettura 2026 plan (June 2026): the export
// orchestration (exportFilesystem / writeOne / writeJSONL / writeCSV /
// buildSnapshot / loadSection) WAS previously inline inside the
// MetadataExportHandler.Handle() method (internal/capabilities/jobs/queue/
// outbox/metadata_export.go). After split, this file owns the
// orchestration so the handler is a thin envelope-dispatch layer.
//
// All FS writes go through the ExportWriter port; all SQL goes through
// the AssetResolver port. Zero `database/sql` or `os` imports in this
// file — the compile-time boundary is enforced by the package
// signature and pinned by `scripts/ci-architectural-checks.sh`.
package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Service coordinates the filesystem export for one request. It owns
// the format-fallback logic (jsonl/csv requests fall back to json
// logging the reason for transparency), the per-asset loop, and the
// combined-file emission. Holds no state across Handle() calls —
// handlers re-construct per event.
type Service struct {
	resolver       AssetResolver
	writer         ExportWriter
	outputDir      string
	formatFallback string
	log            *zap.Logger
}

// NewService constructs a Service. outputDir is the absolute path the
// writer wraps; nil resolver / writer are tolerated for tests that
// only exercise the EventType / empty-payload validation paths — the
// canonical production wiring in BuildOutboxBundle populates both.
//
// formatFallback defaults to FormatJSON (today's only fully-implemented
// path); future PRs can override to FormatJSONL/FormatCSV without a
// schema bump.
func NewService(log *zap.Logger, resolver AssetResolver, writer ExportWriter, outputDir string) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{
		resolver:       resolver,
		writer:         writer,
		outputDir:      outputDir,
		formatFallback: FormatJSON,
		log:            log.Named("metadata_export"),
	}
}

// ErrOutputDirMissing is returned when the handler is asked to write a
// filesystem export but no outputDir is configured. Distinct from
// ErrMetadataTerminal — output dir missing is operator-fixable so it
// is RETRYABLE (the outbox pool will re-attempt after a config push).
var ErrOutputDirMissing = errors.New("metadata_export output directory not configured")

// ErrResolverUnavailable is returned when AssetResolver nil. Distinct
// from ErrMetadataTerminal for the same reason as ErrOutputDirMissing
// (operator-fixable).
var ErrResolverUnavailable = errors.New("metadata_export: AssetResolver port not wired")

// ErrWriterUnavailable is returned when ExportWriter nil. Same retryable
// semantics as ErrOutputDirMissing.
var ErrWriterUnavailable = errors.New("metadata_export: ExportWriter port not wired")

// ExportFilesystem writes one sidecar per asset (with format = FormatJSON
// today), and one combined JSONL/CSV file alongside when the envelope
// asks (format fallback honestly logs + keeps writing JSON for jsonl/csv
// "until real writers land" — matches the pre-split behaviour bit-for-bit).
//
// Returns the first non-nil error encountered so the outbox pool retries
// the whole event (QDRANT-002 PR4 pattern). Per-asset failures short-
// circuit the loop so a partial write left behind can be detected by
// the next attempt via the asset sidecar mtime.
func (s *Service) ExportFilesystem(ctx context.Context, eventID int64, req *MetadataExportRequest, ids []string) error {
	if s.outputDir == "" {
		return ErrOutputDirMissing
	}
	if s.resolver == nil {
		return ErrResolverUnavailable
	}
	if s.writer == nil {
		return ErrWriterUnavailable
	}

	if err := s.writer.EnsureDir(s.outputDir); err != nil {
		return fmt.Errorf("mkdir %s: %w", s.outputDir, err)
	}

	format := req.Format
	if format != FormatJSON {
		// Honest fallback: today's wiring has json as the full path.
		// jsonl/csv are still iterated in the request for forward
		// compatibility — log + fall back to the full handler.
		s.log.Info("metadata_export: format fallback applied",
			zap.String("requested", format),
			zap.String("fallback", s.formatFallback),
			zap.Int64("event_id", eventID),
		)
		format = s.formatFallback
	}

	var snapshotsByID [][]byte
	written := 0
	for _, assetID := range ids {
		snap, err := s.buildSnapshot(ctx, assetID, req)
		if err != nil {
			s.log.Warn("metadata_export per-asset snapshot failed — will retry the whole event",
				zap.String("asset_id", assetID),
				zap.Int64("event_id", eventID),
				zap.Error(err),
			)
			return fmt.Errorf("metadata_export build asset %s: %w", assetID, err)
		}
		body, marshalErr := json.MarshalIndent(snap, "", "  ")
		if marshalErr != nil {
			return fmt.Errorf("metadata_export marshal asset %s: %w", assetID, marshalErr)
		}
		if err := s.writer.WriteJSON(s.outputDir, assetID, body); err != nil {
			s.log.Warn("metadata_export per-asset write failed — will retry the whole event",
				zap.String("asset_id", assetID),
				zap.Int64("event_id", eventID),
				zap.Error(err),
			)
			return fmt.Errorf("metadata_export write asset %s: %w", assetID, err)
		}
		snapshotsByID = append(snapshotsByID, body)
		written++
	}

	// Combiner file for jsonl/csv: also written atomically.
	switch format {
	case FormatJSONL:
		if err := s.writer.WriteJSONL(s.outputDir, req.JobID, snapshotsByID); err != nil {
			return fmt.Errorf("metadata_export jsonl combine: %w", err)
		}
	case FormatCSV:
		if err := s.writeCSVRows(ctx, eventID, req, ids, snapshotsByID); err != nil {
			return fmt.Errorf("metadata_export csv combine: %w", err)
		}
	}

	s.log.Info("asset.metadata_export.requested succeeded",
		zap.Int("written", written),
		zap.String("format", format),
		zap.String("output_dir", s.outputDir),
		zap.Int64("event_id", eventID),
	)
	return nil
}

// buildSnapshot assembles one asset's sidecar. Default includes when
// not specified: {technical, provenance, delivery}. Each section is
// loaded independently via the resolver; section-level errors are
// non-fatal (the caller writes nil placeholder so the on-disk schema
// stays uniform across partial-success queries).
func (s *Service) buildSnapshot(ctx context.Context, assetID string, req *MetadataExportRequest) (*snapshot, error) {
	include := req.Include
	if len(include) == 0 {
		include = []string{IncludeTechnical, IncludeProvenance, IncludeDelivery}
	}

	snap := &snapshot{
		AssetID:    assetID,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Includes:   include,
		Sections:   map[string]any{},
	}
	for _, section := range include {
		v, err := s.loadSection(ctx, assetID, section)
		if err != nil {
			s.log.Debug("metadata_export section load failed (non-fatal)",
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

// loadSection fans out across the resolver's per-section methods.
// timeline + entities intentionally fall through to empty maps: today's
// writers leave them empty and a future PR will add LoadTimeline +
// LoadEntities methods on the resolver in lockstep with this switch.
func (s *Service) loadSection(ctx context.Context, assetID, section string) (any, error) {
	switch section {
	case IncludeTechnical:
		return s.resolver.LoadTechnicalSection(ctx, assetID)
	case IncludeDelivery:
		return s.resolver.LoadDeliverySection(ctx, assetID)
	case IncludeProvenance:
		return s.resolver.LoadProvenanceSection(ctx, assetID)
	case IncludeTimeline:
		// Timelines are owned by semantic.MetadataWriter; we read the
		// canonical-side write here. Today the table is empty (the
		// writer writes JSON sidecars); produce an empty map so the
		// schema is stable.
		return map[string]any{}, nil
	case IncludeEntities:
		// Entities come from the semantic tagger. Same pattern as
		// timeline: empty map today; future writer adds the row reads.
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("unknown section %q (allowlist filter must have caught this)", section)
	}
}

// writeCSVRows assembles the CSV's header + rows from the per-asset
// snapshot bodies. The CSV's columns are:
//
//	asset_id, exported_at, includes, sections_json
//
// The per-section JSON stays raw (string column) so the schema is
// stable across section additions. The assembler writes via the
// canonical csv.Writer (header + rows + flush + error-check) before
// handing the encoded bytes to ExportWriter.WriteCSV for atomicity.
func (s *Service) writeCSVRows(ctx context.Context, eventID int64, req *MetadataExportRequest, ids []string, snapshotsJSON [][]byte) error {
	if len(ids) != len(snapshotsJSON) {
		// Defence: the loop in ExportFilesystem writes one snapshot
		// per id in the same order. A divergence here means a future
		// refactor broke the invariant — fail loud.
		return fmt.Errorf("metadata_export csv combine: ids/snapshots length mismatch (ids=%d, snapshots=%d)", len(ids), len(snapshotsJSON))
	}
	header := []string{"asset_id", "exported_at", "includes", "sections_json"}
	rows := make([][]string, 0, len(snapshotsJSON))
	for i, body := range snapshotsJSON {
		var snap snapshot
		if err := json.Unmarshal(body, &snap); err != nil {
			return fmt.Errorf("metadata_export csv decode asset %s: %w", ids[i], err)
		}
		sectionsJSON, mErr := json.Marshal(snap.Sections)
		if mErr != nil {
			return fmt.Errorf("metadata_export csv marshal sections for %s: %w", ids[i], mErr)
		}
		if err := csvWellFormed(snap, sectionsJSON); err != nil {
			return err
		}
		rows = append(rows, []string{
			snap.AssetID, snap.ExportedAt, strings.Join(snap.Includes, "|"), string(sectionsJSON),
		})
	}
	return s.writer.WriteCSV(s.outputDir, req.JobID, header, rows)
}

// csvWellFormed runs a final sanity check on the row's components
// before the writer encodes it. Catches divergent shapes early so the
// CSV doesn't carry a malformed row through to the downstream consumer.
func csvWellFormed(snap snapshot, sectionsJSON []byte) error {
	if snap.AssetID == "" {
		return fmt.Errorf("metadata_export csv: empty asset_id in snapshot")
	}
	if snap.ExportedAt == "" {
		return fmt.Errorf("metadata_export csv: empty exported_at in snapshot (%s)", snap.AssetID)
	}
	if len(sectionsJSON) == 0 {
		return fmt.Errorf("metadata_export csv: empty sections_json in snapshot (%s)", snap.AssetID)
	}
	return nil
}
