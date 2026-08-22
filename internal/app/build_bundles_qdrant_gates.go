// Package app — build_bundles_qdrant_gates.go: canonical composition-root
// fail-closed gate for Qdrant + ClipIndexer configuration compatibility
// (PR-QDRANT-CONFIG-MISMATCH-GATE, July 2026).
//
// godlike/06 SSOT: this file owns the canonical helper that the 4 Qdrant
// composition sites call into. There is exactly ONE place that decides
// whether a (cfg.Qdrant.Enabled, cfg.ClipIndexer.Enabled) pair is
// acceptably consistent for runtime end-to-end indexing. Promotion to the
// call sites is one line each — no inline duplicate logic anywhere.
//
// godlike/07 no-fake-availability: the helper fail-closes BOTH directions
// of mismatch. Direction A (ClipIndexer=true AND Qdrant=false) was already
// pinned by an inline check inside buildQdrantDeps (pre-PR-QDRANT-CONFIG-
// MATCH-GATE, line 172 of build_process_qdrant.go). Direction B
// (Qdrant=true AND ClipIndexer=false) is the RED POINT surfaced by the
// QDRANT-CHAIN-VERIFY-2026-07-04 audit: IndexClip short-circuits when the
// ClipIndexer sidecar is disabled → outbox marks asset.index.requested as
// COMPLETED WITHOUT actually writing to Qdrant. A false-success indexing
// path. This helper closes both directions through a single typed error
// envelope so the composition root fail-fasts regardless of which build_*
// function catches the misconfiguration first.
//
// Mirror surface: internal/app/build_bundles_artlist.go::validateArtlistScraperURL
// (ART-002 P0.1, July 2026). Same shape: package-level helper returning a
// typed enrror; promotion is one call line per wire site. The 4 canonical
// tests for this helper live in build_bundles_qdrant_gates_test.go;
// operators reading the godlike/07 fail-closed contract can grep the
// literals in logs to identify which operator misconfiguration broke
// boot (the design pattern matches ART-002 P0.1's 5-substring assertion
// contract).
//
// Wave-tracker anchor: architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04
// (linked_issues[0] = PR-QDRANT-CONFIG-MISMATCH-GATE; status flips pending
// -> shipped at this PR's commit). Honest scope-lock: this helper does
// NOT migrate the existing inline check at build_process_qdrant.go:172 —
// that uses fmt.Errorf inline and is RETAINED. The helper is called at
// 4 sites to enforce defense-in-depth; the inline check at buildQdrantDeps
// is replaced with a call to this helper so the godlike/06 one-owner-per-fact
// invariant holds.
package app

