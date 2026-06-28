# PR-VO-B1-C1 bundle — voiceover P0→P1 hardening (June 2026)

Four sequential commits that continue the canonical voiceover hardening
chain after the PR-VO-A bundle closed P0 risk. Each commit is
independently revertable; **this document is the authoritative
meta-index** for "what B1..C1 do, collectively, when treated as one
bundle". The "P1/P2 work" pointer that PR-VO-A's bundle deferred to
this doc is now resolvable: every named item there is **closed by**
one of B1+B2+B3+C1. See [`docs/voiceover/p0-bundle-A1-A6.md`](p0-bundle-A1-A6.md)
for the prior bundle.

> **Bundle integration**: PR-VO-A (atomic + identity + path + accounting)
> closed P0 risk. PR-VO-B1-C1 closes **P1 hardening**: Drive upload
> boundary, group/locale identity, sync dedupe key, and HTTP endpoint
> unification, with the same zero-legacy doctrine (godlike/07).
> Operationally, A and B1-C1 are independent tests of voiceover integrity
> at two maturity tiers — A blocks the basic correctness floor; B1-C1
> make the integration safe for multi-account production use.

## Per-commit index

| #  | Commit     | Subject                                                       | Files (scope)                                                                                  |
| -- | ---------- | ------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| B1 | `73c44aca` | Drive upload split Processor↔Lifecycle (DriveUploaderPort)    | `internal/application/voiceover/`, `internal/infrastructure/drive/`                            |
| B2 | `1cdf35e6` | propagate metadata + StyleGroup verbatim through processLanguage + resolveDestination | `internal/application/voiceover/process.go`, `internal/application/voiceover/types.go`        |
| B3 | `5cd2a1a4` | sync dedupe by `drive_file_id` + BCP-47/compact locale parser | `pkg/localeutil/locale.go`, `internal/application/voiceover/process.go`, `internal/application/voiceover/process_dedupe_test.go` |
| C1 | `c2867b90` | `/generate` unification + RFC 8594 Sunset deprecation (90-day) | `internal/api/assets/voiceover/handler.go`, `internal/application/voiceover/types.go`, `architecture/deprecations.yaml`, `docs/api/ACTIVE_API_GENERATED.md`, `CHANGELOG.md`, `internal/api/assets/voiceover/handler_pr_vo_c1_test.go` |

> **Audit-trail note**: row subjects are the **subject SUFFIX** taken
> from each commit's subject line. Drift between this table and the
> commit history is a `git show -s --format=%s <sha>` mismatch to
> fix, not a soft-update preference.

## Cumulative risk coverage

| Risk category                                                                                              | Closed by    | Status    |
| ---------------------------------------------------------------------------------------------------------- | ------------ | --------- |
| Drive upload boundary (Processor vs Lifecycle) — explicit port abstraction                                  | B1           | ✅ closed |
| Group routing destination resolution (legacy `/generate-with-group` endpoint)                              | C1           | ✅ closed |
| Locale identity (en-US, en_US, case-insensitive lookup; 3-letter and digit-only forms rejected)           | B3           | ✅ closed |
| `StyleGroup` styling-cohort bucket surviving JSON round-trip through `processLanguage` + `resolveDestination` | B2         | ✅ closed |
| Sync dedupe keying (drive_file_id over content-hash for upload dedup)                                       | B3           | ✅ closed |
| HTTP-API routing dispersion (separate handler per routing strategy)                                         | C1           | ✅ closed |
| Backward-compatibility contract for legacy clients (90-day RFC 8594 Sunset)                                 | C1           | ✅ closed |
| Cross-team deprecation headers (RFC 9745 + 8288 successor-version + RFC 8594)                              | C1           | ✅ closed |

> The previous row "Single source of truth for voiceoverSunsetDate"
> is folded into the cross-team deprecation row above: the constant
> in `handler.go`, the entry in `architecture/deprecations.yaml`, and
> the comment in `CHANGELOG.md` form a 3-way sync that survival of the
> 90-day window depends on. C1 ships it but does not gate it (a
> future CI gate will).

