# Block 6 — Confirmation (June 2026)

## Status: ✅ CONFIRMED

Block 6 is a **documentation + metrics label refresh only** — no new
production code paths, no handler/service business logic changes,
no new owners or ports. The post-Block-4 EXPAND canonical surface
is verified and the Sunset machinery's Prometheus label set is
refreshed to align with the current observable set. Single commit
on `main` per AGENTS.md **Git-Lesson-2**; pre-commit review via the
code-reviewer-minimax-m3 subagent.

> **Authority**: Block 6 is the BACKFILL pre-step of the voiceover
> EXPAND/BACKFILL/CUTOVER/CONTRACT migration sequence (godlike/07
> §"Migration sequence"). Its scope is **documentation + metrics
> label values**, not new capability. Forward-pointers to PR-VO-D1+
> are listed at the end of this doc.

---

## The three claims (verified)

### Claim A — `/sync` is admin-only

`internal/api/assets/voiceover/handler.go::RegisterRoutes` no
longer registers `/sync` (Block 4 EXPAND slim). The /sync flow
lives on `internal/application/assets/reconciliation/voiceover/
service.go` and is reachable **exclusively** via
`cmd/admin/cleanup.go:507`:

```go
if err := root.Domains.VoiceoverSync.Sync(ctx); err != nil {
    ...
}
```

The legacy `internal/application/voiceover/sync/service.go` is
deprecated; the `reconciliation/voiceover` package is the canonical
owner (one-owner-per-fact, godlike/06). There is **no network
surface** for /sync — HTTP clients cannot reach it. Only
`pipelinegen cleanup` (admin CLI) drives the sync.

**Verification cross-reference**: `git grep "VoiceoverSync" cmd/`
returns a single call site at `cleanup.go:507`.

### Claim B — `/groups` resolution moved out of the Voiceover handler

`GenerateVoiceoversCommand` (`internal/application/voiceover/
command.go`) carries an optional `*DestinationRequest`. The
`DestinationRequest.Kind` field takes one of three string values:

| `Kind`           | Routing                                          | HTTP-layer involvement            |
| ---------------- | ------------------------------------------------ | --------------------------------- |
| `"group"`        | via `destination.Resolver` → `GroupsResolver`   | **none** (use case layer)         |
| `"explicit"`     | caller-supplied `FolderID` verbatim              | **none** (use case layer)         |
| `""` (legacy)    | auto-detect on available fields                  | **none** (use case layer)         |

The `Handler` struct (voiceover/handler.go) has **zero**
`groupsResolver` / `groups` fields — both were removed at Block 4
EXPAND. There is no handler-level `/groups` resolution at this
commit. `Destination.kind` is payload-driven and lives in the
use case (per Pattern 8 thin-transport rule — AGENTS.md).

**Verification cross-reference**: `git grep groupsResolver
internal/api/assets/voiceover/` returns nothing.

### Claim C — `legacyVoiceoverRouteInvocationsTotal` label set refreshed

The Prometheus counter `legacy_voiceover_route_invocations_total`
(Help text + readback iteration + variable-level comment block) has
been refreshed to align with the post-Block-4 EXPAND canonical
surface.

#### New label values

| Label value          | Source path                                                              | Status at Block 6                                  |
| -------------------- | ------------------------------------------------------------------------ | -------------------------------------------------- |
| `generate`           | `POST /api/voiceover/generate` → `Handler.Generate` (handler.go)         | canonical — pre-allocated for future observability |
| `sync`               | `cmd/admin/cleanup.go:507` → `reconciliation/voiceover.Service.Sync`    | admin-only — pre-allocated for future observability |
| `generate-with-group`| (legacy `/generate-with-group` HTTP route removed at Block 4 EXPAND)     | DEPRECATED — retained briefly for backwards-compat dashboard series during the 2026-06-28 → 2026-09-26 Sunset window; CONTRACTED at PR-VO-E1 |

#### Update surface

1. **`handler.go`** — CounterOpts `Help` text + var-block comment
   updated with the Block 6 label-set documentation.
2. **`handler.go::LegacyVoiceoverDeprecationCount`** — iteration list
   now reads `[generate, sync, generate-with-group]` (was the
   single-element `[generate-with-group]`). Doc-block on the function
   lists the Block 6 label-set pins.
