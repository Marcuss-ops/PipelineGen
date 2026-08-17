// Package app — build_bundles_qdrant_gates_test.go: TDD coverage for the
// PR-QDRANT-CONFIG-MISMATCH-GATE composition-root fail-closed helper
// (mirror of ART-002 P0.1 validateArtlistScraperURL test surface —
// godlike/06 SSOT).
//
// Scope (per architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04.linked_issues[0]):
//   - TestValidateQdrantIndexerCompatibility_NilCfg_ReturnsError:
//     defensive nil-guard coverage (godlike/06 SSOT surface).
//   - TestValidateQdrantIndexerCompatibility_BothDisabled_ReturnsNil:
//     the operator-disabled zero-state is intentional and pass-through.
//   - TestValidateQdrantIndexerCompatibility_BothEnabled_ReturnsNil:
//     the canonical happy path (every end-to-end hop is wired).
//   - TestValidateQdrantIndexerCompatibility_QdrantEnabledNoClipIndexer_FailsClosed:
//     the CRITICAL RED POINT surfaced by the QDRANT-CHAIN-VERIFY-2026-07-04
//     audit. IndexClip short-circuits when ClipIndexer is disabled; outbox
//     marks asset.index.requested as COMPLETED without writing to Qdrant
//     (false-success). The helper fail-closes the misconfiguration at boot
//     per godlike/07 no-fake-availability.
//
// The 4th test is the canonical godlike/07 fail-closed case (the entire
// reason this helper exists). It pins BOTH the failing condition AND the
// actionable fix hints so future operators can copy/paste the env-var
// names from the runtime error message into their deployment config
// without consulting docs.
//
// Coverage note: Direction A (ClipIndexer=true AND Qdrant=false) is
// exercised by TestValidateQdrantIndexerCompatibility_BothEnabled_ReturnsNil's
// sister in composition_test.go::TestComposition_ClipIndexerEnabledNoQdrant_FailClosed
// — the existing canary which already pins that path with buildQdrantDeps
// wired (the pre-PR inline check retains its own 5-substring test in the
// composition_test.go suite). The new helper RE-USES that exact contract
// under a single canonical surface so future godlike/06 SSOT gates only
// point at this helper, not the inline check.
//
// Defense-in-depth preview: this helper is called from 4 build/wire entry
// points (build_process_qdrant/buildQdrantDeps + build_bundles_process/
// BuildOutboxBundle + wire_services/WireServices + build_bundles_core/
// buildHealthService). The unit-test surface targets the helper directly
// per godlike/06 SSOT; per-call-site coverage lives in composition_test.go
// canary tests.
package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// TestValidateQdrantIndexerCompatibility_NilCfg_ReturnsError: defensive
// coverage for the nil-cfg case. Returns a typed error so every Wire*
// call site propagates the godlike/07 fail-closed pattern: the helper
// is invoked UPFRONT and any error short-circuits the enclosing fn
// before it dereferences cfg for its own reads.
func TestValidateQdrantIndexerCompatibility_NilCfg_ReturnsError(t *testing.T) {
	err := validateQdrantIndexerCompatibility(nil)
	require.Error(t, err, "nil cfg must fail-closed (godlike/06 SSOT defensive surface)")
	assert.Contains(t, err.Error(), "cfg is nil",
		"error must name the nil-receiver condition so operators can grep it in logs")
	assert.Contains(t, err.Error(), "PR-QDRANT-CONFIG-MISMATCH-GATE",
		"error must cite the wave-tracker anchor + PR-QDRANT-CONFIG-MISMATCH-GATE for audit traceability")
}

// TestValidateQdrantIndexerCompatibility_BothDisabled_ReturnsNil: when
// both Qdrant and ClipIndexer are disabled, the helper is intentionally
// a no-op. This pins the canonical operator-disabled zero-state: there
// is no outbox indexing chain to validate, the configuration is "feature
// opt-out fully", and the composition root is allowed to proceed.
func TestValidateQdrantIndexerCompatibility_BothDisabled_ReturnsNil(t *testing.T) {
	cfg := &config.Config{
		ClipIndexer: config.ClipIndexerConfig{Enabled: false},
		Qdrant:      config.QdrantConfig{Enabled: false},
	}
	err := validateQdrantIndexerCompatibility(cfg)
	assert.NoError(t, err,
		"disabled Qdrant + disabled ClipIndexer is the allowed zero-state — the gate is a no-op")
}