## Per-PR contract details

### B1 — Drive upload split (`73c44aca`)

`audioProcessor` previously mixed local TTS synthesis with the
Drive upload lifecycle; the two concerns share **zero** business
state but were colocated in the same Process step. B1 splits them
behind `DriveUploaderPort` (per `AGENTS.md` Pattern 0): the local
synthesis step now writes to a known temp path; the upload step is
delegated to a typed port that the Drive infrastructure adapter
implements. `var _ application.DriveUploaderPort = (*Adapter)(nil)`
compile-time assertion lives in `internal/infrastructure/drive/`. The
composition root wires the adapter to the service via
`NewService(ServiceDeps{...})`.

### B2 — Metadata + `StyleGroup` propagation (`1cdf35e6`)

`StyleGroup` is the new **coarse-grained style cohort** bucket
alongside `Group` (specific folder) and `SubfolderName` (leaf
subfolder). It surfaces in the per-asset `metadata.json` manifest
under key `style_group` so downstream consumers (Qdrant re-rankers,
style-cohort analytics, audit replay) can recover the original
selection verbatim. B2 propagates `StyleGroup` from the originating
`DestinationRequest` through `resolveDestination` → `BatchRequest`
→ JSON payload (via the existing `style_group` serialization in
`BatchRequest.PayloadMap`) → worker re-hydration → `processLanguage`.
`omitempty` semantics preserve pre-B2 callers' payload shape (no
`style_group` key appears when `StyleGroup` is unset).

### B3 — Sync dedupe by `drive_file_id` + locale parser (`5cd2a1a4`)

**Locale**:
- New leaf package `pkg/localeutil/locale.go` parses BCP-47 + compact
  forms (`en-US`, `en_US`, `EN_us`) into a normalized representation
  (`Compact` = `enUS`, `BCP47` = `en-US`). Regex gate
  `^([a-zA-Z]{2})(?:[_-]([a-zA-Z]{2}))?$` rejects 3-letter languages
  (`eng`), digit-only forms (`12-34`), and trailing ANSI suffixes
  (`en-12`). `IsValid()` mirrors parsed-set membership; `String()`
  returns BCP-47 (cross-team canonical form).

**Sync dedupe**: the post-upload gate now keys on `drive_file_id`,
NOT content-hash. The legacy content-hash key was redundant when the
same Drive file was reachable across the dedupe window (e.g. the
same file re-uploaded after a partial `voiceovers` rollback).
Helper `applyDedupeByDriveFileID(ctx, db, currentID, driveFileID) (*existingVoiceoverRow, int)`
returns the inode-canonicalising existing row (oldest by `created_at`)
plus a count for ambiguity surfacing. SQL uses
`COUNT(*) OVER()` (SQLite ≥3.25 / 3.45+ in production hosts),
`ORDER BY created_at ASC, id ASC LIMIT 1` for deterministic pickup,
and `AND id != ?` self-fence.

**Known limitations** (deferred to v2):
- Drive HEAD refresh on stale `DriveLink` (folder-move staleness) —
  helper accepts link verbatim. PR-VO-D1 is the followup.
