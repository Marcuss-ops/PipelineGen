// Package wiring owns production semantic-search readiness composition.
package wiring

import (
	"context"
	"strings"

	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediasearch"
)

type semanticBackendInspector interface {
	HasBackend(name string) bool
}

// semanticReadinessChecker probes only canonical media-search dependencies.
// POSTGRES-MEDIA-CUTOVER removed Qdrant reachability, SQLite hydration and
// Ollama presence from this contract. Embedder/backend readiness is derived
// from the semantic backend that actually passed the composition gate; that
// gate requires the E5 embedding registry plus the canonical PostgreSQL
// retrieval+hydration store and delivery dependency.
type semanticReadinessChecker struct {
	embedderWired        bool
	semanticBackendWired bool
	aggregator           mediasearchapi.AggregatorSearcher
	mediaPostgres        interface{ PingContext(context.Context) error }
}

var _ mediasearchapi.SemanticReadyChecker = (*semanticReadinessChecker)(nil)

// newSemanticReadinessChecker adapts composition-root singletons without
// importing concrete database or embedding transports into the handler.
func newSemanticReadinessChecker(root *ComposeRoot, aggregator mediasearchapi.AggregatorSearcher) *semanticReadinessChecker {
	c := &semanticReadinessChecker{aggregator: aggregator}
	if inspector, ok := aggregator.(semanticBackendInspector); ok {
		c.semanticBackendWired = inspector.HasBackend("semantic")
		// The semantic backend is registered only after Embeddings is non-nil,
		// so this is the real E5 composition signal. Ollama is unrelated.
		c.embedderWired = c.semanticBackendWired
	}
	if root != nil && root.MediaPostgres != nil {
		c.mediaPostgres = root.MediaPostgres
	}
	return c
}

// Ready returns nil only when every canonical semantic-search dependency is
// healthy. Failures are returned together through the typed Subsystems map.
func (c *semanticReadinessChecker) Ready(ctx context.Context) error {
	subs := make(map[string]string, 3)

	if !c.embedderWired {
		subs["embedder"] = "E5 semantic embedding dependency not wired"
	}
	if c.aggregator == nil || !c.semanticBackendWired {
		subs["semantic_backend"] = "semantic search backend not registered"
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