3. **`handler_pr_vo_c1_test.go`** — file-level doc header refreshed
   to reflect the Block 6 multi-label iteration + the canonical
   forward-pointers (PR-VO-D3, PR-VO-E1). The actual test pin
   remains on `generate-with-group` to exercise the legacy
   backwards-compat dashboard series.

#### Zero new production code paths

The CounterOpts `route` label dimension is unchanged (no schema
migration). The Help text + iteration list + var-block comment are
documentation-layer changes only. **No new `WithLabelValues().Inc()`
call sites** were added — the iteration reads pre-allocated label
values (zero-valued for the canonical `generate` and `sync` labels
at this commit, positive-only for the legacy `generate-with-group`
series during the 90-day Sunset window).

---

## Migration sequence (godlike/07)

| Step       | Status      | Ref                                       | Notes                                |
| ---------- | ----------- | ----------------------------------------- | ------------------------------------ |
| EXPAND     | ✅ done     | Block 4 EXPAND (`fa712eb4`)               | slim handler.go to canonical surface |
| BACKFILL   | ✅ in progress | PR-VO-C1 + Block 6 (this doc)            | Sunset machinery + label refresh    |
| CUTOVER    | ⏳ pending   | PR-VO-D1                                  | observability emit hooks             |
| CONTRACT   | ⏳ pending   | PR-VO-E1                                  | remove `generate-with-group` label   |

---

## Sunset timeline

- **2026-06-28** — Sunset machinery promoted to PR-VO-C1 (live).
- **2026-09-26** — `voiceoverSunsetDate` (RFC 8594 IMF-fixdate
  `Sat, 26 Sep 2026 00:00:00 GMT`). After this deadline the legacy
  `/generate-with-group` label value is CONTRACTED — removed from
  `LegacyVoiceoverDeprecationCount`'s iteration list, removed from
  the CounterOpts Help text.
- **Wave 23** — expected operational migration deadline.

---

## Test pin (unchanged)

`handler_pr_vo_c1_test.go` (5 tests) pins:

- `voiceoverSunsetDate` format (RFC 8594 IMF-fixdate).
- `addVoiceoverDeprecationHeader` response headers (Deprecation +
  Sunset + Link rel=successor-version).
- `legacyVoiceoverRouteInvocationsTotal` counter semantics
  (legacy-dashboard backwards-compat).
- `LegacyVoiceoverDeprecationCount` readback (now multi-label).
- StatusCode preservation contract (helper must not change status).

Drift on any of the 5 tests is a HARD breaking change to the
Sunset machinery.

---

## Forward-pointers (out of Block 6 scope)

- **PR-VO-D1** — sync-service layer to close the Qdrant outbox gap
  when B3's dedupe gate fires (currently bypasses
  `EnqueueIndexEvent`). Owner: voiceover sync service
  (`internal/application/voiceover/sync/`).
- **PR-VO-D2** — Drive HEAD refresh on stale `DriveLink` /
  `DriveFileID`. Owner: voiceover process layer.
- **PR-VO-D3** — full `/generate` handler integration tests
  (httptest with stubbed jobsSvc + destination.resolver).
- **PR-VO-E1** — CONTRACT the `generate-with-group` label value
  post-Sunset (2026-09-26). Removes it from the iteration list and
  the CounterOpts Help text; migrates the test pin to the
  canonical `generate` label.

---

## References

- Prior bundle: [`p1-bundle-B1-C1.md`](p1-bundle-B1-C1.md)
- Block 4 EXPAND slim commit: `fa712eb4` on `origin/main`.
- Canonical architecture: [`../../ARCHITECTURE.md`](../../ARCHITECTURE.md)
- Agent-facing rules: [`../../AGENTS.md`](../../AGENTS.md)
- Zero legacy doctrine: [`../architecture/godlike/07_ZERO_LEGACY_POLICY.md`](../architecture/godlike/07_ZERO_LEGACY_POLICY.md)
- One-owner-per-fact: [`../architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md`](../architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md)
- AGENTS.md Pattern 8 (thin-transport): [`../../AGENTS.md#pattern-8--api-package-thin-transport-only`](../../AGENTS.md)
- AGENTS.md Git-Lesson-2 (direct-to-main): [`../../AGENTS.md#git-lesson-2-june-2026--direct-to-main-workflow`](../../AGENTS.md)
- Deprecation manifest: [`../../architecture/deprecations.yaml`](../../architecture/deprecations.yaml)
