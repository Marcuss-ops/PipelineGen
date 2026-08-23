// Package scripts — generation_identity.go provides thin wrappers
// around the canonical script.BuildFingerprint for generation items
// and envelopes.
//
// The identity is deterministic and includes every field that affects
// the generated script content. Output flags (postprocessors) are
// excluded — they control what artifacts to produce, not what text
// the engine writes.
package adapters

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// BuildItemIdentity computes a deterministic identity string for a
// generation item. Two items with the same identity produce the same
// script text (modulo LLM non-determinism), so the identity serves as
// a memory-gate cache key.
//
// This function is a thin wrapper around the canonical
// script.BuildFingerprint. All fingerprint logic lives in the domain
// package; this wrapper preserves the existing adapters-package call
// sites.
func BuildItemIdentity(item scriptpkg.GenerationItemV2) string {
	return scriptpkg.BuildFingerprint(scriptpkg.FingerprintInputFromItem(item))
}

// BuildEnvelopeIdentity computes a single identity for the entire
// envelope, used for top-level idempotency. For single-item envelopes
// this is the item identity; for multi-item envelopes it delegates to
// the canonical BuildFingerprint so envelope identity uses the same
// hashing path as every other generation fingerprint.
//
// Control flags that only affect idempotency/cache behavior (e.g.
// ForceRefresh) are intentionally ignored so the identity reflects
// only the content that determines the generated script.
func BuildEnvelopeIdentity(env *scriptpkg.GenerationEnvelopeV2) string {
	if env == nil || len(env.Items) == 0 {
		return ""
	}
	if len(env.Items) == 1 {
		return BuildItemIdentity(env.Items[0])
	}
	var ids []string
	for _, item := range env.Items {
		ids = append(ids, BuildItemIdentity(item))
	}
	sort.Strings(ids)
	input := scriptpkg.GenerationFingerprintInput{
		ContractVersion: 1,
		SourceType:      "envelope",
		SourceTextHash:  sha256Hex(strings.Join(ids, "\n")),
	}
	return scriptpkg.BuildFingerprint(input)
}

// sha256Hex returns the first 16 hex chars of the SHA-256 digest of s.
func sha256Hex(s string) string {
	sum := digest.SHA256Bytes([]byte(s))
	return sum[:16]
}
