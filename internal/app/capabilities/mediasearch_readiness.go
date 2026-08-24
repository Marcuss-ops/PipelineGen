// Package app — mediasearch_readiness.go (wired August 2026).
//
// Closes the QDRANT-004 readiness gap: the mediasearch handler was
// constructed with an empty SemanticReady checker, so GET
// /internal/v1/media/ready always reported
// "semantic_search_real checker not wired" even when the search plane
// was fully operational. This file owns the production-shaped typed
// readiness probe the composition root hands to the handler, plus the
// index-version source.
//
// godlike/07 fail-closed contract (see internal/api/mediasearch/
// readiness.go): Ready returns a typed multi-error whose
// Subsystems() map names EVERY failing sub-system. The handler's
// buildReadinessReport decomposes that map into per-sub-system
// booleans. When no sub-system fails, Ready returns nil and the report
// renders all-green.
package capabilities

import (
	"context"
	"strings"

	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/api/mediasearch"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

// semanticReadinessChecker implements mediasearchapi.SemanticReadyChecker
// using narrow ports (not concrete infra types) so the composition root
// adapts the singletons once and tests can inject fakes:
//
//   - embedder          → embedderWired (root.AI.OllamaEmbedClient != nil)
//   - semantic_backend  → the canonical search.Aggregator (nil ⇒ no backend fanout)
//   - qdrant_reachable  → qdrantProbe.Probe (live GET /collections)
//   - sqlite_hydration  → dbPinger.PingContext (canonical media.db.sqlite)
//   - workspace_enforced→ structural: WorkerAuth + extractActor always refuse
//     missing/default workspace for non-admin principals; the /api surface
//     additionally mounts WorkspaceScopeMiddleware. No runtime probe needed.
type semanticReadinessChecker struct {
	embedderWired bool
	aggregator    mediasearchapi.AggregatorSearcher
	qdrantProbe   interface{ Probe(context.Context) error }
	dbPinger      interface{ PingContext(context.Context) error }
}

// Compile-time assertion: the checker satisfies the handler's port.
var _ mediasearchapi.SemanticReadyChecker = (*semanticReadinessChecker)(nil)

// newSemanticReadinessChecker wires the checker against the composition
// root and the canonical aggregator (may be nil pre-wiring; the probe
// then reports the semantic_backend sub-system as failing). The concrete
// Qdrant HealthProbe and SQLiteDB satisfy the narrow ports structurally.
func newSemanticReadinessChecker(root *wiring.ComposeRoot, aggregator mediasearchapi.AggregatorSearcher) *semanticReadinessChecker {
	c := &semanticReadinessChecker{
		aggregator: aggregator,
	}
	if root != nil {
		c.embedderWired = root.AI != nil && root.AI.OllamaEmbedClient != nil
		if root.Process != nil && root.Process.QdrantHealthProbe != nil {
			c.qdrantProbe = root.Process.QdrantHealthProbe
		}
		// Guard against a typed-nil *SQLiteDB: assigning root.DB blindly
		// would box a non-nil interface whose PingContext panics on the
		// embedded nil *sql.DB. Only assign when the concrete pointer is
		// actually non-nil.
		if root.DB != nil {
			c.dbPinger = root.DB
		}
	}
	return c
}

// Ready implements mediasearchapi.SemanticReadyChecker. Returns nil when
// every sub-system is healthy; otherwise a typed multi-error carrying the
// full failure map (not just the first failure).
func (c *semanticReadinessChecker) Ready(ctx context.Context) error {
	subs := make(map[string]string, 5)

	// embedder — the dedicated embedding client must be present.
	if !c.embedderWired {
		subs["embedder"] = "ollama embedding client not wired"
	}

	// semantic_backend — the canonical aggregator must exist (it owns the
	// backend fanout incl. Qdrant semantics).
	if c.aggregator == nil {
		subs["semantic_backend"] = "search aggregator not wired"
	}

	// qdrant_reachable — live probe against the configured Qdrant base.
	if c.qdrantProbe == nil {
		subs["qdrant_reachable"] = "qdrant health probe not wired"
	} else if err := c.qdrantProbe.Probe(ctx); err != nil {
		subs["qdrant_reachable"] = sanitizeReadinessMessage(err.Error())
	}

	// sqlite_hydration — canonical media.db.sqlite must answer a ping.
	if c.dbPinger == nil {
		subs["sqlite_hydration"] = "sqlite db not wired"
	} else if err := c.dbPinger.PingContext(ctx); err != nil {
		subs["sqlite_hydration"] = sanitizeReadinessMessage(err.Error())
	}

	if len(subs) == 0 {
		return nil
	}
	return readinessSubsystemsError{subs: subs}
}

// readinessSubsystemsError is the typed multi-error the handler's
// buildReadinessReport decomposes via the Subsystems() contract.
type readinessSubsystemsError struct {
	subs map[string]string
}

func (e readinessSubsystemsError) Error() string {
	parts := make([]string, 0, len(e.subs))
	for k, v := range e.subs {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	if len(parts) == 0 {
		return "semantic search readiness: ok"
	}
	return "semantic search not ready: " + strings.Join(parts, "; ")
}

// Subsystems implements the godlike/07 typed probe contract.
func (e readinessSubsystemsError) Subsystems() map[string]string {
	return e.subs
}

// sanitizeReadinessMessage trims a failure summary to a public-safe
// single line: whitespace collapsed, length capped at a UTF-8 rune
// boundary (never splits a multi-byte character). Mirrors the
// handler-side sanitize philosophy (no internal URLs / stack traces
// cross the HTTP boundary).
func sanitizeReadinessMessage(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > 200 {
		msg = string([]rune(msg)[:200])
	}
	return msg
}

// WireMediasearchReadiness assembles the SemanticReadyChecker the
// mediasearch handler needs from the composition root singletons.
//
// IndexVersionSource is deliberately NOT wired: the handler already
// defaults to StaticIndexVersion("") when the field is nil (empty
// version = unknown, per godlike/07 no-fake-availability), and no live
// IndexManifest adapter exists yet. Wiring a non-empty fake here would
// violate the documented "empty string when unknown" contract.
func WireMediasearchReadiness(root *wiring.ComposeRoot, aggregator mediasearchapi.AggregatorSearcher) mediasearchapi.SemanticReadyChecker {
	return newSemanticReadinessChecker(root, aggregator)
}
