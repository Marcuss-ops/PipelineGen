// Package staged — errors.go: canonical typed-error sentinel for
// the COMPLETE-side staged artifact lifecycle (post-upload,
// pre-publish on Drive).
//
// Why a dedicated package: the cutover wave's contract is that
// "is this artifact ready and verifiable?" is the only branch the
// caller takes. Splitting the package from the download-side
// `assets.SourceStager` (see internal/application/assets/ports.go)
// keeps the two state machines mentally separate, per godlike/06
// one-owner-per-fact.
package staged

import "errors"

// ErrStagedArtifactMissing is the canonical godlike/07 typed sentinel
// returned by the Resolver when any step in the staged-lookup chain
// fails:
//
//  1. IndexStore returns "" or an error (DB row absent) — the
//     artifact is not in the staged surface.
//  2. os.Stat fails on the looked-up local path (file deleted,
//     moved, TTL'd by the standalone cleanup daemon, etc.) — the
//     DB row is stale.
//  3. files.HashFile fails on the local file (I/O error, truncated
//     zeros, silent disk corruption) — the staged payload is
//     unverifiable for downstream ArtifactPreparation.
//
// All three failure paths return wrapped via fmt.Errorf("...: %w",
// ErrStagedArtifactMissing) so upstream callers branch on
// errors.Is(err, staged.ErrStagedArtifactMissing) without
// needing to distinguish internal failure detail. The granular
// root error is preserved through the %w chain for the operator
// log scraper.
//
// Scope decision (per godlike/07 typed-error contract): a SINGLE
// sentinel was preferred over three separate sentinels
// (ErrDBNotFound, ErrFileNotFound, ErrHashFailed) because the
// cutover-wave caller branches only on "is this artifact usable?"
// — per-stem sentinels would expose internal diagnostic detail
// at the cutover boundary without enabling any distinct call-site
// behavior, which is the canonical godlike/07 anti-pattern of
// over-typed error surfaces.
var ErrStagedArtifactMissing = errors.New("staged: artifact missing or unverifiable on the staged surface")
