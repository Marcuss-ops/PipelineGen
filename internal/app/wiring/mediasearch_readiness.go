// Package wiring owns production semantic-search readiness composition.
package wiring

import (
	"context"
	"strings"

	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediasearch"
)

// semanticReadinessChecker probes only canonical media-search dependencies.
// POSTGRES-MEDIA-CUTOVER removed Qdrant reachability and SQLite hydration from
// this contract: the media PostgreSQL handle now owns both pgvector retrieval
// and media_assets hydration.
type semanticReadinessChecker struct {
	embedderWired bool
	aggregator    mediasearchapi.AggregatorSearcher
	mediaPostgres interface{ PingContext(context.Context) error }
}

var _ mediasearchapi.SemanticReadyChecker = (*semanticReadinessChecker)(nil)

// newSemanticReadinessChecker adapts composition-root singletons without
// importing concrete database or transport implementations into the handler.
func newSemanticReadinessChecker(root *ComposeRoot, aggregator mediasearchapi.AggregatorSearcher) *semanticReadinessChecker {
	c := &semanticReadinessChecker{aggregator: aggregator}
	if root != nil {
		// Keep the existing query-embedder presence signal. The semantic backend
		// itself is registered only when the canonical embedding registry is wired,
		// so aggregator readiness remains the authoritative composition gate.
		c.embedderWired = root.AI != nil && root.AI.OllamaEmbedClient != nil
		if root.MediaPostgres != nil {
			c.mediaPostgres = root.MediaPostgres
		}
	}
	return c
}

// Ready returns nil only when every canonical semantic-search dependency is
// healthy. Failures are returned together through the typed Subsystems map.
func (c *semanticReadinessChecker) Ready(ctx context.Context) error {
	subs := make(map[string]string, 3)

	if !c.embedderWired {
		subs["embedder"] = "query embedding client not wired"
	}
	if c.aggregator == nil {
		subs["semantic_backend"] = "search aggregator not wired"
	}
	if c.mediaPostgres == nil {
		subs["media_postgres"] = "media PostgreSQL not wired"
	} else if err := c.mediaPostgres.PingContext(ctx); err != nil {
		subs["media_postgres"] = sanitizeReadinessMessage(err.Error())
	}

	if len(subs) == 0 {
		return nil
	}
	return readinessSubsystemsError{subs: subs}
}

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

func (e readinessSubsystemsError) Subsystems() map[string]string { return e.subs }

func sanitizeReadinessMessage(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if len([]rune(msg)) > 200 {
		msg = string([]rune(msg)[:200])
	}
	return msg
}

// WireMediasearchReadiness assembles the canonical readiness checker. Index
// version remains unknown unless a live manifest adapter is supplied elsewhere;
// no fake version is invented here.
func WireMediasearchReadiness(root *ComposeRoot, aggregator mediasearchapi.AggregatorSearcher) mediasearchapi.SemanticReadyChecker {
	return newSemanticReadinessChecker(root, aggregator)
}
