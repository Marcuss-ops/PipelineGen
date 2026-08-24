// Package app — wiring of the canonical stockbuild.Handler (P0-2).
//
// BuildStockbuildHandler is the SOLE canonical composition helper
// for stockbuild.Handler. It wires:
//
//   - The canonical subjects.Resolver (P0-1, signed-off commit-pair).
//   - The canonical steps.Store (Stock Cutover §12-3 — the
//     execution_steps ledger).
//   - 8 PhaseBody implementations — one per canonical phase.
//
// godlike/06 SSOT — exactly one canonical owner per fact:
//
//   - stockbuild.NewHandler is the SOLE place that knows how to
//     bind subjects.Resolver + steps.Store + 8 phase bodies.
//
//   - BuildStockbuildHandler is the SOLE place in `internal/app/`
//     that knows how to construct it (the composition root).
//
// Phase bodies: today shipped as stubs that return
// stockbuild.ErrPhaseNotImplemented. The P1 follow-up lands the
// real primitive integrations (planner for SEARCH/SELECT,
// stockpipeline for DOWNLOAD/EXTRACT/UPLOAD, media_assets atomic
// writer for PERSIST, outbox emitter for INDEX, counts reader for
// VERIFY). Until then, a stockbuild run that reaches a non-stub
// phase surfaces a typed error rather than a silent-success
// (godlike/07 NO-FAKE-AVAILABILITY).
package wiring

import (
	"context"
	"database/sql"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/stockbuild"
	subjectsrepo "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/subjectsrepo"
)

// BuildStockbuildHandler constructs the canonical stockbuild.Handler
// from the primary SQLite DB handle. The handler is the SINGLE
// owner of the youtube.stock.build.v1 workflow; every composition
// root MUST route through this constructor (godlike/06 SSOT).
//
// `db` MUST be the canonical primary media DB. The 8 phase bodies
// (today: stubs) read subjects / steps / orchestrator state from
// this same DB handle; reading from a parallel DB would break the
// execution_steps join invariant.
//
// Returns the typed-port-ready handler. Composition roots bind it
// to the kerneljob.MutableJobRegistry via stockbuild.RegisterBinding.
func BuildStockbuildHandler(log *zap.Logger, db *sql.DB) (*stockbuild.Handler, error) {
	if db == nil {
		return nil, stockbuild.ErrStepsStoreNotWired // closest sentinel; composition bug
	}
	subjectsResolver := subjectsrepo.NewResolver(db)
	stepsStore := steps.NewSQLiteStore(db)
	phases := stubPhases(log)
	return stockbuild.NewHandler(log, subjectsResolver, stepsStore, phases)
}

// stubPhases returns the 8 stub PhaseBody implementations. Each
// returns stockbuild.ErrPhaseNotImplemented when invoked, so a
// stockbuild run surfaces a typed failure rather than a
// silent-success (godlike/07 NO-FAKE-AVAILABILITY).
//
// The P1 follow-up swaps each stub for the real primitive:
//
//   - 01_search   → planner (subject-led YouTube candidate search)
//   - 02_select   → planner.Select (category-weighted top-N)
//   - 03_download → stockpipeline.SourceStager.StageSource
//   - 04_extract  → stockpipeline.Cutter.Cut
//   - 05_upload   → stockpipeline.Publisher (Drive upload via outbox)
//   - 06_persist  → media_assets atomic writer (transactional)
//   - 07_index    → outbox event emitter (transactional outbox)
//   - 08_verify   → counts reader (sot-stock-verify supersedes)
//
// Each stub today is a closure capturing log.
func stubPhases(log *zap.Logger) map[stockbuild.PhaseName]stockbuild.PhaseBody {
	stub := func(name stockbuild.PhaseName) stockbuild.PhaseBody {
		return &stubPhaseBody{name: name, log: log}
	}
	out := make(map[stockbuild.PhaseName]stockbuild.PhaseBody, 8)
	for _, p := range stockbuild.AllPhases() {
		out[p] = stub(p)
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// stubPhaseBody
// ────────────────────────────────────────────────────────────────────────────

// stubPhaseBody is the canonical stub PhaseBody. Returns
// stockbuild.ErrPhaseNotImplemented for every Run invocation.
//
// godlike/07: a stub that returns nil-error would be a silent-
// success false-positive; the typed-error return is canonical so
// the operator dashboard reflects the actual phase state.
type stubPhaseBody struct {
	name stockbuild.PhaseName
	log  *zap.Logger
}

// Run implements stockbuild.PhaseBody.
func (s *stubPhaseBody) Run(_ context.Context, _ stockbuild.PhaseInput) error {
	if s.log != nil {
		s.log.Warn("stockbuild: phase body is a stub (P1 follow-up wires the primitive)",
			zap.String("phase", string(s.name)))
	}
	return stockbuild.ErrPhaseNotImplemented
}

// Compile-time assertion: *stubPhaseBody satisfies stockbuild.PhaseBody.
var _ stockbuild.PhaseBody = (*stubPhaseBody)(nil)
