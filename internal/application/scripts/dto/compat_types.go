// Package dto — compat_types.go (PR-noop-adapters-purge, 2026-07-25).
//
// PR-noop-adapters-purge (2026-07-25, P0 absolute — wave
// CLEANUP-PRIORITY-1-5-2026-07-25) retired the duplicate
// EntityExtractor + MetadataGenerator interface definitions.
//
// godlike/06 SSOT one-canonical-owner-per-fact:
// the typed ports EntityExtractor + MetadataGenerator live ONLY in
// `internal/application/scripts/adapters/compat_adapters.go`. The
// pre-PR duplicate declarations here violated the invariant (two
// definitions, same signature, neither knew about the other).
// Callers that previously referenced dto.EntityExtractor /
// dto.MetadataGenerator are migrated to adapters.EntityExtractor /
// adapters.MetadataGenerator (= the canonical home).
//
// The remaining surface (PostProcessArtifact type alias +
// SerializeEntityResultRoundTrip helper) is preserved for backward
// read-compat with code that does NOT depend on the typed ports.
package dto

import (
	"encoding/json"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// PostProcessArtifact is the historical accumulator name used by tests and
// several processors. It aliases the canonical interface{}.
//
// godlike/06 SSOT: this alias remains because it documents the
// pre-PR-3 accumulator shape. New code MUST use typed ports
// (adapters.EntityExtractor / adapters.MetadataGenerator) rather
// than interface{}-shaped postprocessors.
type PostProcessArtifact = interface{}

// SerializeEntityResultRoundTrip preserves the typed entity result as JSON for
// legacy read-only compatibility. It never mutates the source of truth.
//
// godlike/06 SSOT: this helper is the canonical JSON serializer for
// *scriptpkg.EntityResult. Defined here because the dto package
// owns the entity-result serialization surface; the EntityExtractor
// / MetadataGenerator ports live in internal/application/scripts/adapters/.
func SerializeEntityResultRoundTrip(res *scriptpkg.EntityResult) (string, error) {
	if res == nil {
		return "", nil
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("serialize entity result: %w", err)
	}
	return string(raw), nil
}
