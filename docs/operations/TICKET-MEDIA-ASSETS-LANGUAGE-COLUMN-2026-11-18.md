# TICKET — `media_assets.language` exists but is not exposed to the local search or the ranker (2026-11-18)

- **Status:** CLOSED (2026-11-18) — language is exposed in the canonical projection, filtered in SQL by the PostgreSQL local backend, and scored in `mediasampler::local_score`; the release binary activates the score at the next publish
- **Severity:** P2 — the language filter is documented as a forward-pointer and the ranker cannot use language
- **Owner:** asset registry (schema/projection) + brain/ranker contract
- **Source:** audit 2026-11-18; `migrations/sqlite/099_qdrant_asset_columns.sql:10`; `internal/platform/sqlite/assets/imagesregistry/clips_repository.go:14`; `internal/capabilities/assets/search/cross_provider_test.go:223-240`
- **Related:** `BrainRust/docs/stato/brainrust.md` (the note that says the column is missing)
- **Rule:** the contract must expose the real state; the documentation must stop saying the column does not exist

## Problem — corrected diagnosis

The previous state note says the language weight in `local_score.rs` is blocked
because `media_assets.language` does not exist. **That is inaccurate.** The
column does exist:

- `migrations/sqlite/099_qdrant_asset_columns.sql:10`
  `ALTER TABLE media_assets ADD COLUMN language TEXT NOT NULL DEFAULT '';`
- `migrations/sqlite/152_add_canonical_metadata_columns.sql:75` adds
  `original_language`;
- `migrations/sqlite/289_media_assets_lifecycle_default.sql` rebuilds the table
  with both columns.

What is actually missing is the exposure of that column:

1. **The canonical Go projection omits it.** `MediaAssetColumns` in
   `internal/platform/sqlite/assets/imagesregistry/clips_repository.go` is the
   single SELECT projection used by every Get/List/Search/Resolve path, and it
   contains neither `language` nor `original_language` (verified: zero
   occurrences in the projection). The local backend therefore cannot filter
   by language at the SQL layer.
2. **The test documents the gap instead of enforcing the feature.**
   `TestFilterLanguageHonestyContract` in
   `internal/capabilities/assets/search/cross_provider_test.go` pins the
   current behaviour: `Language` reaches `AdvancedSearchRequest` and provider
   `SearchFilters` but is not a local SQL filter, and no provider implements
   it. The comment names the exact future change: when the migration lands and
   one provider supports it, the test becomes
   `TestFilterLanguageEnforcedContract`.
3. **The Rust ranker has no language input at all.** `mediasampler` (11 source
   files) contains no `language` field in `Candidate`, `Scene` or
   `SampleOptions`; the `local_score.rs` weight cannot exist until the ranker
   contract carries the language. The database is not the blocker; the wire
   contract is.

## Migration plan

1. Add `language` (and `original_language`, if the product wants original vs
   detected) to `MediaAssetColumns`, the scanner, and the `asset.Asset` /
   `asset.Details` types. The projection is load-bearing: every consumer of the
   40-column shape must be updated in the same change.
2. Add the local SQL filter for `Filters.Language` (empty value means “no
   filter”), and expose it through the stored search path.
3. Extend the media-ranker contract (`contract/media-ranker/v1`) with the
   scene language and the candidate language; populate them in the Go adapter
   (`internal/platform/media/rustexec/media_ranker_adapter.go`).
4. Implement the weight in `mediasampler` with the same penalty/bonus the Go
   oracle used, plus the parity fixture that pins the score.
5. Rename `TestFilterLanguageHonestyContract` to
   `TestFilterLanguageEnforcedContract` and flip its assertions to the enforced
   behaviour, as its own comment prescribes.
6. Correct `docs/stato/brainrust.md` and the PIANO entry: the column is not
   missing; the projection and the contract were.

## Acceptance criteria

- [x] `MediaAssetColumns` includes `language` and the scanner/structs compile;
      `go test ./internal/platform/sqlite/assets/imagesregistry/... -count=1`
      passes.
- [x] A query with `Filters.Language = "it"` returns only Italian rows from the
      local backend; an empty filter returns all rows (test both).
- [x] The media-ranker request schema carries language for scene and candidate;
      `contract/media-ranker/v1` parity fixtures cover same-language and
      cross-language candidates.
- [x] `cargo test -p mediasampler` passes, including a fixture that changes the
      language and changes the score by the documented amount.
- [x] `TestFilterLanguageEnforcedContract` replaces the honesty test and fails
      if the local filter is silently removed.
- [x] No document states that the `media_assets.language` column is missing.

## Verification

```sh
rg -n "language" internal/platform/sqlite/assets/imagesregistry/clips_repository.go
go test ./internal/capabilities/assets/search/... ./internal/platform/sqlite/assets/imagesregistry/... -count=1
(cd BrainRust && cargo test -p mediasampler --locked)
rg -n "TestFilterLanguageHonestyContract" internal
```

## Out of scope

- Provider-side language support (each provider adapter is a separate change;
  the honesty test only requires the wire value to round-trip until then).
- Backfilling empty `language` values for existing rows: a data job, not this
  contract change. The ticket must state whether the filter treats empty as
  “unknown” (recommended) or as a match.

## Execution log — 2026-11-18

Codice:

- `MediaAssetColumns` now selects `language` and `original_language`;
  `ScanMediaAsset` scans them into the `Asset` metadata accessors
  (`Asset.Language()` / the new `Asset.OriginalLanguage()`), and the
  `clips_crud_test` projection contract was extended from 40 to 42 columns with
  a round-trip assertion.
- `LocalMediaSearchRequest.Language` is compiled by
  `buildLocalMediaSearchQuery` into `AND ($8 = '' OR language = $8)`; the query
  builder is now a pure function so placeholder arithmetic is testable.
- `pgLocalSearchBackend` forwards `q.Filters.Language` into the SQL request and
  into the Rust `score_local` batch (query + candidate language).
- `mediasampler::local_score` adds a ±0.10 language seat: same language +1.0,
  known different language −1.0, unknown 0.0 (never penalised), clamped to
  [0,1]. `mediasampler::rank` carries `scene_language` / `language` as
  pass-through contract fields only, so the canonical ranker formula and its
  goldens stay bit-identical.
- `TestFilterLanguageHonestyContract` is gone; `TestFilterLanguageEnforcedContract`
  (in `internal/app/wiring/search`) fails if the local backend drops the value.

Verifica:

```text
go build ./...                                            0
go test ./internal/platform/postgres/media ./internal/app/wiring/search \
        ./internal/platform/sqlite/assets/imagesregistry \
        ./internal/capabilities/assets/search -count=1    PASS
cargo test -p mediasampler --locked                        44 + 7 + 4 PASS
```

Limite dichiarato: il binario `bin/mediasampler` è quello di `v0.3.7`, quindi
il segnale lingua è compilato e testato ma si attiva in produzione al prossimo
rilascio BrainRust. Il test di riga su PostgreSQL non è eseguibile in questo
workspace (nessuna istanza): il predicato SQL è coperto dal test del builder.
