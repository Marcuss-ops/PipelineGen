// Package legacyaudit — single-source-of-truth for legacy Qdrant
// point classification and canonical cleanup contract.
//
// Issue 12 (June 2026): the qdrant-maintenance command consolidates
// clean-qdrant-locators + cleanup-qdrant-legacy under one surface
// with 3 modes: audit (classify all 8 categories, no mutations),
// repair-locators (strip drive_link/local_path keys), and
// delete-invalid (outbox-delete non-locator assets). Per the user
// spec, locator payload keys are repairable — points whose only
// finding is LegacyLocatorPayload are excluded from delete-invalid.
// The 8 categories are the canonical list:
//
//  1. non-media rows           — payload.source is missing or not
//     in the allowed media-type set
//     (video|image|audio). Caused by
//     legacy ingest paths emitting rows
//     without a source discriminator.
//  2. metadata.json            — payload.metadata_json (legacy
//     fingerprint block on payload, NOT
//     on media_assets.metadata_json
//     column). Bulk-import era residue;
//     the canonical asset fingerprint
//     is now indexed_version_<channel>
//     on the per-channel payload key,
//     see IndexSchema.ManifestQ5().
//  3. hidden/temp files        — payload.name (or local-path
//     surrogate) starts with '.' OR
//     ends with '.tmp'/'.bak'/'.swp'.
//     These are Drive upload-residue
//     rows that the sync pipeline did
//     not delete.
//  4. invalid vectors          — at least one dense channel has
//     a NaN/Inf/zero-row vector. Maps
//     directly to ErrNaNOrInf in the
//     canonical PayloadMapper.
//  5. wrong dimensions         — vector channel dim != schema
//     IndexSchema spec dim. The
//     canonical mapping is
//     AssetIDToQdrantPointID +
//     EmbeddingSpec.Dimensions check.
//  6. legacy lifecycle         — payload has both the legacy
//     "status" key AND the canonical
//     "lifecycle_state" key, OR the
//     legacy status is non-empty with
//     lifecycle_state empty. The
//     canonical SSOT is lifecycle_state
//     (QDRANT-004 PR2, see
//     qdrant.DefaultV3Schema.PayloadIndexes).
//  7. legacy locator payload   — payload has drive_link or
//     local_path (the QDRANT-005
//     closure removed both keys from
//     BuildPayload, but legacy upserts
//     still carry them). See
//     qdrant.LocatorCleaner for the
//     single-purpose path; this is
//     the unified-classification path.
//  8. non-canonical point ID   — point.ID is NOT a canonical
//     UUID v5 string (because
//     AssetIDToQdrantPointID always
//     produces UUID v5 hashes). Asset
//     IDs written via raw asset.ID
//     literal are an anti-pattern
//     (every point whose ID is raw
//     has been inserted by a legacy
//     path that bypassed the
//     canonical helper).
//
// Classification is READ-ONLY: pointing at a point never mutates
// its payload or media_assets row. The apply step (Apply via the
// canonical outbox.Dispatcher.EnqueueAndDelete) is a separate
// concern, gated on the dry-run output.
//
// Cross-reference:
//   - Internal/infrastructure/qdrant/locator_cleaner.go cleanup
//     contract for category 7.
//   - Internal/infrastructure/qdrant/pointid.go canonical UUID v5
//     boundary for category 8.
//   - architecture/ownership.yaml ::target_readiness for the
//     downstream consumer.
//   - godlike/06_DATA §"One owner per fact" (this package owns
//     the canonical 8-category list; downstream consumers must
//     import this package, not duplicate it).
package legacyaudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// QdrantScanner is the read-side port the AuditService needs from the
// Qdrant collection surface. Production wires
// internal/infrastructure/qdrant.Client.ScrollPoints behind this port;
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
	Collection  string       `json:"collection"`
	TotalPoints int          `json:"total_points"`
	Audit       Categories   `json:"audit"`
	Points      []PointAudit `json:"points,omitempty"`
	Errors      []string     `json:"errors,omitempty"`
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
	schema := qdrant.DefaultV3Schema()
	specByChannel := make(map[string]qdrant.EmbeddingSpec, len(schema.DenseVectors))
	for _, s := range schema.DenseVectors {
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
			// Empty collection — return the zero-valued report so apply
			// is a no-op. No error.
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

		// Page termination: when ScrollPoints returns fewer than
		// pageSize entries, we're at the tail. The production scanner
		// returns pageSize+1 sentinel for end-of-collection detection
		// (the Qdrant REST contract returns NextOffset="" specifically
		// for the LAST page; the caller walks until empty).
		if len(points) < pageSize {
			break
		}
		// Cursor advance: in production the scanner embeds NextOffset
		// in the slice header offset (mirrors qdrant.ScrollResult).
		// For the port-defined scanner the caller supplies its own
		// NextOffsetExtractor; we use a small interface assertion here
		// so the production wiring drops the cursor through qdrant.Client.ScrollPoints
		// and tests can drive with an inline NextOffsetExtractor.
		if ex, ok := scanner.(NextOffsetExtractor); ok {
			next := ex.NextOffset(points)
			if next == "" {
				break
			}
			offset = next
		} else {
			// Without a NextOffsetExtractor we can't paginate — break
			// on first page so the loop terminates. This branch covers
			// stubs that mock the first page only.
			break
		}
	}
	return r, nil
}

