// Package usecase — text_track_database.go is the database-side leaf
// of the text-track 6-file split. It owns ONLY the READY-only
// fan-out lookup against the canonical text_track TextTrackRepository.
//
// AGENTS.md / godlike/06 SSOT split (July 2026): the orchestrator
// (text_track_resolver.go) is the SOLE canonical site for:
//   - asset.Normalize() calls (BCP-47 normalisation)
//   - ResolvedTextBundle provenance assembly
//   - preferred-language fan-out policy (first-found wins)
//
// This leaf is intentionally a one-helper file so the orchestrator's
// priority-2 path stays declarative without inlining a repo lookup.
// The orchestrator normalises the (asset, language, kind) triple
// BEFORE calling this helper, so the input normalizedLang is already
// a canonical BCP-47 code ("und" being the explicit "already-known
// undetermined" marker — the orchestrator filters "und" before
// reaching this helper to avoid a full-table scan).
//
// godlike/07 honest lock: a nil repo is propagated as (nil, nil, nil)
// so the orchestrator can keep its fail-closed path without each
// leaf re-implementing the nil-tolerance guard.
package usecase

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// fetchDatabaseTrackRaw performs the canonical READY-only lookup
// against the given TextTrackRepository. The orchestrator plumbs
// the (already-normalized) language code so this leaf does NOT
// re-derive the BCP-47 rules (godlike/06 SSOT — those rules live
// in internal/kernel/asset/bcp47.go).
//
// Returns (nil, nil, nil) when the repo is nil (orchestrator keeps
// its priority-2 fail-closed path). When the repo returns a row,
// the second return value carries the canonical []TimedCue slice
// (per Fase 4 port-surface widening) and is nil for rows without
// cue metadata.
func fetchDatabaseTrackRaw(
	ctx context.Context,
	repo detail.TextTrackRepository,
	clipID string,
	normalizedLang string,
	kind detail.TextTrackKind,
) (*detail.TextTrack, []detail.TimedCue, error) {
	if repo == nil {
		return nil, nil, nil
	}
	return repo.FindReady(ctx, clipID, normalizedLang, kind)
}
