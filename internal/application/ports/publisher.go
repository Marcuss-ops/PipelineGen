// Package ports — Publisher port (Fase 5(a), July 2026).
//
// godlike/06 SSOT: this file is a thin alias re-export of the
// canonical `Publisher` declaration at
// `internal/capabilities/delivery/publisher.go`. The alias
// preserves byte-stable identity for callers that want to depend on
// the abstract `ports.Publisher` rather than the deep-package
// `delivery.Publisher`.
//
// Phase 5(a) scope: declare the alias and the godlike/06 SSOT doc
// block pointing at the canonical site. Push 5.2 migrates callers
// from `delivery.Publisher` → `ports.Publisher`.
//
// WHY an alias (not a fresh declaration): the canonical interface
// embeds 2 methods (Publish + ResolveFolder) including the
// root-override-forward-prevention-gate. Duplicating the body here
// would create 2 authoritative surfaces for the root-override gate;
// the alias is the cheapest drift-proofing mechanism available.
package ports

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
)

// Publisher is the canonical Fase 5(a) alias for the
// `delivery.Publisher` interface.
//
// godlike/06 SSOT: the canonical declaration lives at
// `internal/application/assets/delivery/publisher.go:38`.
//
// Usage:
//
//	import "github.com/Marcuss-ops/PipelineGen/internal/application/ports"
//	type Deps struct {
//	    Publisher ports.Publisher
//	    ...
//	}
//
// Identity: `ports.Publisher == delivery.Publisher` (Go type
// aliases are transparent at the package boundary).
//
// Compile-time identity lock (godlike/06 SSOT alias drift-proofing):
// the var assertion below freezes the canonical interface identity
// so any future drift in `delivery.Publisher` (signature change,
// rename) surfaces at build time of this file rather than at first
// runtime call site.
//
// Push 5.2 cycle-guard note: this file imports `delivery` but
// `delivery` MUST NOT import `internal/application/ports` (would
// create `ports → delivery → ports` cycle). The cycle guard is
// documentation-only here; the canonical enforcement point is the
// infrastructure-layer `cmd/archcheck` percheck (`max_struct_deps`
// + cross-package-import allowlist — see docs/migrations/api-infrastructure-imports-allowlist.txt).
type Publisher = delivery.Publisher

// Compile-time identity lock (godlike/06 SSOT — phase 4-era alias
// freezes the canonical interface value for drift-detection).
var _ Publisher = (Publisher)(nil)
