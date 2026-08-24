// Package app — artlist_runs_adapter.go (PR-ARTLIST-PERSIST-FIX, 2026-07-04)
//
// Composition-root adapter that bridges
//
//	artlist.RunRepository (port in internal/capabilities/assets/providers/artlist/ports.go)
//	sqlite/assets.RunRepository (infra side: internal/infrastructure/database/sqlite/assets/artlist_runs_repository.go)
//
// Without this adapter, the import cycle would be: artlist pkg imports
// sqlite/assets pkg (for ClipsRepository) AND sqlite/assets pkg's
// artlist_runs_repository.go imports artlist pkg (for the port type).
// The adapter lives here in composition-root because that's the only
// layer that's already authorised to import BOTH leaf dependencies
// (artlist pkg + sqlite/assets pkg).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - artlist.RunRepository — SOLE canonical port seen by NewService
//   - sqlite/assets.RunRepository — SOLE canonical concrete in the
//     infra layer (write-closure fact: only this concrete writes
//     artlist_runs rows)
//   - *artlistRunsRepoAdapter — the SINGLE translation site between
//     the two interface names (godlike/06 one-canonical-owner-per-fact)
//     — future drift in any of the three surfaces surfaces as a
//     build failure at the compile-time pin below.
//
// godlike/07 minimum-blast-radius: configuration-only changes (no
// field renames; the field-to-field translation is the canonical
// mapping per the schema-reconciliation review of 2026-07-04).
package app

import (
	"context"
	"fmt"

	artlist "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	artlistsql "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/artlist"
)

// artlistRunsRepoAdapter is the canonical composition-root adapter
// that satisfies artlist.RunRepository. Translates
// artlist.RunRecord → sqlite/assets.RunRecord field-by-field, then
// delegates the actual SQL work to the *ArtlistRunsRepository
// concrete (composition-pre-created).
type artlistRunsRepoAdapter struct {
	concrete *artlistsql.ArtlistRunsRepository
}

// NewArtlistRunsRepoAdapter wraps the SQLite-backed concrete behind
// the artlist.RunRepository port. Returns nil for nil concrete
// (godlike/07 minimum-blast-radius: test fixtures may construct
// adapters with nil concrete to exercise nil-handling paths;
// production callers must pre-validate via WireArtlist's
// NewArtlistRunsRepository error return).
func NewArtlistRunsRepoAdapter(concrete *artlistsql.ArtlistRunsRepository) *artlistRunsRepoAdapter {
	return &artlistRunsRepoAdapter{concrete: concrete}
}

// compile-time pin: *artlistRunsRepoAdapter satisfies
// artlist.RunRepository. Drift in either surface's signature
// surfaces as a build failure here rather than a runtime panic.
var _ artlist.RunRepository = (*artlistRunsRepoAdapter)(nil)

// RunRecord translates the application-layer RunRecord into the
// infra-layer RunRecord and delegates to the concrete. NULL-guard on
// the concrete is godlike/07 minimum-blast-radius: a nil concrete
// (impossible in production; reachable in test fixtures via
// NewArtlistRunsRepoAdapter(nil)) returns a typed sentinel so the
// caller can branch on intent.
//
// godlike/06 SSOT (column-level reconciliation 2026-07-04): the
// field-to-field mapping below mirrors the artlist_runs schema
// verbatim. Schema additions require a corresponding update here +
// in the concrete; column drops cascade through both layers.
func (a *artlistRunsRepoAdapter) Record(ctx context.Context, rec artlist.RunRecord) error {
	if a == nil || a.concrete == nil {
		// Defensive: test-only nil-receiver path. Production never
		// reaches here because WireArtlist pre-validates the
		// concrete via NewArtlistRunsRepository's error return.
		return nil
	}
	translated := artlistsql.RunRecord{
		RunID:        rec.RunID,
		Term:         rec.Term,
		Status:       rec.Status,
		RootFolderID: rec.RootFolderID,
		TagFolderID:  rec.TagFolderID,
		RequestedN:   rec.RequestedN,
		FoundN:       rec.FoundN,
		ProcessedN:   rec.ProcessedN,
		SkippedN:     rec.SkippedN,
		FailedN:      rec.FailedN,
		ErrorMessage: rec.ErrorMessage,
	}
	return a.concrete.Record(ctx, translated)
}

// LatestRun bridges the infra-layer LatestRunRow → application-layer
// LatestRunSummary (the typed read-shape surfaced via
// DiagnosticsResponse.LatestRun).
//
// godlike/06 SSOT: this is the SINGLE translation site between the
// two struct definitions. Future renames in either surface surface
// as a build failure at the compile-time pin above.
//
// (nil, nil) on empty-table: the application-layer DiagnosticsService
// interprets nil as "no runs yet" and omits the LatestRun field from
// the JSON response entirely (godlike/07 honest-about-fresh-install).
func (a *artlistRunsRepoAdapter) LatestRun(ctx context.Context) (*artlist.LatestRunSummary, error) {
	if a == nil || a.concrete == nil {
		// Defensive: same nil-handling discipline as Record above.
		return nil, nil
	}
	row, err := a.concrete.LatestRun(ctx)
	if err != nil {
		return nil, fmt.Errorf("artlistRunsRepoAdapter.LatestRun: %w", err)
	}
	if row == nil {
		// Empty-table (fresh install) — forward nil verbatim.
		return nil, nil
	}
	return &artlist.LatestRunSummary{
		RunID:     row.RunID,
		Term:      row.Term,
		Status:    row.Status,
		Error:     row.ErrorMessage,
		CreatedAt: row.CreatedAt,
	}, nil
}