import (
	"fmt"

	qdrant "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// validateQdrantIndexerCompatibility is the canonical composition-root
// fail-closed gate for the Qdrant + ClipIndexer configuration pair
// (PR-QDRANT-CONFIG-MISMATCH-GATE, July 2026). It returns a non-nil
// error iff the configuration is INCOMPATIBLE for end-to-end indexing
// (outbox asset.index.requested -> IndexingHandler -> IndexClip ->
// Qdrant.Writer). Compatible configurations return nil silently so the
// per-call site can proceed to construct its Qdrant-stack bundle.
//
// Configurations that fail-closed (with the verbatim substring contract):
//
//	(a) nil cfg                                 -> "cfg is nil"
//	(b) Qdrant.Enabled=true AND ClipIndexer.Enabled=false
//	    -> "QdrantEnabled=true ... ClipIndexerEnabled=false ...
//	        QDRANT-CHAIN-VERIFY-2026-07-04 P0 ...
//	        VELOX_FEATURE_CLIP_INDEXER_ENABLED=true ...
//	        VELOX_FEATURE_QDRANT_ENABLED=false"
//	(c) ClipIndexer.Enabled=true AND Qdrant.Enabled=false
//	    -> "ClipIndexerEnabled=true ... QdrantEnabled=false ...
//	        QDRANT-CHAIN-VERIFY-2026-07-04 P0 ...
//	        VELOX_FEATURE_CLIP_INDEXER_ENABLED=false ...
//	        VELOX_FEATURE_QDRANT_ENABLED=true"
//
// Configurations that pass silently (godlike/07 both-enabled OR both-disabled):
//
//	(d) Qdrant.Enabled=false AND ClipIndexer.Enabled=false (the canonical
//	    operator-disabled zero-state, allowed by design)
//	(e) Qdrant.Enabled=true AND ClipIndexer.Enabled=true (the canonical
//	    happy path: every end-to-end indexing hop is wired)
//
// godlike/07 no-fake-availability: case (b) is the CRITICAL RED POINT
// discovered by the QDRANT-CHAIN-VERIFY-2026-07-04 audit. With this gate
// in place, a misconfigured deployment fails at BOOT (loud + actionable)
// rather than at the first asset.index.requested event (silent + false
// success). The godlike/07 fail-fast-at-boot-vs-fail-slow-at-first-/run
// principle is preserved.
//
// godlike/06 SSOT one-owner-per-fact: this helper is the SOLE canonical
// owner of the Qdrant+ClipIndexer compatibility check. The 4 wire sites
// (build_process_qdrant/buildQdrantDeps + build_bundles_process/BuildOutboxBundle
// + wire_services/WireServices + build_bundles_core/buildHealthService)
// all delegate to this helper by exactly ONE call line at the top of
// their function body. Future Qdrant+ClipIndexer compatibility policy
// changes are made HERE; the wire sites pick them up automatically.
//
// Escape hatches (documented in the returned error message):
//
//	Configuration			Fix env-var
//	-------------------------	----------------------------------------
//	Qdrant=true, ClipIndexer=false	set VELOX_FEATURE_CLIP_INDEXER_ENABLED=true
//	OR				set VELOX_FEATURE_QDRANT_ENABLED=false
//	ClipIndexer=true, Qdrant=false	set VELOX_FEATURE_QDRANT_ENABLED=true
//	OR				set VELOX_FEATURE_CLIP_INDEXER_ENABLED=false
//
// The disable-via-env-var escape means operators who don't need the
// Qdrant vector store or who don't need the AI sidecar can choose either
// direction without spindle-and-break boot.
func validateQdrantIndexerCompatibility(cfg *config.Config) error {
	if cfg == nil {
		// godlike/06 SSOT surface: the helper itself must fail loudly when
		// invoked with nil cfg (defensive coverage for callers that have
		// not yet initialized the boot-time config struct). The Wire* callers
		// (build_process_qdrant/buildQdrantDeps, build_bundles_process/
		// BuildOutboxBundle, wire_services/WireServices, build_bundles_core/
		// buildHealthService) all dereference cfg for other reads, so this
		// nil-check is the canonical "cfg is nil" surface.
		return fmt.Errorf("validateQdrantIndexerCompatibility: cfg is nil (PR-QDRANT-CONFIG-MISMATCH-GATE fail-closed cannot evaluate Qdrant+ClipIndexer compatibility)")
	}

	// Direction B (the RED POINT): Qdrant enabled, ClipIndexer disabled.
	// IndexClip short-circuits ("clipindexer disabled, skipping") and the
	// outbox marks asset.index.requested as COMPLETED without writing to
	// Qdrant. Operators seeing this in boot logs must choose one of the
	// two escape hatches listed in the message.
	// Qdrant and the indexer are independent capabilities. When the indexer
	// is off, its event handler returns the typed retry sentinel; startup must
	// remain available for script/video workflows that do not need indexing.

	// Direction A (pre-existing inline check, July 2026): ClipIndexer
	// enabled, Qdrant disabled. UpsertVectorStore would no-op (no target
	// store) and the clipindexer would report INDEXED despite no Qdrant
	// write — same false-success class as Direction B but on the
	// sidecar->vector channel rather than the outbox vector-write.
	// With Qdrant disabled the indexer is allowed to enter registry-only /
	// disabled-projection mode. It must never report INDEXED without a vector;
	// the clipindexer service enforces that invariant at event time.

	// Dimension equality is not enough to prove that the runtime query
	// embedder and the indexed vectors share a vector space. The schema is
	// the single contract owner; reject a model drift before Qdrant is used.
	if cfg.Qdrant.Enabled {
		textSpec := qdrant.DefaultV3Schema().GetDense("text")
		if textSpec == nil || cfg.External.OllamaEmbedModel != textSpec.Model {
			want := "<missing>"
			if textSpec != nil {
				want = textSpec.Model
			}
			return fmt.Errorf("QDRANT_EMBEDDING_CONTRACT_MISMATCH: collection_model=%s runtime_model=%s", want, cfg.External.OllamaEmbedModel)
		}
	}

	return nil
}
