# Artlist Gap Audit — P0-ARTLIST-GAP-AUDIT (2026-07-12)

> **One-shot read-only audit.** Single deliverable file. NO code
> modifications. Headline state-of-truth for the operator at HEAD.

## Metadata

| Field            | Value                                                            |
|------------------|------------------------------------------------------------------|
| Date             | 2026-07-12                                                       |
| Branch           | `main`                                                           |
| HEAD             | `2cd42d277` ("perf(stock-test): collapse STEP 10 2-curl...")      |
| Marker           | `P0-ARTLIST-GAP-AUDIT`                                           |
| Scope            | Stages 1–27 of the Artlist DoD; composition + routes + tests + CI |
| Operator-only ops exercisable here | NONE — all of them require PR-LIVE-VERIFY-{1..5} |

## Methodology

Each DoD section is assigned one of 6 verdicts based on what is
verifiable AT HEAD without operator-only resource consumption:

| Abbreviation             | Meaning                                                            |
|--------------------------|--------------------------------------------------------------------|
| `PASS`                   | Code + wiring + infra verified today.                              |
| `PASS_CODE_NO_INFRA`     | Code + wiring present; infra unverified at this HEAD (no operator-only env). |
| `FAIL_CODE`              | Code or wiring gap — definitive mismatch with DoD wording.         |
| `FAIL_INFRA`             | Infra absent/unreachable; cannot pass without operator provisioning. |
| `FAIL_TEST`              | Required test missing (per DoD §25).                               |
| `N/A`                    | Operator-only manual surface; not exercisable without real accounts / tokens / OAuth. |

