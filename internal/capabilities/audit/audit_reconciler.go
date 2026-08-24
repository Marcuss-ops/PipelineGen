// Package legacyaudit — drift detection + apply step.
//
// This file owns ONE capability concern (godlike/06 SSOT
// one-canonical-owner-per-fact): the "fix it" surface — drift detection
// helpers (legacy lifecycle dual-key, legacy locator payload keys, non-
// canonical UUID v5 point IDs) AND the apply-step shapes that the
// cmd/admin/qdrant_maintenance.go delete-invalid mode consumes. Sister files:
//
//   - audit_collection.go — read-side port + walker + walker output
//     envelope (the data shapes that flow through the audit pass).
//   - audit_payload.go — per-point payload classifiers (pure functions,
//     no I/O).
//   - legacyaudit.go (slim orchestrator) — package doc + StringifyReport
//     cross-capability CLI presentation helper.
//
// Every drift-detection helper (legacyLifecycleHit / legacyLocatorHit /
// observeNonCanonicalPointID), every canonical-point-ID helper
// (CanonicalPointID / IsCanonicalPointID), and every apply-step struct
// (ApplyRequest / MarshalAudit / ValidateAssetIDs) lives canonically
// here. The 4-way split is governed by
// architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06
// (PR-SPLIT-LEGACYAUDIT-V2, deadline 2026-07-15).
package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// ──────────────────────────────────────────────────────────────────────
// Per-category drift-detection helpers (exported for unit-test use).
// ──────────────────────────────────────────────────────────────────────

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
// QDRANT-001 surface at internal/platform/qdrant/schema/AssetIDToQdrantPointID
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

// ──────────────────────────────────────────────────────────────────────
// Internal helper (used by the drift-detection surface above).
// ──────────────────────────────────────────────────────────────────────

// hasKeyNonEmpty mirrors stringFromPayload with an explicit boolean so
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