// TestValidateQdrantIndexerCompatibility_BothEnabled_ReturnsNil: when
// both Qdrant and ClipIndexer are enabled, every end-to-end indexing
// hop is wired (outbox asset.index.requested -> IndexingHandler ->
// IndexClip -> Qdrant.Writer). The helper is a no-op and composition
// root proceeds normally. This pins the canonical happy path.
func TestValidateQdrantIndexerCompatibility_BothEnabled_ReturnsNil(t *testing.T) {
	cfg := &config.Config{
		ClipIndexer: config.ClipIndexerConfig{Enabled: true},
		Qdrant:      config.QdrantConfig{Enabled: true},
		External:    config.ExternalConfig{OllamaEmbedModel: "intfloat/multilingual-e5-base"},
	}
	err := validateQdrantIndexerCompatibility(cfg)
	assert.NoError(t, err,
		"enabled Qdrant + enabled ClipIndexer is the happy path — the gate is a no-op")
}

func TestValidateQdrantIndexerCompatibility_EmbeddingModelMismatchFailsClosed(t *testing.T) {
	cfg := &config.Config{
		ClipIndexer: config.ClipIndexerConfig{Enabled: true},
		Qdrant:      config.QdrantConfig{Enabled: true},
		External:    config.ExternalConfig{OllamaEmbedModel: "nomic-embed-text"},
	}
	err := validateQdrantIndexerCompatibility(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "QDRANT_EMBEDDING_CONTRACT_MISMATCH")
}

// TestValidateQdrantIndexerCompatibility_QdrantEnabledNoClipIndexer_FailsClosed:
// the CANONICAL godlike/07 no-fake-availability case from the
// QDRANT-CHAIN-VERIFY-2026-07-04 audit. Qdrant is enabled but the
// ClipIndexer AI sidecar is disabled — IndexClip short-circuits to nil
// at first asset.index.requested event, the outbox marks the event as
// COMPLETED without writing to Qdrant (false-success). The helper
// aborts loudly with an actionable fix hint naming both env-var escape
// hatches.
//
// The assertion contract (5 substrings) mirrors PR-ARTLIST-LIVE-WIRE's
// validateArtlistScraperURL test envelope — the same canonical pattern
// for godlike/07 fail-closed composition gates. If a future refactor
// weakens any of the substrings, this test fails and the regression is
// caught at unit-test time rather than at first operator boot.
func TestValidateQdrantIndexerCompatibility_QdrantEnabledNoClipIndexer_FailsClosed(t *testing.T) {
	cfg := &config.Config{
		ClipIndexer: config.ClipIndexerConfig{Enabled: false},
		Qdrant:      config.QdrantConfig{Enabled: true},
		// The embedding contract check runs first and would short-circuit
		// before the Direction-B branch under test; keep the canonical
		// runtime model so this test pins the ClipIndexer-off failure.
		External: config.ExternalConfig{OllamaEmbedModel: "intfloat/multilingual-e5-base"},
	}
	err := validateQdrantIndexerCompatibility(cfg)
	require.Error(t, err,
		"PR-QDRANT-CONFIG-MISMATCH-GATE: Qdrant=true + ClipIndexer=false must fail-closed (godlike/07 no-fake-availability; the RED POINT surfaced by QDRANT-CHAIN-VERIFY-2026-07-04 audit)")

	// 5-substring contract assertion (godlike/07 fail-closed coupling).
	assert.Contains(t, err.Error(), "QdrantEnabled=true",
		"error must name the failing condition (the Qdrant feature flag was on)")
	assert.Contains(t, err.Error(), "ClipIndexerEnabled=false",
		"error must name the failing field (the ClipIndexer sidecar was off)")
	assert.Contains(t, err.Error(), "QDRANT-CHAIN-VERIFY-2026-07-04 P0",
		"error must cite the wave-tracker anchor for audit traceability")
	assert.Contains(t, err.Error(), "VELOX_FEATURE_CLIP_INDEXER_ENABLED=true",
		"error must name the env-var fix hint (start the AI sidecar)")
	assert.Contains(t, err.Error(), "VELOX_FEATURE_QDRANT_ENABLED=false",
		"error must name the alternative env-var fix hint (disable the vector store)")
}
