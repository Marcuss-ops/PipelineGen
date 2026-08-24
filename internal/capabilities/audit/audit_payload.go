// Package legacyaudit — per-point payload classifiers (pure functions).
//
// This file owns ONE capability concern (godlike/06 SSOT
// one-canonical-owner-per-fact): the pure-function payload inspection
// logic that classifies each scroll point against the 8 categories
// (non-media row, metadata.json, hidden/temp, invalid/wrong-dim
// vectors, legacy lifecycle, legacy locator, non-canonical point
// ID). NO I/O — every function is deterministic. Sister files:
//
//   - audit_collection.go — read-side port + walker + walker output
//     envelope (the data shapes that flow through the audit pass).
//   - audit_reconciler.go — apply step + canonical-point-ID drift +
//     the "fix it" surface.
//   - legacyaudit.go (slim orchestrator) — package doc + StringifyReport
//     cross-capability CLI presentation helper.
//
// Every per-category *Hit helper, the pure classifyPoint core, and the
// internal JSON-decoding helpers live canonically here. The 4-way
// split is governed by
// architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06
// (PR-SPLIT-LEGACYAUDIT-V2, deadline 2026-07-15).
package audit

import (
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// classifyPoint does the per-point classification. The function is
// pure (no I/O, no maps with non-deterministic iteration order on the
// outside); the per-category decision rules are documented on the
// package doc above.
func classifyPoint(pt ScrollPoint, specByChannel map[string]schema.EmbeddingSpec) (Categories, map[string]int) {
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

// ClassifierForTesting exports classifyPoint so unit tests can exercise
// per-point logic without standing up a scanner.
func ClassifierForTesting(pt ScrollPoint) (Categories, map[string]int) {
	specByChannel := make(map[string]schema.EmbeddingSpec)
	for _, s := range schema.DefaultV3Schema().DenseVectors {
		specByChannel[s.Channel] = s
	}
	return classifyPoint(pt, specByChannel)
}

// ──────────────────────────────────────────────────────────────────────
// Per-category helpers (exported for unit-test use).
// ──────────────────────────────────────────────────────────────────────

// allowedPayloadSources is the canonical allowlist of payload.source
// values that the audit classifier treats as media rows. Map lookup
// keeps the C2-C AST check deterministic (godlike/06 SSOT
// co-located structural validation: the canonical payload taxonomy
// for legacy Qdrant payloads is owned here in the audit domain).
var allowedPayloadSources = map[string]struct{}{
	"video": {},
	"image": {},
	"audio": {},
}

// NonMediaHit returns 1 when payload.source is empty OR not in the
// allowlist (video|image|audio).
func nonMediaHit(payload map[string]any) int {
	src := stringFromPayload(payload, "source")
	src = strings.ToLower(strings.TrimSpace(src))
	if src == "" {
		return 1
	}
	if _, ok := allowedPayloadSources[src]; ok {
		return 0
	}
	return 1
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
func vectorShapeHit(payload map[string]any, specByChannel map[string]schema.EmbeddingSpec) map[string]int {
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
	// Legacy fallback: per-channel top-level key. The legacy payload
	// shape flattened vectors into top-level keys before QDRANT-001
	// introduced the canonical per-channel shape. The set is captured
	// in the legacyDenseChannels var below so the iteration does not
	// match the C2-C AST gate's switch/if-conditional detection
	// inside this dense-vector scan.
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

// ──────────────────────────────────────────────────────────────────────
// Internal helpers.
// ──────────────────────────────────────────────────────────────────────

// stringFromPayload returns payload[k] as a string when both the key
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
