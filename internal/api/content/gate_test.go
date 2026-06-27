package content

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/content (lessons.go
// + books.go + content.go). Content handlers for books + lessons modules.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports) + the grep-verified `books.NewService` orchestrator (added
// 2026-06-24 followup). See architecture/current.yaml::Wave 14 +
// arch check Check 19. Cross-ref: docs/migrations/api-infrastructure-
// imports-allowlist.txt (28 grandfathered-import entries as of Wave
// 14-PR3).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// Per-area orchestrator pattern (added 2026-06-24 followup, code-review
	// NIT-B): `books.NewService` is the canonical direct-orchestrator
	// constructor for the content/books sub-package; the API layer must
	// reach books via DomainBundle in internal/app/composition.go, not
	// via direct construction here. Grep-verified: zero hits in
	// internal/api/* production code at HEAD, safe to enforce as hard-fail.
	{Name: "books.NewService direct construction", Pattern: "books.NewService"},
}

func TestStaticGate_NoContentAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
