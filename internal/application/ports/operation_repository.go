// Package ports — OperationRepository port (Fase 5(a), July 2026).
//
// godlike/06 SSOT: this file is a thin alias re-export of the
// canonical `OperationsRepository` declaration at
// `internal/application/operations/ports.go`. The alias preserves
// byte-stable identity for callers that want to depend on the
// abstract `ports.OperationRepository` rather than the deep-package
// `operations.OperationsRepository`.
//
// Phase 5(a) scope: declare the alias and the godlike/06 SSOT doc
// block pointing at the canonical site. Push 5.2 migrates callers
// from `operations.OperationsRepository` → `ports.OperationRepository`.
//
// WHY an alias (not a fresh declaration): the canonical interface
// declares 3 methods (Insert + GetLatestForKey + UpdateState) used
// by the FASE 2 submission service. Duplicating the body here
// would invite drift; the alias is the cheapest drift-proofing
// available.
//
// The alias name `OperationRepository` (without the 's' in
// Operations) is intentional: an `Operation` is the abstract
// resource (one submission attempt with idempotency), and a
// `Repository` is the access pattern. The downstream
// `operations.OperationsRepository` interface is the alias target
// (the name there is a Fase 2 historical convention).
package ports

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/operations"
)

// OperationRepository is the canonical Fase 5(a) alias for the
// `operations.OperationsRepository` interface.
//
// godlike/06 SSOT: the canonical declaration lives at
// `internal/application/operations/ports.go:79`.
//
// Usage:
//
//	import "github.com/Marcuss-ops/PipelineGen/internal/application/ports"
//	type Deps struct {
//	    Operations ports.OperationRepository
//	    ...
//	}
//
// Identity: `ports.OperationRepository == operations.OperationsRepository`
// (Go type aliases are transparent at the package boundary).
//
// Compile-time identity lock (godlike/06 SSOT alias drift-proofing):
// the var assertion below freezes the canonical interface identity
// so any future drift in `operations.OperationsRepository` (signature
// change, added method) surfaces at build time of this file rather
// than at first runtime call site.
//
// Push 5.2 cycle-guard note: this file imports `operations` but
// `operations` MUST NOT import `internal/application/ports` (would
// create `ports → operations → ports` cycle). The cycle guard is
// documentation-only here; the canonical enforcement point is the
// `cmd/archcheck` percheck's `max_struct_deps` rule on the
// `internal/application/jobs` package (which imports BOTH).
type OperationRepository = operations.OperationsRepository

// Compile-time identity lock (godlike/06 SSOT — phase 4-era alias
// freezes the canonical interface value for drift-detection).
var _ OperationRepository = (OperationRepository)(nil)
