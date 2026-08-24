//go:build ignore

// Package fixture — self-check fixture for Check 15 (QDRANT-005A, June 2026).
//
// This file (check_15_qdrant_config_apikey.go) demonstrates the
// qdrant.NewClient(&qdrant.Config{...}) construction WITHOUT
// APIKey: cfg.Qdrant.APIKey in the same file. The canonical pattern
// is:
//
//	client := qdrant.NewClient(&qdrant.Config{
//	    BaseURL: cfg.Qdrant.BaseURL,
//	    APIKey:  cfg.Qdrant.APIKey,   // <-- REQUIRED
//	    Timeout: cfg.Qdrant.Timeout,
//	}, log)
//
// An API-key-protected Qdrant deployment appears unhealthy (HTTP 401)
// when the client omits the X-Api-Key header on every request. This
// fixture is the anti-pattern shape: a file that constructs the
// client but does NOT propagate cfg.Qdrant.APIKey. The per-file
// check in scripts/ci-architectural-checks.sh::Check 15 catches
// this by:
//  1. Finding every Go file that contains `qdrant.NewClient(&qdrant.Config{`
//  2. Verifying the SAME file also contains `APIKey:\s*cfg\.Qdrant\.APIKey`
//  3. Failing the gate if any file has the client construction
//     but not the APIKey propagation pattern.
//
// The self-check regex `qdrant\.NewClient\(&qdrant\.Config\{` matches
// the construction call on the first line of newClientWithoutAPIKey,
// verifying the fixture has the expected shape.
package fixture

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// newClientWithoutAPIKey constructs a qdrant.Client WITHOUT propagating
// cfg.Qdrant.APIKey — the QDRANT-005A Phase 1 Blocker 1 anti-pattern.
// In production this would cause an API-key-protected Qdrant server
// to return HTTP 401 on every request, making the deployment appear
// unhealthy even though Qdrant is actually reachable.
func newClientWithoutAPIKey() *transport.Client {
	client := transport.NewClient(&schema.Config{
		BaseURL: "http://127.0.0.1:6333",
		Timeout: 10,
		// MISSING: APIKey: cfg.Qdrant.APIKey
	}, zap.NewNop())
	return client
}