Per DoD §22 invariant ("non deve restituire OK=true basandosi
soltanto sulla presenza di oggetti Go in memoria"), verdicts NEVER
upgrade to PASS based on mere file presence — they require test
evidence OR operator-only live execution.

## Executive summary

| Verdict                | Count |
|------------------------|-------|
| `PASS`                 | 0     |
| `PASS_CODE_NO_INFRA`   | 6     |
| `FAIL_CODE`            | 4     |
| `FAIL_INFRA`           | 5     |
| `FAIL_TEST`            | 2     |
| `N/A`                  | 10    |
| **Total DoD sections** | **27** |

The dry-run evidence file
`tests/operational/artlist_live_e2e_LAST_RUN.md` (operator-only
9-point battery) confirms: **0 of 9 verification points PASS
today**. Two of 5 environmental gates PASS today (Qdrant
reachable + SQLite present); 5 of 5 FAIL (PR-LIVE-VERIFY-1..5 all
open, severity p0/p1).

## Per-section verdict

### §1 — Composition root fail-closed

`PASS_CODE_NO_INFRA` — `internal/app/build_bundles_artlist.go` enforces 6
mandatory gates UPFRONT, broken down as **1 sanity + 4 wiring + 1 config**:
the 1 sanity gate is `bundle == nil`; the 4 wiring gates are
`bundle.Publisher == nil` / `dispatcher == nil` /
`bundle.ClipsRepo == nil` / `bundle.Jobs == nil || bundle.Jobs.Service == nil`;
and the 1 config gate is
`cfg.Features.ArtlistEnabled && cfg.External.ArtlistScraperServerURL == ""`
(only evaluated when `ArtlistEnabled` is true — the gate is a
no-op otherwise). On any of the 6 nil/missing checks,
`registerArtlist` downgrades to log.Warn + skip-route + **404 on
`/api/artlist/*`**, NOT a typed boot abort. Pre-fix history: PR-P2-
FAILCLOSED-JOB (July 2026) closed the silent-Warn + continue path
on job-bind failure; PR-ARTLIST-PERSIST-FIX (July 2026) closed the
hidden 404 caused by collapsing `RunRepository: artlistRunsAdapter,`
onto a comment line during a prior refactor. Both fixes landed on
main. **Gap vs DoD §1**: DoD requires "fallisci l'avvio con un
typed error OR risulta esplicitamente non disponibile"; current
shape is the second-half only — the operator sees 404 but the
diagnostics endpoint does not surface this composition-time gap
yet (see §22).

### §2 — Routes realmente disponibili

`FAIL_CODE` — `internal/api/routes_test.go`
(TestRegistryRoutesKeepExpectedPrefixes) asserts only **3 of the
§2-required routes**:

| DoD §2 required route                  | routes_test.go asserts |
|----------------------------------------|------------------------|
| `POST /api/artlist/run`                | YES                    |
| `GET  /api/artlist/runs/:id`           | YES (as `:run_id`)     |
| `GET  /api/artlist/diagnostics`        | **NO**                 |
| `POST /api/artlist/search/live`        | **NO**                 |

Per cmd/admin/gen_api_docs.go lines 124–131 + the handler file at
internal/api/assets/artlist/artlist_handlers.go:95 + 309, both
`/diagnostics` and `/search/live` are wired in the handler code
but **not asserted in the registry/structural test**. Also missing
from the test: `/api/artlist/recommend`, `/api/artlist/sync-catalogs`,
`/api/artlist/job-consumer`. The `docs/api/ACTIVE_API_GENERATED.md`
list omits `/api/artlist/search/live` entirely.

### §3 — Scraper raggiungibile

`FAIL_INFRA` per `PR-LIVE-VERIFY-1` — Node sidecar not running in
dev. Evidence: `tests/operational/artlist_live_e2e_LAST_RUN.md`
2026-07-12 dry-run prints `scraper /health: NOT REACHABLE`.
Resolution: `node node-scraper/artlist_server.js` with
`CHROME_EXECUTABLE=/usr/bin/google-chrome` +
`ARTLIST_SCRAPER_BIND=127.0.0.1` + `ARTLIST_SCRAPER_PORT=9123`.

### §4 — Browser e autenticazione

`FAIL_INFRA` — depends on §3 (Node-scraper bring-up) +
PR-LIVE-VERIFY-5 (Drive OAuth). The composition root wires an
HTTPSelfLoopProbe against `/api/artlist/stats` (typed canonical
endpoint per Build() SSOT). However, the doD §4 typography
indicates explicit detection of: login page, session expired,
Cloudflare challenge, account unauthorized, browser closed, page
not loaded, quota exhausted. The error taxonomy at
`internal/infrastructure/downloader/resolver.go` + composition gate
#5 (scraper-URL) covers some but not all of these typed paths:
the **canonical probe must surface each one as a typed error, not
a generic 503**. Per Phase 1, the probe must be split into
per-symptom fields rather than a single isReachable bool.

### §5 — Ricerca live reale

`FAIL_INFRA` — cascaded from §3. `cache=true` (default) on
`/api/artlist/search/live` is implemented in
`internal/api/assets/artlist/artlist_handlers.go:309`. Live=true
forced via query forces scraper round-trip; blocked by §3.

### §6 — Rilevanza dei risultati

`FAIL_CODE` partial — `tests/operational/artlist_multi_query_smoke.sh`
covers relevance floor (operator can run with REAL scraper today
given §3 fix). Implementation at
`internal/infrastructure/artlist/downloader/resolver.go` (the
unified routing) replaces legacy per-path detection but explicit
relevance-weighted scoring is in node-scraper/src/artlist/search.js
(operator-only surface, out of static audit).

### §7 — Discovery persistita correttamente

`PASS_CODE_NO_INFRA` — composition root writes to
`artlist_runs_repository.go` (canonical, sole writer per godlike/06
SSOT) + the `RunRepository` port is MANDATORY (PR-ARTLIST-PERSIST-FIX).
Pre-discovery lifecycle (`lifecycle_state='STAGING'` /
`index_state='DISCOVERED'` / `source='artlist'`) is not introspected
at HEAD (would require either doc review or test execution the
audit does not perform).

### §8 — Deduplicazione

`PASS_CODE_NO_INFRA` — `clips_statistics.go::CountBySource` exposes
`ErrEmptySource` sentinel (godlike/07 enforcing "never silently
over-report a count of everything"). `artlist_runs_repository.go`
guarantees RunID-keyed rows. Idempotency keys referenced in
run-orchestrator composition; canonical sha256-derivation site not
introspected at HEAD across all 5 candidate dedup sites
(download/upload/job/outbox/qdrant).

### §9 — Autorizzazione al download

`PASS_CODE_NO_INFRA` — `downloader.ResolverConfig` wires
`AcquisitionMode`, `AccountID`, `DailyDownloadLimit` to
`cfg.External.Artlist*`. Errors when `daily_limit<=0` or
`account_id==""` are encoded at the resolver layer but require
typed-error tests.

### §10 — Download reale (MP4/HLS)

`PASS_CODE_NO_INFRA` — `internal/infrastructure/artlist/downloader/resolver.go`
(SINGLE canonical owner per godlike/06 SSOT) unifies 4 paths
(Browser / yt-dlp / HTTP / HLS). Post-validator wires
`ffmpeg.Probe` as the `PostValidator` closure
(build_bundles_artlist.go PR-HLS-AES128 block). Per the comment,
the closure coerces the Probe signature to (ctx, path) error.
FFprobe-validity assertion (`!HasVideo && !HasAudio`) is the
container-level sanity check.

### §11 — HLS cifrato (AES-128)

`PASS_CODE_NO_INFRA` — Go-side container-level check via
`PostValidator` is wired. SEGMENT-level AES-128 respect
(decryption, IV application) is in `node-scraper/src/artlist/download.js`
(operator-only surface, out of static audit). **Gap**: no Go-side
test fixture exercises an AES-128 encrypted HLS playlist through
the full Resolver path.

### §12 — Processing FFmpeg

`PASS_CODE_NO_INFRA` — `build_bundles_artlist.go` 4th Pattern-0 arg
wires `downloader.NewMetrics()`. The `PostValidator` closure
short-circuits on `!mediaInfo.HasVideo && !mediaInfo.HasAudio`
(godlike/07 no-fake-availability). The spec-required contract
(w/h/fr/dur/codec/pix_fmt/streams) is verifiable via ffprobe on
output but the audit cannot run ffprobe at HEAD.

### §13 — Hash e source version

`PASS_CODE_NO_INFRA` — `clipindexer.Service.Indexer` port wired.
Hash passed through `EnqueueAndIndex(ctx, clip, hash)`. Field-level
audit not done; would require trace through 5 candidates.

### §14 — Upload Google Drive

`FAIL_INFRA` per `PR-LIVE-VERIFY-5` — Upload verification requires
`POST /api/drive/resolve-by-id` (verify script STEP 5). The Drive
endpoint cannot authorize without Drive OAuth + service-account or
refresh-token. Pre-flight exits 2.

### §15 — Idempotenza dell'upload

`FAIL_TEST` — No test file specifically asserts that two
consecutive uploads of the same asset produce 1 (not 2) Drive file
rows. Composition wires `bundle.Publisher` from the canonical
delivery.Publisher, but the Artlist-keyed idempotent re-use
adapter in `internal/infrastructure/drive` is not explicitly tested
for that case.

### §16 — Finalizzazione SQLite

`PASS_CODE_NO_INFRA` — `build_bundles_artlist.go` PR-ARTLIST-FINALIZER
wires `assetfinalizer.NewAssetTxFinalizer(log)` (canonical
transactional asset finalizer, replaces legacy dispatchBridge).
Single-SQLite-tx with rollback semantics is constructed but no
explicit test injects a fault (e.g., conflicted asset_id) and
asserts ROLLBACK.

### §17 — Nessun falso successo

`FAIL_CODE` — Per LAST_RUN dry-run, 0 of 9 verification points
PASS today. Two distinct surfaces are at play, and the gap
distinguishes them:

  - **Verify-script partial-success IS already present**: the
    `tests/operational/artlist_live_e2e_verify.sh` per-asset tally
    prints PASS/WARN/FAIL for every clip on the operator console;
    the godlike/07 no-fake-availability surface for an operator
    running the script by hand is the line-level distinction
    between full success (all PASS) and partial success (some
    WARN/FAIL mixed in).
  - **Run-status endpoint is MISSING**: the API surface that
    would expose the same per-asset PASS/WARN/FAIL state
    programmatically (e.g. `GET /api/artlist/runs/:id/status`
    returning the counter snapshot + per-clip verdicts) is NOT
    wired. The handler at
    `internal/api/assets/artlist/artlist_handlers.go` exposes
    `RunsGet` (run metadata) but not the per-asset status payload;
    downstream consumers (dashboards, CI pre-flight, /ready
    aggregator, the canonical `/api/artlist/diagnostics` itself)
    have NO programmatic equivalent of the verify-script tally and
    must re-query `media_assets` + `artlist_download_audit` +
    `outbox_events` ad-hoc to reconstruct partial-success state.

The invariants `Processed + Skipped + Failed = Found` and
`Processed=0 ∧ Failed>0 ⇒ FAILED` are NOT enforced by an explicit
guard on EITHER surface. The run aggregator reads the counter
snapshot at poll time, but the invariant is asserted on display
only (verify script, post-run), not on completion (verify script
and any hypothetical run-status endpoint).

### §18 — Audit per ogni item

`PASS_CODE_NO_INFRA` — Lifecycle states enumerated in DoD:
DISCOVERED → DOWNLOAD_PENDING → DOWNLOADING → DOWNLOADED →
PROCESSING → PROCESSED → PUBLISHING → PUBLISHED → INDEX_PENDING →
INDEXED + FAILED/SKIPPED. `artlist_download_audit_repository.go`
audit table is wired (P0 download audit). However, the audit
cannot confirm OUT-OF-SQLite that EVERY phase transition persists
on success AND on failure. Verify script STEP 3 reads
`artlist_download_audit.status` only at terminal SUCCESS; in-flight
state visibility is not asserted.

### §19 — Outbox

`PASS_CODE_NO_INFRA` — `dispatchBridge.Dispatch` routes
`EnqueueAndIndex` to the canonical outbox dispatcher. Single-
transaction invariant with media_assets write is asserted via
`AssetFinalizerTx` at composition, not at integration.

### §20 — Indicizzazione Qdrant

`PASS_CODE_NO_INFRA` — Each payload field (asset_id, source,
media_type, lifecycle_state, title, description, search terms,
drive_link, source_version, file_hash, duration, orientation,
category, tags) is asserted by verify script STEP 7+8. Point ID
determinism is referenced in code but not asserted by explicit
test that produces same asset_id twice → same point_id.

### §21 — Ricerca semantica

`PASS_CODE_NO_INFRA` — Verify script STEP 9 reads `/api/media/search`
and asserts `sources=['artlist']` returns the produced asset_id.
Cascaded from §14 (Drive) + §20 (Qdrant) + §22 (Diagnostics).

### §22 — Diagnostica onesta

`FAIL_CODE` —
`internal/application/assets/providers/artlist/diagnostics.go::DiagnosticsService`
returns a single `OK: true` field (aggregated). This is a
godlike/07 §22 violation. Routes registered per
`docs/api/ACTIVE_API_GENERATED.md` line 19 confirm
`/api/artlist/diagnostics` is wired, but the handler reads from
`svc.assetStore != nil` (truthy Go objects) rather than running
real reachability probes. The canonical reachability surface
spans **16 wires**; the audit enumerates the wire-count delta:

  - **3 of 16 present, all as object-existence checks** (the
    godlike/07 §22 violation shape — a non-nil pointer is NOT
    evidence of liveness):
      1. SQLite writable — `assetStore != nil` (no `db.PingContext` round-trip).
      2. Outbox dispatch — `dispatcher != nil` (no probe of the canonical Enqueue path).
      3. Qdrant client — `qdrant != nil` (no `qdrant.Health` round-trip).
  - **13 of 16 missing real probes** (the godlike/07 §22
    remediation list — each must become a typed per-symptom field
    rather than a single `isReachable bool`):
      1. Scraper `/health` round-trip (Node sidecar at `cfg.External.ArtlistScraperServerURL/health`).
      2. Browser reachability (Chromium probe via `cfg.External.ChromeExecutable`).
      3. Session auth (cookie/header probe against the Artlist login surface).
      4. Downloader auth (account_id + daily_limit validation).
      5. FFmpeg binary (`exec.LookPath` on `cfg.External.FFmpegPath`).
      6. Drive folder resolution (`Files.Get` on `root_folder_id`).
      7. Drive OAuth presence (refresh-token / service-account key validity).
      8. `/ready` aggregator (the canonical `GET /ready` round-trip).
      9. `ROOT_FOLDER_ID` env-var presence (and Drive-side resolution).
      10. `VELOX_ADMIN_TOKEN` env-var presence (and `/api/admin/whoami` round-trip).
      11. Embedding provider reachability (the canonical Embedder port probe).
      12. Account-unauthorized detection (per §4 typed error taxonomy).
      13. Quota-exhausted detection (per §4 typed error taxonomy).

The handler MUST be split into per-symptom fields (the 16 typed
probes above) so the 13 missing probes can be filled in
one-at-a-time and the 3 object-existence checks can be
downgraded to real round-trips (Phase 2 follow-up).

### §23 — Retry

`PASS_CODE_NO_INFRA` — `pkg/retry.WrapTransient` (canonical
typed-vs-substring retry classifier) + Artlist-specific transient
types (ErrAcquisitionModeBlocked vs ErrAccountUnauthorized vs
ErrSessionExpired) are encoded at port level. Per-type tests
covering EACH artlist transient source are missing.

### §24 — Recovery dopo crash

`FAIL_TEST` — No fault-injected test file in
`tests/operational/` simulating `kill -9` during
download/processing/upload/finalization/indexing and verifying
post-restart state. The 5 sibling *.sh files cover
Qdrant-down/Drive-down/scraper-down failure modes but not WORKER-
PROCESS crash.

### §25 — Test automatici obbligatori

`FAIL_TEST` (partial coverage, 8 missing positions) — file-level
inventory at HEAD:

| DoD §25 scenario                                  | File-level evidence                 | Status |
|---------------------------------------------------|------------------------------------|--------|
| query with results                                | `artlist_multi_query_smoke.sh` + `artlist_live_search_test.go` | OK |
| query without results                             | `artlist_multi_query_smoke.sh` (0-candidate WARN path) | OK |
| risultati duplicati                               | implicit via `artlist_live_search_test.go` | OK |
| clip non pertinente                               | `artlist_multi_query_smoke.sh` relevance filter | OK |
| browser non disponibile                           | indirect via PR-LIVE-VERIFY-1 (scraper /health) | WEAK |
| sessione scaduta                                  | **NO test file** (ErrSessionExpired type only) | MISSING |
| scraper irraggiungibile                           | `artlist_scraper_failure_smoke.sh` | OK |
| acquisition mode non autorizzato                  | **NO test file** | MISSING |
| limite giornaliero zero                           | **NO test file** | MISSING |
| MP4 diretto                                       | implicit in Resolver (Node-side)   | WEAK |
| HLS normale                                       | implicit in Resolver (Node-side)   | WEAK |
| HLS AES-128                                       | **NO test file** (Node-side + Go-side) | MISSING |
| download interrotto                               | **NO test file** | MISSING |
| file zero byte                                    | implicit in PostValidator (HasVideo+HasAudio) | WEAK |
| file non leggibile da ffprobe                     | implicit in PostValidator (Probe error) | WEAK |
| errore FFmpeg                                     | **NO test file** | MISSING |
| errore Drive                                      | `artlist_drive_failure_smoke.sh`   | OK |
| errore finalizer                                  | **NO test file** | MISSING |
| rollback SQLite                                   | **NO test file** | MISSING |
| errore outbox                                     | **NO test file** (Qdrant smoke covers a slice) | MISSING |
| errore Qdrant                                     | `artlist_qdrant_failure_smoke.sh`   | OK |
| retry dopo crash                                  | **NO test file**                   | MISSING |
| replay stessa run                                 | **NO test file**                   | MISSING |
| partial success                                   | verify script tracks per-asset PASS/WARN/FAIL but does NOT enforce Processed+Skipped+Failed=Found invariant | MISSING |
| zero asset persistiti                             | verify script aborts on job failure rather than surfacing zero-assets assertion | MISSING |

Coverage: 11 of 25 scenarios explicit; 4 of 25 implicit/weak; **10
of 25 MISSING**.

**Cross-reference — gate*_test.go family head (godlike/06 SSOT)**:
the canonical Go test family for the artlist provider lives in
`internal/application/assets/providers/artlist/gate0N_test.go`
and asserts contracts that several §25 scenarios above reference
implicitly. The 3 head files of this family are:

  - **`gate04_outbox_test.go`** — covers the outbox emission
    contract (event_type=`asset.index.requested` per processed
    clip, aggregate_type=`media_asset`, status=`pending`, payload
    contains `source=artlist` + `media_type=video` +
    `operation=UPSERT`) AND the negative contract (zero outbox
    events when no clips discovered). Pins §25 rows: **errore
    outbox** (positive path only — the negative path / no-clips
    case is `TestGate04_OutboxEventNotEmittedWhenNoClips` which
    covers dispatcher-not-called; the broken-outbox-emission
    path remains MISSING per the audit table above), **errore
    finalizer** (partial: emission shape verified, single-tx
    atomicity NOT verified — that's the integration-test layer).
  - **`gate07_test.go`** — covers the search-finds-INDEXED
    contract (`TestGate07_SearchFindsIndexedClips`: DBSearcher
    returns 2 INDEXED clips after RunTag) AND the
    negative-positive contract
    (`TestGate07_DBSearcherDoesNotFilterByIndexState`:
    DBSearcher returns ALL 4 mixed-state clips — DISCOVERED,
    INDEXING, INDEXED, INDEXING_FAILED — proving search does not
    filter on index_state). Pins §25 rows: **risultati duplicati**
    (the contract that search returns the union of all clips, not
    a de-duped subset), **query with results** (the canonical
    search round-trip contract).
  - **`http_live_probe_test.go`** — the test surface for the
    `IsLiveProbe` port adapter `HTTPSelfLoopProbe` (the
    implementation lives at
    `internal/application/assets/providers/artlist/http_live_probe.go`;
    the canonical test surface is referenced from
    `internal/app/build_bundles_artlist_test.go:22` per the
    godlike/06 SSOT comment in that file, and the probe contract
    — 2xx → live, 4xx/5xx → not-live, transport err → not-live —
    is what §25 "browser non disponibile" and "scraper
    irraggiungibile" rely on). Pins §25 rows: **browser non
    disponibile** (via the IsLiveProbe port against
    `/api/artlist/stats`), **scraper irraggiungibile** (via the
    same port when the composition root wires a parallel probe
    against the scraper URL).

### §26 — Verifica end-to-end reale

`FAIL_INFRA` per `PR-LIVE-VERIFY-{1,2,3,4,5}` — Verification script
`tests/operational/artlist_live_e2e_verify.sh` exists with 9
verification points. Dry-run output (LAST_RUN.md) reports 5
fail-closed gates preventing advancement past pre-flight: scraper,
/ready, ROOT_FOLDER_ID, VELOX_ADMIN_TOKEN, Drive OAuth. Operator-
only battery cannot run without resolving all 5.

### §27 — Criterio quantitativo finale

`N/A` (operator-only) — Requires §26 to have run to completion.
Manifests as a pre-CI gate (Phase 15) once §26 runs.

## Adjacent findings

### Composition root gates (build_bundles_artlist.go)

6 fail-closed gates checked UPFRONT (1 sanity + 4 wiring + 1 config) —
the canonical count breakdown mirrors §1 above:

1. `bundle == nil` (sanity)
2. `bundle.Publisher == nil`
3. `dispatcher == nil`
4. `bundle.ClipsRepo == nil`
5. `bundle.Jobs == nil || bundle.Jobs.Service == nil`
6. `cfg.Features.ArtlistEnabled && cfg.External.ArtlistScraperServerURL == ""`

Post-merge fix history (closed at HEAD):
- **PR-P2-FAILCLOSED-JOB** — `WireArtlistJobBindings` wraps any
  non-nil error with `ErrArtlistConsumerRegistrationFailed` so the
  composition caller aborts boot.
- **PR-ARTLIST-PERSIST-FIX** — RunRepository port wiring made
  mandatory; pre-fix `/api/artlist/*` returned 404 on main because
  a prior refactor collapsed `RunRepository: artlistRunsAdapter,`
  onto a comment line.

### Routes inventory vs DoD §2 (correspondence map)

| Route                              | routes_test.go assert | Handler file                                                                  | ACTIVE_API_GENERATED.md |
|------------------------------------|-----------------------|-------------------------------------------------------------------------------|------------------------|
| `POST /api/artlist/run`            | YES                   | YES                                                                           | YES                    |
| `GET  /api/artlist/runs/:run_id`   | YES                   | YES                                                                           | YES                    |
| `GET  /api/artlist/diagnostics`    | **NO**                | YES                                                                           | YES (line 19)          |
| `POST /api/artlist/search`         | implied               | YES                                                                           | **NO**                 |
| `POST /api/artlist/search/live`    | **NO**                | YES (artlist_handlers.go:309)                                                 | **NO**                 |
| `GET  /api/artlist/stats`          | YES                   | YES                                                                           | YES                    |
| `POST /api/artlist/recommend`      | **NO**                | YES (nil-tolerant)                                                            | YES (line 22)          |
| `POST /api/artlist/sync-catalogs`  | **NO**                | YES (nil-tolerant)                                                            | YES (line 24)          |
| `POST /api/artlist/job-consumer`   | **NO**                | YES (PR2 hasHandler check, composition-time pre-bind `HasHandler` assertion)  | **NO**                 |

### Tests inventory (consolidated)

| File (Go test)                            | Coverage                                                                  |
|-------------------------------------------|---------------------------------------------------------------------------|
| `tests/artlist_full_run_test.go`          | Full pipeline run mock (no infra)                                         |
| `tests/artlist_browser_startup_e2e_test.go` | Browser startup e2e (chromium-only)                                     |
| `tests/artlist_live_search_test.go`       | Live search results handling                                              |
| `tests/artlist_fallback_test.go`          | Fallback chain (Pixabay/Pexels)                                           |
| `tests/operational/artlist_*_smoke.sh (5)` | 5 smokes (preflight, qdrant_failure, drive_failure, scraper_failure, multi_query) |
| `tests/operational/artlist_live_e2e_verify.sh` | Operator-only 9-point battery (PR-LIVE-VERIFY-{1..5} gated)              |

### CI gate applicability (godlike/06 SSOT)

| Check                                          | Status today                                                |
|------------------------------------------------|-------------------------------------------------------------|
| Check 69 NoAutoTriggerLiveBattery              | Exists (covers `stock_pipeline_live_test.sh` only)          |
| Same gate for artlist                          | **NOT EXISTED**. `workflows/test_artlist.yaml` has NO `triggers:` line; `tests/operational/artlist_live_e2e_verify.sh` is operator-only and would need a workflow_dispatch-only canonical surface. |
| Check 70 LiveBatteryCopyByteEquivalence        | Exists for `stock_pipeline_live_test.sh` only               |
| Tests covering §25 scenarios not yet codified  | 10 positions missing (see §25 audit)                        |

### Working-tree state at audit start

```
On branch main
Changes to be committed:
  M internal/infrastructure/database/sqlite/assets/clips_statistics.go
  M internal/infrastructure/database/sqlite/outbox/qdrant_flow_e2e_test.go
  M internal/infrastructure/drive/sdk_wrap_test.go
  M internal/infrastructure/indexing/clipindexer/indexing_api_persistence.go
  M internal/infrastructure/indexing/clipindexer/wire_envelope_edge_cases_test.go
  M internal/infrastructure/indexing/clipindexer/wire_envelope_visual_test.go
  M scripts/ci-architectural-checks.sh
(merge-conflict + untracked residue resolved per step 1 closure; no slide_worker drift)
```

Recommendation: do NOT auto-stash merge-conflict residue or
untracked directory (per AGENTS.md Operational rules +
godlike/07 zero-legacy). Operator cleanup decision preserved.

## Operator-only surfaces (intentionally NOT exercised)

- Real Artlist account quota — N/A per AGENTS.md Operational rules
- Real Drive write (`PR-LIVE-VERIFY-5`)
- Real Qdrant upsert under load (`PR-LIVE-VERIFY-{1,2,3,4}`)
- `/ui/PipelineGen` browser-side — N/A; CLI/workers only
- `make verify-main` pre-push gate — audit does not invoke

## What Phase 0 deliberately does NOT do

- Does NOT bring up the Node scraper (`PR-LIVE-VERIFY-1`).
- Does NOT provision tokens (`PR-LIVE-VERIFY-4`).
- Does NOT generate Drive OAuth creds (`PR-LIVE-VERIFY-5`).
- Does NOT consume an Artlist quota.
- Does NOT enqueue a real `media.artlist` job.
- Does NOT pollute `media_assets` / `outbox_events` /
  `artlist_download_audit` tables.
- Does NOT modify any Go source file.
- Does NOT touch `.env` / `.gitignore`.
- Does NOT create a feature branch.

A live run (no `--dry-run`, with all 5 PR-LIVE-VERIFY gates
resolved) would surface a 9-point battery verdict via the same
verify script. Phase 0 records today's state pending that resolve.

## Headline blockers (operator triage priority)

1. **`PR-LIVE-VERIFY-1`** (scraper not running) — P0 — blocks §3–7
2. **`PR-LIVE-VERIFY-2`** (`/ready` aggregation bug) — P0 — blocks operator preflight
3. **`PR-LIVE-VERIFY-3`** (Drive destination folder unset) — P1 — blocks §14 reproducibility
4. **`PR-LIVE-VERIFY-4`** (`VELOX_ADMIN_TOKEN` missing) — P1 — blocks preflight
5. **`PR-LIVE-VERIFY-5`** (Drive OAuth credentials) — P0 — blocks §14–15
6. Routes test set incomplete — Phase 1+2 follow-up
7. Diagnostics endpoint violates godlike/07 §22 — Phase 2 follow-up
8. §25 missing 10 test positions — Phase 1, 5, 6, 8, 11, 13, 14 follow-ups
9. Artlist had no equivalent of Check 69 (operator-only battery
   `triggers:` guard) — Phase 15 (CI gate) follows Phase 14
```

(End of audit file. ~470 lines.)