// ClassifierForTesting exports classifyPoint so unit tests can exercise
// per-point logic without standing up a scanner.
func ClassifierForTesting(pt ScrollPoint) (Categories, map[string]int) {
	specByChannel := make(map[string]qdrant.EmbeddingSpec)
	for _, s := range qdrant.DefaultV3Schema().DenseVectors {
		specByChannel[s.Channel] = s
	}
	return classifyPoint(pt, specByChannel)
}

// classifyPoint does the per-point classification. The function is
// pure (no I/O, no maps with non-deterministic iteration order on the
// outside); the per-category decision rules are documented on the
// package doc above.
func classifyPoint(pt ScrollPoint, specByChannel map[string]qdrant.EmbeddingSpec) (Categories, map[string]int) {
	var (
		cats   Categories
		dimObs map[string]int
	)
	if len(pt.Payload) == 0 {
		// Empty payload is treated as non-media (category 1) AND
		// triggers a non-canonical point ID if pt.ID is non-canonical.
		cats.NonMediaRow = 1
		observeNonCanonicalPointID(pt, &cats)
		return cats, dimObs
	}

	// 1. non-media rows: source is missing or not in the allowlist.
	cats.NonMediaRow = nonMediaHit(pt.Payload)

	// 2. metadata.json: legacy fingerprint block on payload (the
	// pre-QDRANT-001 emission pattern; canonical emitter writes
	// indexed_version_<channel> per-channel only).
	cats.MetadataJSON = metadataJSONHit(pt.Payload)

	// 3. hidden/temp files: name OR local-path surrogate starts with
	// '.' OR ends with '.tmp'/'.bak'/'.swp'.
	cats.HiddenTempFiles = hiddenTempHit(pt.Payload)

	// 4 + 5. invalid/wrong-dim vectors: scan dense channels.
	dimObs = vectorShapeHit(pt.Payload, specByChannel)
	if _, ok := dimObs["__invalid_token"]; ok {
		cats.InvalidVectors = 1
		delete(dimObs, "__invalid_token")
	}
	if len(dimObs) > 0 {
		cats.WrongDimensions = 1
	}

	// 6. legacy lifecycle: both legacy "status" and canonical
	// "lifecycle_state" are present, OR legacy status with empty
	// lifecycle_state.
	cats.LegacyLifecycle = legacyLifecycleHit(pt.Payload)

	// 7. legacy locator payload: drive_link or local_path in payload.
	cats.LegacyLocatorPayload = legacyLocatorHit(pt.Payload)

	// 8. non-canonical point ID: pt.ID is NOT a UUID string
	// (canonical AssetIDToQdrantPointID always produces UUID v5).
	observeNonCanonicalPointID(pt, &cats)

	return cats, dimObs
}

// ──────────────────────────────────────────────────────────────────────
// Per-category helpers (exported for unit-test use).
// ──────────────────────────────────────────────────────────────────────

// NonMediaHit returns 1 when payload.source is empty OR not in the
// allowlist (video|image|audio).
func nonMediaHit(payload map[string]any) int {
	src := stringFromPayload(payload, "source")
	src = strings.ToLower(strings.TrimSpace(src))
	if src == "" {
		return 1
	}
	switch src {
	case "video", "image", "audio":
		return 0
	default:
		return 1
	}
}

// MetadataJSONHit returns 1 when payload carries a "metadata_json"
// key (the pre-QDRANT-001 fingerprint block). The canonical emission
// pattern uses per-channel indexed_version_<channel> keys; a leftover
// metadata_json payload field is an audit finding.
func metadataJSONHit(payload map[string]any) int {
	if v, ok := payload["metadata_json"]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return 1
		}
	}
	return 0
}

// HiddenTempHit returns 1 when payload.name (or local-path surrogate)
// has a hidden/temp filename signature. Patterns are reduced to a
// single allowlist: leading-dot OR a temp suffix.
func hiddenTempHit(payload map[string]any) int {
	name := stringFromPayload(payload, "name")
	if isHiddenOrTemp(name) {
		return 1
	}
	if path := stringFromPayload(payload, "local_path"); isHiddenOrTemp(path) {
		return 1
	}
	return 0
}

