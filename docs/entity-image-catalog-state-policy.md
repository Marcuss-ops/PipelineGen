# Entity Image Catalog: candidate state policy

## Scope

This policy applies to rows in `entity_image_catalog_candidates`. It is
separate from:

- `vidrush_provider_cache`, which is a temporary response cache;
- `entity_image_catalog_materializations.status`, which describes the local,
  Drive and content-addressed asset;
- semantic quality, which determines whether a candidate is eligible for the
  catalog at all.

The candidate state describes the **remote URL's catalog lifecycle**. A
materialized Drive asset can remain reusable even when the original remote URL
later becomes stale or broken.

## Canonical timestamps

`last_seen_at` is the catalog's last successful observation timestamp. It is
updated when a candidate is accepted from a provider refresh or when a
successful materialization verification marks the candidate `fresh`.

`entity_image_catalog_materializations.last_verified_at` is the last
verification time for the materialized asset. It does **not** replace
`last_seen_at` for remote URL state and it does not make a failed remote URL
selectable for a new download.

All comparisons use UTC and the application clock supplied to
`entitycatalog.ClassifyCandidateStatus`.

## States

| State | Entry condition | Normal selection | Refresh behavior |
|---|---|---:|---|
| `fresh` | Accepted observation age is `<= 7 * 24h` | Yes | No refresh solely because of age |
| `stale` | Accepted observation age is `> 7 * 24h` | Yes, as fallback | Refresh is recommended, but only performed when the usable pool is insufficient or force refresh is requested |
| `broken` | Acquire or verify failure for the URL | No | Excluded from normal pool; explicit provider refresh or successful validation may create/recover a candidate |
| `retired` | Explicit manual retirement | No | Terminal; never selected automatically |
| `active` | Legacy pre-migration value | Treated as `fresh`, then classified by timestamp | Read compatibility only; new writes use `fresh` |

The exact boundary is intentional: a candidate seen exactly seven days ago is
still `fresh`; one nanosecond beyond the boundary is `stale`.

The threshold is defined once in code as:

```go
entitycatalog.CandidateFreshAfter = 7 * 24 * time.Hour
```

## Deterministic classification

`entitycatalog.ClassifyCandidateStatus(now, candidate)` has these invariants:

1. `broken` and `retired` are preserved regardless of age.
2. A candidate without `last_seen_at` retains its stored state; the policy
   does not invent staleness for legacy rows or incomplete fixtures.
3. `fresh`/`active`/`stale` with a timestamp are classified from age only.
4. Age never converts a candidate directly to `broken`.
5. `AssessCandidateState` reports `fresh` and `stale` as usable; `broken` and
   `retired` are not usable.

During catalog lookup, a timestamped candidate whose effective state changed
is persisted. Therefore the normal lookup path makes the `fresh -> stale`
transition observable in SQLite rather than applying it only in memory.

## Explicit transitions

The bounded events in `entitycatalog.TransitionCandidateStatus` are:

| Event | Result |
|---|---|
| `provider_accepted` | `fresh` |
| `validation_succeeded` | `fresh` |
| `validation_failed` | `broken` |
| `manual_retirement` | `retired` |

Additional rules:

- `stale` is not a failure and never becomes `broken` because of age alone.
- `broken` is never used as fallback. It can recover only through an explicit
  successful refresh/validation path.
- `retired` is terminal in the state machine; provider results must not revive
  it implicitly.
- A successful candidate refresh writes `fresh` and updates `last_seen_at`.
- A failed acquire or verify writes `broken`; the candidate remains durable
  for audit and future explicit refresh decisions.

## Pool and refresh policy

State classification is combined with the existing candidate-pool policy:

- `fresh` and `stale` candidates count as usable;
- `broken` and `retired` candidates do not count;
- for a provider limit of 10, a pool of 8 usable candidates is sufficient;
- if the usable pool is insufficient, existing usable candidates are returned
  immediately as fallback and one provider refresh is attempted;
- `force_refresh` bypasses a sufficient pool and attempts one provider refresh;
- stale candidates are not refreshed on every lookup while the pool remains
  sufficient.

This preserves availability while preventing stale remote URLs from being
confused with known failures.

## Materialization interaction

A candidate with a materialization row is reusable from Drive only when the
materialization is `materialized` and has:

- `asset_id`;
- `file_hash`;
- `drive_link`.

That reuse path does not download the remote URL and does not upload/finalize a
new Drive asset. The remote candidate may still be `stale`; the verified Drive
asset is the durable source for reuse.

## Periodic recertification

A bounded maintenance job runs with a default interval of 24 hours and a
maximum batch of 100 candidates. It selects only:

- `fresh`/`active`/`stale` candidates whose `last_seen_at` is older than the
  seven-day freshness threshold;
- `broken` candidates with fewer than five validation attempts whose persisted
  `next_retry_at` is due.

A successful remote GET/decode/dimension check transitions the candidate to
`fresh`, resets attempts, and updates only the remote validation timestamps.
A failure keeps it `broken`, records the error, and schedules exponential
retry at 1h, 2h, 4h, then up to 24h; after five attempts no further automatic
retry is selected. The job does not call the materializer, delete files, or
modify `entity_image_catalog_materializations`: a verified Drive asset remains
usable even if its original remote URL later fails.

The retry fields are added by migration `228_entity_image_catalog_recertification.sql`.
Operators can override the cadence and batch with
`VELOX_ENTITY_IMAGE_RECERTIFICATION_INTERVAL` and
`VELOX_ENTITY_IMAGE_RECERTIFICATION_BATCH_SIZE`.

## Verification matrix

The policy is covered by deterministic tests in:

```text
internal/capabilities/images/entitycatalog/state_policy_test.go
```

The tests verify:

- exactly-at-threshold `fresh` behavior;
- `stale` one nanosecond after the threshold;
- stale remains usable and recommends refresh;
- `broken` and `retired` remain non-usable regardless of age;
- legacy rows without timestamps do not become stale by assumption;
- successful validation, failed validation and manual retirement transitions;
- unknown transition events fail closed.

The application integration test in
`internal/capabilities/scripts/adapters/entity_image_catalog_integration_test.go`
continues to verify fallback and refresh behavior for fresh, stale and broken
pools.
