// Package legacyaudit — collection snapshot walker.
//
// This file owns ONE capability concern (godlike/06 SSOT one-canonical-owner-per-fact):
// the read-side port the audit pipeline drives + the data shapes that flow
// out of the collection walker. Sister files:
//
//   - audit_payload.go — per-point payload classification (pure functions,
//     no I/O).
//   - audit_reconciler.go — apply step + canonical-point-ID drift + the
//     "fix it" surface.
//   - legacyaudit.go (slim orchestrator) — package doc + StringifyReport
//     cross-capability CLI presentation helper.
//
// Every QdrantScanner interface, ScrollPoint, NextOffsetExtractor,
// Categories, PointAudit, Report, and Classify walker lives canonically
// here. The 4-way split is governed by
// architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06
// (PR-SPLIT-LEGACYAUDIT-V2, deadline 2026-07-15).
package audit

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// QdrantScanner is the read-side port the AuditService needs from the
// Qdrant collection surface. Production wires
// internal/platform/qdrant.Client.ScrollPoints behind this port;
// tests inject a stub that yields synthetic ScrollPoint slices without
// HTTP.
//
// Scrolling is paginated via NextOffset; AuditService drives the loop
// to completion (NextOffset==""). The scanner does NOT filter or
// rewrite — classification stays source-faithful so the operator can
// audit which subset was tagged.
type QdrantScanner interface {
	ScrollPoints(ctx context.Context, collection, offset string, limit int) ([]ScrollPoint, error)
}

// ScrollPoint mirrors qdrant.ScrollPoint; defined here so the
// application-layer port does NOT leak the infra-layer type across an
// internal/application -> internal/infrastructure edge.
type ScrollPoint struct {
	ID      string
	Payload map[string]any
}

// NextOffsetExtractor is the contract the AuditService needs to walk
// the paginated result. QDRANT returns NextOffset alongside each page;
// production concatenates Page{NextOffset, Points} into the port
// response of ScrollPoints above.
type NextOffsetExtractor interface {
	NextOffset(page []ScrollPoint) string
}

// Categories captures the 8 user-spec classification buckets. The
// category field is the canonical axis the dry-run report emits;
// nullable fields ('missing' axis) and dimension counters ('invalid'
// axis) complement it for actionable breakdowns.
type Categories struct {
	NonMediaRow          int      `json:"non_media_row"`
	MetadataJSON         int      `json:"metadata_json"`
	HiddenTempFiles      int      `json:"hidden_temp_files"`
	InvalidVectors       int      `json:"invalid_vectors"`
	WrongDimensions      int      `json:"wrong_dimensions"`
	LegacyLifecycle      int      `json:"legacy_lifecycle"`
	LegacyLocatorPayload int      `json:"legacy_locator_payload"`
	NonCanonicalPointID  int      `json:"non_canonical_point_id"`
	SampleIDs            []string `json:"sample_ids,omitempty"`
}

// Pointed point is the per-point classification the AuditService
// emits so the Apply step (separate concern, gated on --apply) can
// dispatch dry-run provenance back to the operator without re-scanning.
type PointAudit struct {
	PointID    string
	AssetID    string
	Categories Categories
	// DimensionObservation records per-channel dimension state when
	// the point hit category 5 (wrong_dimensions) so the operator
	// can tell WHICH channel drifted.
	DimensionObservation map[string]int `json:"dimension_observation,omitempty"`
}

// Report is the full dry-run output the CLI prints and the apply
// step consumes. JSON-stable so --json mode (and CI dashboards) see a
// schema-versioned shape across releases.
type Report struct {
	Collection   string       `json:"collection"`
	TotalPoints  int          `json:"total_points"`
	CompleteScan bool         `json:"complete_scan"`
	Audit        Categories   `json:"audit"`
	Points       []PointAudit `json:"points,omitempty"`
	Errors       []string     `json:"errors,omitempty"`
}

// Classify walks the collection via the Scanner port, classifies each
// point, and returns the per-category counters + a per-point audit
// slice (capped at maxPointAudits to keep dry-run output bounded for
// large collections — operators can re-run with --limit if they want
// more samples). Returns the report + a non-nil error ONLY on hard
// scan failures (scroll error, bad collection name); per-point
// classification errors are captured in Report.Errors.
func Classify(ctx context.Context, scanner QdrantScanner, collection string, maxPointAudits int) (*Report, error) {
	if scanner == nil {
		return nil, errors.New("legacyaudit.Classify: scanner is required")
	}
	if collection == "" {
		return nil, errors.New("legacyaudit.Classify: collection is required")
	}
	sch := schema.DefaultV3Schema()
	specByChannel := make(map[string]schema.EmbeddingSpec, len(sch.DenseVectors))
	for _, s := range sch.DenseVectors {
		specByChannel[s.Channel] = s
	}

	r := &Report{Collection: collection}
	const pageSize = 500
	const maxPages = 400 // safety cap at 200k points — matches verifier.go
	offset := ""

	for pageIdx := 0; pageIdx < maxPages; pageIdx++ {
		points, err := scanner.ScrollPoints(ctx, collection, offset, pageSize)
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("scroll page %d: %v", pageIdx, err))
			return r, fmt.Errorf("scroll %q at offset %q: %w", collection, offset, err)
		}
		if len(points) == 0 && offset == "" {
			// Empty collection — the first page is a complete scan and
			// apply is a no-op. No error.
			r.CompleteScan = true
			return r, nil
		}
		r.TotalPoints += len(points)

		for _, pt := range points {
			cats, dimObs := classifyPoint(pt, specByChannel)
			// Union the per-point classification into the report counter
			// (each point can fit multiple categories; the bucket counts
			// are point-wise hits, not point-wise exclusivity).
			r.Audit.NonMediaRow += cats.NonMediaRow
			r.Audit.MetadataJSON += cats.MetadataJSON
			r.Audit.HiddenTempFiles += cats.HiddenTempFiles
			r.Audit.InvalidVectors += cats.InvalidVectors
			r.Audit.WrongDimensions += cats.WrongDimensions
			r.Audit.LegacyLifecycle += cats.LegacyLifecycle
			r.Audit.LegacyLocatorPayload += cats.LegacyLocatorPayload
			r.Audit.NonCanonicalPointID += cats.NonCanonicalPointID

			if maxPointAudits > 0 && len(r.Points) < maxPointAudits {
				pa := PointAudit{
					PointID:              pt.ID,
					AssetID:              stringFromPayload(pt.Payload, "asset_id"),
					Categories:           cats,
					DimensionObservation: dimObs,
				}
				r.Points = append(r.Points, pa)
				// Track a flat per-point sample of IDs (bounded).
				if len(r.Audit.SampleIDs) < 100 {
					r.Audit.SampleIDs = append(r.Audit.SampleIDs, pt.ID)
				}
			}
		}

		// Page termination: the Qdrant REST contract signals
		// end-of-collection via NextOffset="" (see
		// https://api.qdrant.tech/api-reference/points/scroll-points).
		// The page size is a HINT to the server, not a termination
		// signal — relying on `len(points) < pageSize` fires
		// prematurely when a page returns fewer points than the
		// request limit (e.g., a collection with 250 points when
		// pageSize=500). The cursor is the sole canonical signal.
		if ex, ok := scanner.(NextOffsetExtractor); ok {
			next := ex.NextOffset(points)
			if next == "" {
				r.CompleteScan = true
				break
			}
			offset = next
		} else {
			// Without a NextOffsetExtractor we can't paginate. Return a
			// partial report; it must not count toward removal evidence.
			break
		}
	}
	return r, nil
}