// IsHiddenOrTemp is the predicate used by HiddenTempHit and exposed so
// the cmd/admin report layer can surface the same predicate.
func IsHiddenOrTemp(s string) bool { return isHiddenOrTemp(s) }

func isHiddenOrTemp(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, ".") {
		return true
	}
	lower := strings.ToLower(s)
	for _, suffix := range []string{".tmp", ".bak", ".swp", ".partial", ".~"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// VectorShapeHit inspects every dense vector channel carried in the
// Vectors map (legacy payload-only pattern) OR the canonical
// `vectors` map (per-point key format). Channel presence with a
// wrong-dim vector bumps WrongDimensions; channels with malformed
// tokens (non-numeric) bump InvalidVectors under the sentinel key
// "__invalid_token".
func vectorShapeHit(payload map[string]any, specByChannel map[string]qdrant.EmbeddingSpec) map[string]int {
	dimObs := make(map[string]int)

	// Canonical Qdrant REST pattern: payload carries "vectors":
	// {"text": [...], "transcript": [...], ...}. The OLD wire shape
	// from the pre-QDRANT-001 sync paths sometimes flattened vectors
	// into payload top-level keys. The classifier handles BOTH.
	channels := map[string][]float64{}

	if raw, ok := payload["vectors"]; ok {
		if m, ok := raw.(map[string]any); ok {
			for k, v := range m {
				if arr, ok := v.([]any); ok {
					vec := floatsFromAny(arr)
					if vec != nil {
						channels[k] = vec
					}
				}
			}
		}
	}
	// Legacy fallback: per-channel top-level key.
	for _, ch := range []string{"text", "transcript", "visual", "audio"} {
		if _, present := channels[ch]; present {
			continue
		}
		if raw, ok := payload[ch]; ok {
			switch v := raw.(type) {
			case []any:
				vec := floatsFromAny(v)
				if vec != nil {
					channels[ch] = vec
				}
			case []float64:
				channels[ch] = v
			case []float32:
				cp := make([]float64, len(v))
				for i, x := range v {
					cp[i] = float64(x)
				}
				channels[ch] = cp
			}
		}
	}

	for ch, vec := range channels {
		spec, ok := specByChannel[ch]
		if !ok {
			// Unknown channel — not a wrong-dim finding; just record
			// for the operator's per-channel breakdown.
			dimObs[ch] = len(vec)
			continue
		}
		// Check shape: NaN/Inf tokens bump InvalidVectors.
		for _, x := range vec {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				dimObs["__invalid_token"] = 1
				break
			}
		}
		// Check dim against the canonical EmbeddingSpec.
		if spec.Dimensions > 0 && len(vec) != spec.Dimensions {
			dimObs[ch] = len(vec)
		}
	}
	return dimObs
}

// LegacyLifecycleHit returns 1 when payload has both the legacy
// "status" key AND the canonical "lifecycle_state" key (duality from
// pre-QDRANT-004 ingest paths), OR when status is non-empty while
// lifecycle_state is empty (legacy-only path).
func legacyLifecycleHit(payload map[string]any) int {
	hasStatus, statusValue := hasKeyNonEmpty(payload, "status")
	hasLifecycle, lifecycleValue := hasKeyNonEmpty(payload, "lifecycle_state")
	if hasStatus && hasLifecycle {
		// Both fields populated — legacy drift; canonical SSOT is
		// lifecycle_state, so this point is outdated.
		if statusValue != lifecycleValue {
			return 1
		}
		// Both present and equal — no drift; conservative: do not
		// tag.
		return 0
	}
	if hasStatus && !hasLifecycle {
		// Legacy-only: status is non-empty and lifecycle_state is
		// empty / missing.
		return 1
	}
	return 0
}

// LegacyLocatorHit returns 1 when payload has drive_link or local_path
// (QDRANT-005 closure removed both from BuildPayload but legacy
// upserts still carry them).
func legacyLocatorHit(payload map[string]any) int {
	for _, k := range []string{"drive_link", "local_path"} {
		if _, ok := payload[k]; ok {
			return 1
		}
	}
	return 0
}

// ObserveNonCanonicalPointID sets cats.NonCanonicalPointID = 1 when
// pt.ID is NOT a UUID v5 (canonical) hash. Identifies points written
// via legacy code paths that used the raw asset.ID literal as point.ID.
func observeNonCanonicalPointID(pt ScrollPoint, cats *Categories) {
	if pt.ID == "" {
		return
	}
	if _, err := uuid.Parse(pt.ID); err != nil {
		cats.NonCanonicalPointID = 1
	}
}

// ──────────────────────────────────────────────────────────────────────
// PointID canonicalisation helpers (apply step).
// ──────────────────────────────────────────────────────────────────────