- Qdrant outbox gap when the dedupe gate fires (bypasses
  `swapVoiceoverRow`'s `EnqueueIndexEvent`) — sync-service layer
  owns the fix.

### C1 — `/generate` unification (`c2867b90`)

`destination.kind` selects routing strategy on the canonical
`POST /api/voiceover/generate` endpoint. The four-case dispatch
table (must stay in sync with the docstring in
`internal/application/voiceover/types.go::DestinationRequest.Kind`):

| `Kind`         | Required fields                            | Failure mode |
| -------------- | ----------------------------------------- | ------------ |
| `""` (default) | nothing                                   | legacy auto-detect (configured VoiceoverFolder; service-layer reports if absent) |
| `"group"`      | `destination.group` non-empty              | 400 empty group; **501** missing `GroupsResolver`; 404 (with available list) unknown group |
| `"explicit"`   | `destination.folder_id` non-empty          | 400 empty FolderID |
| `<other>`      | treated as `""`                           | legacy auto-detect |

Handler routes through `service.GenerateWithDestination(ctx, text, language, filename, *DestinationRequest)`
whenever a non-empty `FolderID` is set (post `GroupsResolver` for
`"group"`, direct for `"explicit"`, or caller's auto-detect hint).
Legacy `h.service.Generate(...)` shortcut remains the **only** path
taken by callers that send NO `destination` field at all (100%
back-compat per the doc-only-bundle rule).

**Deprecation contract** (the `/generate-with-group` legacy
endpoint, kept for 90 days):

| Header                         | Value                                              | Source                                 |
| ------------------------------ | -------------------------------------------------- | -------------------------------------- |
| `Deprecation` (RFC 9745)       | `true`                                              | per-invocation `c.Header("Deprecation", "true")` |
| `Sunset` (RFC 8594)            | `Sat, 26 Sep 2026 00:00:00 GMT`                     | `voiceoverSunsetDate` constant           |
| `Link` (RFC 8288)              | `</api/voiceover/generate>; rel="successor-version"` | relative URI-reference successor-pointer |
| `legacy_voiceover_route_invocations_total` (Prometheus) | monotonically incremented per invocation | `LegacyVoiceoverDeprecationCount()` admin readback |

Cross-team contract: the Sunset header value is pinned in 3 places
that **must** stay in sync — the constant in `handler.go`, the
`removal_date` field in `architecture/deprecations.yaml#PR-VO-C1`,
and the comment in `CHANGELOG.md#PR-VO-C1`. A future CI gate is
planned (carried over as deferred follow-up).

**Build-blocker recovery (PR-VO-C1 added side-effect)** —
`internal/application/voiceover/process.go` had a stray `/*/`
at line 30 in HEAD — a C-style block comment-start without a
matching `*/`, which made the Go parser treat the rest of the file
as one unterminated block comment. PR-VO-C1's commit removes the
line entirely. Zero behavior change.

## Tests pinned by the bundle

| PR  | Test file                                                    | Cases / scope                                                                              |
| --- | ------------------------------------------------------------ | ------------------------------------------------------------------------------------------ |
| B1  | `internal/infrastructure/drive/*_test.go` + `internal/application/voiceover/service_test.go` | DriveUploaderPort contract round-trip (uploads route via adapter; failures surface as typed port errors). |
| B2  | `internal/application/voiceover/process_metadata_test.go`    | StyleGroup + metadata verbatim through processLanguage + resolveDestination (incl. JSON round-trip; legacy callers' payload shape preserved). |
| B3  | `pkg/localeutil/locale_test.go`                             | 16 cases: 6 happy (en-US, en_US, EN_us) + 8 rejected (eng, 12-34, en-XX, ANSi-suffix, digit-only, 1-letter, 3-letter region) + 2 helper (whitespace-trim, internal-whitespace reject, IsValid-mirror). |
| B3  | `internal/application/voiceover/process_dedupe_test.go`      | 8 cases: empty input (nil, 0); matching-different-id (row, 1); **NULL-coalesce-to-empty** (closes the original test-failure trigger); same-ID fence; no match (nil, 0); unrelated (nil, 0); multi-existing ambiguity-count (row, N>1); cancelled context (nil, 0). |
| C1  | `internal/api/assets/voiceover/handler_pr_vo_c1_test.go`    | 5 deprecation contract smoke tests: `voiceoverSunsetDate` IMF-fixdate format; RFC 9745 + 8594 + 8288 Link triple on `/generate-with-group`; Prometheus counter increment on each invocation; `LegacyVoiceoverDeprecationCount` aggregate readback; helper does NOT alter response status. |

## Architectural patterns reaffirmed

1. **Direct-to-main, doc-only bundle (Git-Lesson-2)** — same convention
   as PR-VO-A: each PR lands individually on `main`, this doc is the
   meta-index. No `--no-ff`, no `--force`, no topic branch.
2. **AGENTS.md Pattern 0** — narrow Go-structural port with compile-time
   assertion (B1 `DriveUploaderPort`; mirrors the youtube/ports flow
   from PR1.7).
3. **AGENTS.md Pattern 6** — additive request/payload struct: C1 adds
   `destination.kind` (zero-value safe via `omitempty`; legacy callers
   untouched). The validator's `400 hard` on `empty FolderID` for
   `Kind="explicit"` is the canonical godlike/07 §"No fake availability"
   pattern (the explicit opt-in gets a deterministic failure, not a
   silent fallback to the config-level folder).
4. **AGENTS.md Pattern 8** — thin API transport: voiceover handler
   does routing/dispatch only; `drive_file_id` resolution + dedupe
   gate live in `internal/application/voiceover/process.go`. No
   business logic in `internal/api/`.
5. **godlike/06 (data/config ownership)** — unified through the
   bundle: SQLite voiceovers table is the deduplication surface
   (`process_dedupe_test.go` mirror); Qdrant projection is a derived
   view (gap: the dedupe gate bypasses `EnqueueIndexEvent` —
   deferred to PR-VO-D1 sync-service).
6. **godlike/07 (zero legacy doctrine)** — C1's 90-day Sunset
   deprecation is the canonical EXPAND/CUTOVER pair for HTTP-API
   unification. The legacy `/generate-with-group` body is preserved
   100% byte-identical during the window (the Sunset headers are
   the **only** new surface on the old route).

## Future P1/P2 work (post-bundle)

- **PR-VO-D1** — sync-service layer to close the Qdrant outbox gap
  when B3's dedupe gate fires (currently bypasses
  `EnqueueIndexEvent`). Canonical owner: voiceover sync service
  (`internal/application/voiceover/sync/`).
- **PR-VO-D2** — Drive HEAD refresh on stale `DriveLink` / `DriveFileID`
  (folder-move staleness). Today the existing row's `DriveLink` is
  consumed verbatim; a Drive HEAD request per-lookup would close
  the staleness window.
- **PR-VO-D3** — full handler integration tests for the unified
  `/generate` endpoint. `handler_pr_vo_c1_test.go` currently
  pins only the deprecation contract (Smoke tests); full request-flow
  tests require a stub-package for `groupsResolver`, `service`,
  `jobsSvc`, `syncService` — deferred because the
  `internal/api/assets/voiceover/` directory has no such stub
  pattern yet (the existing `gate_test.go` walks prohibited-pattern
  strings, not behaviour).
- **PR-VO-E1** — promote the voiceoverSunsetDate cross-team sync
  hazard to a CI gate: `// validate: voiceoverSunsetDate ==
  architecture/deprecations.yaml#PR-VO-C1.removal_date`. Drift
  here breaks the cross-team Sunset contract silently.

## References

- Prior bundle: [`docs/voiceover/p0-bundle-A1-A6.md`](p0-bundle-A1-A6.md)
- Canonical architecture: [`../../ARCHITECTURE.md`](../../ARCHITECTURE.md)
- Agent-facing rules: [`../../AGENTS.md`](../../AGENTS.md)
- Qdrant projection doctrine (B1-C1's only non-closed risk):
  [`../architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md#qdrant-projection`](../architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md)
- Zero legacy doctrine (C1's 7-phase deprecation is the model):
  [`../architecture/godlike/07_ZERO_LEGACY_POLICY.md`](../architecture/godlike/07_ZERO_LEGACY_POLICY.md)
- Port abstraction pattern (B1's reference):
  [`../../AGENTS.md#pattern-0--port-abstraction-layer-june-2026-pr17-followup`](../../AGENTS.md)
- Active API table: [`../api/ACTIVE_API_GENERATED.md`](../api/ACTIVE_API_GENERATED.md)
- Deprecation manifest: [`../../architecture/deprecations.yaml`](../../architecture/deprecations.yaml)
