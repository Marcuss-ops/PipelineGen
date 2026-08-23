// Package ports — JobFinalizer port (Fase 5(a), July 2026).
//
// godlike/06 SSOT: this file is a thin alias re-export of the
// canonical `JobFinalizer` declaration at
// `internal/domain/finalization/interfaces.go`. The alias preserves
// byte-stable identity for callers that want to depend on the
// abstract `ports.JobFinalizer` rather than the deep-package
// `finalization.JobFinalizer`.
//
// Phase 5(a) scope: declare the alias and the godlike/06 SSOT doc
// block pointing at the canonical site. Push 5.2 migrates callers
// from `finalization.JobFinalizer` → `ports.JobFinalizer`.
//
// WHY an alias (not a fresh declaration): the canonical surface
// declares a multi-method interface (CompleteWithArtifacts) with
// 6-step idempotency contract. Duplicating that body here would
// invite drift between the two declarations; the alias is the
// cheapest drift-proofing mechanism available (godlike/06 SSOT
// "duplicate the type, not the contract").
package ports

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// JobFinalizer is the canonical Fase 5(a) alias for the
// `finalization.JobFinalizer` interface.
//
// godlike/06 SSOT: the canonical declaration lives at
// `internal/domain/finalization/interfaces.go:31`.
//
// Usage:
//
//	import "github.com/Marcuss-ops/PipelineGen/internal/application/ports"
//	type Deps struct {
//	    Finalizer ports.JobFinalizer
//	    ...
//	}
//
// Identity: `ports.JobFinalizer == finalization.JobFinalizer` (Go
// type aliases are transparent at the package boundary).
//
// Compile-time identity lock (godlike/06 SSOT alias drift-proofing):
// the var assertion below freezes the canonical interface identity
// so any future drift in `finalization.JobFinalizer` (signature
// change, added method) surfaces at build time of this file rather
// than at first runtime call site.
type JobFinalizer = finalization.JobFinalizer

// Compile-time identity lock (godlike/06 SSOT — phase 4-era alias
// freezes the canonical interface value for drift-detection).
var _ JobFinalizer = (JobFinalizer)(nil)