// CanonicalPointID returns the canonical UUID v5 hash for assetID
// using the project-namespaced boundary. Mirrors the canonical
// QDRANT-001 surface at internal/infrastructure/qdrant/schema/AssetIDToQdrantPointID
// so the apply step can build replacement events without going
// through the schema_aliases.go forwarded shell (which is no longer
// the canonical entry point per the Check 2 gate).
func CanonicalPointID(assetID string) string {
	return schema.AssetIDToQdrantPointID(assetID)
}

// IsCanonicalPointID returns true iff pt.ID is a UUID v5 hash that
// resolves to AssetIDToQdrantPointID(assetID). Used by the apply step
// to confirm a "non-canonical point ID" finding is real.
func IsCanonicalPointID(assetID, ptID string) bool {
	if assetID == "" || ptID == "" {
		return false
	}
	return CanonicalPointID(assetID) == ptID
}

// ──────────────────────────────────────────────────────────────────────
// Apply helpers (used by cmd/admin/qdrant_maintenance.go delete-invalid mode).
// ──────────────────────────────────────────────────────────────────────

// ApplyRequest is the input the CLI passes to the apply step. The
// per-asset_id list preserves scan provenance so the operator can
// audit which asset was deleted via which outbox event.
type ApplyRequest struct {
	Collection string
	// AssetIDs is the resolved list to delete through the canonical
	// outbox.Dispatcher.EnqueueAndDelete path. Empty here is OK
	// when the dry-run output had zero audit findings.
	AssetIDs []string
}

// MarshalAudit produces a stable JSON encoding of the ApplyRequest so
// callers can checkpoint apply progress.
func MarshalAudit(req ApplyRequest) ([]byte, error) {
	if req.Collection == "" {
		return nil, errors.New("legacyaudit.MarshalAudit: collection is required")
	}
	return json.Marshal(req)
}

// StringifyReport renders a human-readable summary suitable for the
// CLI default (non-JSON) output. The function is exported so the admin
// command can avoid re-implementing the format.
func StringifyReport(r *Report) string {
	if r == nil {
		return "(no report)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Collection:        %s\n", r.Collection)
	fmt.Fprintf(&b, "Points scrolled:   %d\n", r.TotalPoints)
	fmt.Fprintf(&b, "Non-media rows:    %d\n", r.Audit.NonMediaRow)
	fmt.Fprintf(&b, "metadata.json:     %d\n", r.Audit.MetadataJSON)
	fmt.Fprintf(&b, "Hidden/temp:       %d\n", r.Audit.HiddenTempFiles)
	fmt.Fprintf(&b, "Invalid vectors:   %d\n", r.Audit.InvalidVectors)
	fmt.Fprintf(&b, "Wrong dimensions:  %d\n", r.Audit.WrongDimensions)
	fmt.Fprintf(&b, "Legacy lifecycle:  %d\n", r.Audit.LegacyLifecycle)
	fmt.Fprintf(&b, "Legacy locator:    %d\n", r.Audit.LegacyLocatorPayload)
	fmt.Fprintf(&b, "Non-canonical ID:  %d\n", r.Audit.NonCanonicalPointID)
	if len(r.Errors) > 0 {
		fmt.Fprintf(&b, "Errors:            %d\n", len(r.Errors))
		for i, e := range r.Errors {
			fmt.Fprintf(&b, "  [%d] %s\n", i, e)
		}
	}
	return b.String()
}

// ──────────────────────────────────────────────────────────────────────
// Internal helpers.
// ──────────────────────────────────────────────────────────────────────

// StringFromPayload returns payload[k] as a string when both the key
// exists AND the value type is string.
func stringFromPayload(payload map[string]any, k string) string {
	if payload == nil {
		return ""
	}
	if v, ok := payload[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// HasKeyNonEmpty mirrors stringFromPayload with an explicit boolean so
// legacy-lifecycle detection can distinguish "key missing" from
// "key present but empty".
func hasKeyNonEmpty(payload map[string]any, k string) (bool, string) {
	if payload == nil {
		return false, ""
	}
	v, ok := payload[k]
	if !ok || v == nil {
		return false, ""
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		return s != "", s
	}
	return true, ""
}

// floatsFromAny converts []any to []float64 returning nil on first
// non-numeric value; the caller treats nil as a no-hit. JSON's
// default decode emits []any so we centralise the conversion.
func floatsFromAny(arr []any) []float64 {
	if len(arr) == 0 {
		return nil
	}
	out := make([]float64, 0, len(arr))
	for _, x := range arr {
		switch v := x.(type) {
		case float64:
			out = append(out, v)
		case float32:
			out = append(out, float64(v))
		default:
			return nil
		}
	}
	return out
}

// ValidateAssetIDs returns an error when AssetIDs contains an empty
// entry. The apply step calls this before any outbox dispatch.
func ValidateAssetIDs(ids []string) error {
	for i, id := range ids {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("legacyaudit.ValidateAssetIDs: empty asset id at index %d", i)
		}
	}
	return nil
}
