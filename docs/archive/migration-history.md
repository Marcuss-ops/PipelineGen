# PipelineGen — Migration History (Archived Waves 1-13)
# -----------------------------------------------------------------------------
# Companion to `architecture/current.yaml` (active waves 0, 14-18).
#
# This file is **read-only narrative**. Operational state lives in
# `architecture/current.yaml`; programming references live in
# `architecture/ownership.yaml` (legacy monolithic, archived June 2026 — replaced by the 6-file split in commit `dc6add3e`; canonical source at `architecture/ownership/` + aggregated view at `architecture/ownership.generated.yaml`). Treat this archive as the audit trail
# behind each `status: done` wave.
#
# Conventions preserved from the original current.yaml:
#   - `status:` was `done` + `verified_zero: true` at archive time.
#   - `blocked_by:` references are kept as Wave IDs only (numeric anchors);
#     these edges are useful for retrospective dependency analyses.
#   - Sub-waves (e.g. 5_PR1/2/3) are flattened to a single section with
#     the resolved commit SHAs noted inline.
#
# To reconstruct the canonical YAML view of any historical wave, consult
# git log on `architecture/current.yaml` (formerly `architecture/current.yaml`)
# or the operator's PR description at the wave's resolved tag.
# -----------------------------------------------------------------------------

## Wave 1 — Ratcheting cleanup guard

- **Status**: done
- **Verified-zero**: true
- **Implemented in**: `scripts/archcheck/main.go`
- **Blocked by**: [0]
- **Exit gate**: `go run ./scripts/archcheck` exits 0 baseline-on-baseline.

scripts/archcheck runs in ratchet mode: accepts current violations only,
forbids new directories, new aliases, new wrappers, new os/exec calls.
Ratchet count is monotone-decreasing per rule.

---

## Wave 2 — Empty directories removal

- **Status**: done
- **Verified-zero**: true
- **Exit gate**: `find . -type d -empty -not -path './.git/*' -not -path './data/*'` returns only `./.blackboxcli`.

All empty legacy directories removed (June 2026):
- `internal/admin` — removed
- `internal/media/repository` — removed (internal/media fully gone)
- `internal/api/sources/{artlist,root,youtube}` — removed (empty shells)

`.blackboxcli` retained as external tool artifact.

---

## Wave 3 — Mega-package infrastructure → `pkg/*`

- **Status**: done
- **Verified-zero**: true
- **Exit gate**: `find internal/infrastructure -maxdepth 1 -name '*.go'` returns empty.

Files at `internal/infrastructure/<root>.go` were extracted into `pkg/<x>`
with zero wrappers. 15 packages landed:

```
pkg/concurrent  pkg/corid       pkg/defaults    pkg/hashutil
pkg/pathutil    pkg/ptrutil     pkg/retry       pkg/similarity
pkg/sliceutil   pkg/sqlutil     pkg/testutil    pkg/textutil
pkg/timeutil    pkg/urlutil     pkg/veloxclient
```

---

## Wave 4A — Asset/Media domain unification

- **Status**: done
- **Verified-zero**: true
- **Cross-reference**: Wave 13 (eliminate internal/media namespace) completes this chain.
- **Exit gate**: `rg 'internal/(assets|artifacts|core/assetop|core/audio|core/destination|core/embedding|core/processor|core/scoring)' --type go | grep -vE ':\s*\d+\s*:\s*//'` returns zero (7 doc-only residuals, listed in current.yaml prior version).

`internal/assets/`, `internal/artifacts/`, `internal/core/{assetop,audio,destination,embedding,processor,scoring}/`
were migrated to `internal/domain/asset/` + `internal/application/assets/` +
`internal/infrastructure/database/sqlite/assets/` + `internal/infrastructure/drive/`.
Residual doc-comment references cleaned in subsequent documentation PRs.

---

## Wave 4B — Job/Worker/Outbox contracts consolidation

- **Status**: done
- **Verified-zero**: true
- **Exit gate**: `rg 'internal/(contracts|jobs/domain_)' --type go` returns zero.

`internal/contracts/` removed. Domain types (`Job`, `WorkerSession`, `Store`,
`Command` variants) live in `internal/domain/job/`.

---

## Wave 4C — Remove `internal/core` + `internal/domain/media`

- **Status**: done
- **Verified-zero**: true
- **Blocked by**: [4A, 4B]
- **Exit gate**: `rg 'internal/core' --type go | grep -vE ':\s*\d+\s*:\s*//'` returns zero (10 doc-only residuals, classified per wave for future cleanup PRs).

`internal/core/` removed as legacy root. Sub-packages migrated:

| From                            | To                                          |
|---------------------------------|---------------------------------------------|
| `internal/core/lifecycle`       | `internal/application/assets/lifecycle/`   |
| `internal/core/maintenance`     | `internal/application/assets/maintenance/` |
| `internal/core/workspace`       | `internal/domain/job/workspace/`           |
| `internal/core/analysis.go`     | `internal/domain/asset/analysis.go` (absorbed) |

`internal/domain/media/` removed (June 2026). Types now declared natively in
`internal/domain/asset/`: `SourceType`, `AssetStatus`, `AssetNode`,
`AssetExecutionResult`, `ClipFolder`, `ClipManifest`, `IndexingCheckpoint`,
`PipelineStrategy`, `NormalizeStrategy`, `ActiveKey`, `MonitoredSource`,
`Subject`, `ImageAsset`, `ImageUsage`, `ImageTag`, `CategoryChannel`,
`SearchQuery`, `SearchQueryResult`.

---

## Wave 5 — Full jobs consolidation

- **Status**: done
- **Verified-zero**: true
- **Sub-waves**:
  - **5_PR1** — Domain & Types Recovery. Resolved commit `91980ef7ac3da563eca96de178efee571df0646c`. Tag `cleanup-wave-05-pr1-land`. Fixed 8 distinct files (e.g. `pkg/platform/platform.go` NEW leaf, `ffmpeg.go` dropped deleted infrastructure import, `worker_registry.go` WorkerNode struct alignment, `api/job.go` rewrite fixing 6 perl-sed syntax errors).
  - **5_PR2** — Application composition wiring (`app/dependencies.go`, `cmd/*`). Resolved commit `c2516fdb`. Tag `cleanup-wave-05-pr2-final-fix`. Four files migrated to `appjobs.*` types: `cmd/worker/main.go`, `internal/infrastructure/jobs/local/broker.go`, `internal/infrastructure/remote/jobbrokerclient/client.go`, `internal/api/workers/handler.go`.
  - **5_PR3** — Remove application-layer type aliases. Resolved at commit `21269fbf` (parent) and `e49c8a43` (script.generate_from_clips sub-fragment). Removes `Store`, `StartJob`, `RequeueResult` zero-copy forwarding aliases from `internal/application/jobs/types.go`. Migrated single known consumer to `domain/job` directly.

- **From**: `internal/jobs`, `internal/outboxhandlers`, `internal/application/jobbroker`, `internal/application/workerassets`
- **To**: `internal/application/jobs`, `internal/infrastructure/jobs/local`, `internal/application/jobs/assets`
- **Exit gate**: `rg 'internal/(jobs/|outboxhandlers|application/jobbroker|application/workerassets)' --type go` returns zero.

---

## Wave 6 — Scripts consolidation

- **Status**: done
- **Verified-zero**: true
- **Exit gate**: `rg 'internal/(scripts|application/scriptflow)' --type go | grep -vE ':\s*\d+\s*:\s*//'` returns zero (1 doc-only residual in `gemmamemory/stub.go`).

`internal/application/scriptflow/` and `internal/scripts/` eliminated.
Flat-merge absorbed all sub-packages into `internal/application/scripts/`
+ `internal/domain/script/` + `internal/infrastructure/database/sqlite/scripts/`.

---

## Wave 7 — Assets + Artifacts + Registry unification

- **Status**: done
- **Verified-zero**: true
- **Blocked by**: [4A]
- **Exit gate**: `rg 'internal/(artifacts|^internal/assets$)' --type go | grep -vE ':\s*\d+\s*:\s*//'` returns zero (2 doc-only residuals in `artifacts/types_test.go`).

`internal/artifacts/` and `internal/assets/` (root) removed. Consumers live
under `internal/application/assets/artifacts/` + `internal/infrastructure/database/sqlite/assets/`.

---

## Wave 8 — Association + Realtime → assets search/sync

- **Status**: done
- **Verified-zero**: true
- **Exit gate**: `rg 'internal/application/(association|realtime)' --type go` returns zero.

`association/` and `realtime/` migrated from `internal/sources/` to
`internal/application/assets/{association,realtime}/`. Zero residual
references to original paths.

---

## Wave 9 — Images + Content + Voiceover (consolidation of `internal/media/` sub-trees)

- **Status**: done
- **Verified-zero**: true
- **Exit gate**: `rg 'internal/media/(fullimages|generation|books|lessons|voiceoversync)' --type go` returns zero.

All target sub-directories of `internal/media/` migrated:

| From                            | To                                                       |
|---------------------------------|----------------------------------------------------------|
| `internal/media/fullimages`     | `internal/application/images/fullimages/`                |
| `internal/media/generation`     | `internal/application/images/`                           |
| `internal/media/books`          | `internal/application/content/books/` (absorbed)         |
| `internal/media/lessons`        | `internal/application/content/lessons/` (absorbed)       |
| `internal/media/voiceoversync`  | `internal/application/voiceover/sync.go`                 |

`internal/media/` non-root migration completed in this wave (full elimination
happens in Wave 13).

---

## Wave 10 — Storage + Drive + Qdrant adapters

- **Status**: done
- **Verified-zero**: true
- **Exit gate**: `rg 'internal/(media/(mediaasset|vectorstore|storage)|upload)' --type go` returns zero.

| From                            | To                                          |
|---------------------------------|---------------------------------------------|
| `internal/media/mediaasset`     | `internal/infrastructure/media/processor`   |
| `internal/upload/drive`         | `internal/infrastructure/drive`             |
| `internal/media/vectorstore`    | `internal/infrastructure/qdrant`            |
| `internal/media/storage`        | `internal/infrastructure/files/storage`     |
| `internal/upload` (root vuoto)  | removed                                     |

Qdrant removed by PG-034 (the canonical replacement is
`internal/infrastructure/qdrant`, exposed via typed ports per Pattern 0).

---

## Wave 11 — Catalog + Intelligence

- **Status**: done
- **Verified-zero**: true
- **Exit gate**: `rg 'internal/media/(assetindex|assettree|clipcatalog|clipindexer|clipresolver|foldermemory|ingest|autotag|classifier|ontology|semantic|catalogsync|monitor)' --type go` returns zero.

Catalog, indexing, enrichment, sync consolidation:

| From                                  | To                                                            |
|---------------------------------------|---------------------------------------------------------------|
| `internal/media/ingest`               | `internal/application/ingest/`                               |
| `internal/media/monitor`              | `internal/application/monitor/`                              |
| `internal/media/clipindexer`          | `internal/application/assets/clipssearch/`                   |
| `internal/media/clipresolver`         | `internal/application/assets/clipresolver/`                  |
| `internal/media/stockpipeline`        | `internal/application/assets/providers/stock/`               |
| `internal/media/catalogsync`          | `internal/application/assets/catalogsync/`                   |
| `internal/media/assettree`            | `internal/application/assets/assettree/`                     |
| `internal/media/deletion.go`          | `internal/application/assets/deletion/`                      |
| `internal/media/foldermemory`         | `internal/infrastructure/files/foldermemory/`                |

---

## Wave 12 — Providers + Sources

- **Status**: done
- **Verified-zero**: true
- **Exit gate**: `rg 'internal/sources' --type go` returns zero.

Provider migration completed (June 2026):
- Registry tipizzato in `internal/application/assets/providers/`
- Artlist `SearchProvider` + `FetchProvider` (`artlistadapter.NewAdapter`)
- YouTube `SearchProvider` + `FetchProvider` (`youtubeadapter.NewAdapter`)
- Stock pipeline integrato
- `internal/sources/` rimosso completamente
- `internal/api/sources/` rimosso (directory vuote eliminate)
- Fallback generale rimosso
- `register-from-youtube` migrato a `FetchProvider`

Note: later infrastructure extraction (yt-dlp, FFmpeg, Node scraper,
downloader) is tracked under Wave 12 as a partially-extracted sub-area
managed via `docs/roadmap/PR1_YOUTUBE_INFRASTRUCTURE.md` +
`docs/roadmap/PR2_ARTLIST_INFRASTRUCTURE.md`.

---

## Wave 13 — Eliminate `internal/media` namespace

- **Status**: done
- **Verified-zero**: true
- **Blocked by**: [4A, 11, 10]
- **Exit gate**: `rg 'internal/media' --type go` returns zero.

`internal/media/` removed completely from the filesystem. Zero Go files
resolve symbols from `internal/media/`. Finalizes the chain of Wave 9
(content), Wave 10 (storage/drive), Wave 11 (catalog/intelligence) by
folding the remaining sub-trees into their canonical owners.

---

## Cross-reference: Operational Roadmap (PR0–PR4)

The `docs/roadmap/PR{0..4}_*.md` files (referenced from `current.yaml` at
archive time) map legacy Wave numbers onto forward-looking operational PRs:

- **PR0** — repo docs/baseline alignment (no code).
- **PR1** — extends Wave 12 + creates `internal/infrastructure/youtube/`.
- **PR2** — extends Wave 12 + creates `internal/infrastructure/artlist/`.
- **PR3** — finishes Wave 14 (api/ compaction).
- **PR4** — finishes Wave 15 residual items (services struct + CoreDeps
  already removed by Wave 15 PR4d-final).

The Operational Roadmap's `Synchronisation rules` are preserved verbatim
in the archived current.yaml snapshot; readers seeking modern truth should
consult `architecture/current.yaml` instead.

---

# Wave 22 PR-5 polish (June 2026) — S1b+S1c reviewer absorption

## S1b — synchronous 10000-record fallback removal

The pre-S1a pattern layered a `repo.ListClipsPaged(..., 10000, 0, ...)`
inline paginated scan into the HTTP handler's request goroutine. This
violated AGENTS.md Pattern 8 ("api layer is thin transport only, no
business orchestration") because the handler thread ended up running
the orphan-scan + Drive-files.Get + DELETE coordination synchronously.

**Fix**: Cleanup and Reconcile now ALWAYS enqueue a `job.TypeSystemCleanup`
job through `JobsServicePort` (canonical service side) and
`jobservice.Enqueue(...)` (api side). The worker registered at
`internal/application/assets/maintenance/` does the actual orphan scan,
Drive-files.Get, and physical delete from the broker pool. The
10000-record synchronous path is REMOVED entirely.

## Wave 22 PR-5 polish (June 2026) — what this PR added

| Reviewer finding                               | Edit location                                                            |
|------------------------------------------------|--------------------------------------------------------------------------|
| (a) Canonicalise `"system.cleanup"` -> `job.TypeSystemCleanup` | `internal/application/clips/clip_ops.go` + `internal/api/assets/clips/clip_ops.go` (constant already defined in `internal/domain/job/job.go:69`) |
| (b) Wire `s.Cleanup` via `JobsServicePort`     | Already wired (no code change). Port stays nil-tolerated for tests. **Note (June 2026, archaeology)**: pre-PR, `clip_ops.go::Cleanup` already routed through `s.jobs.Enqueue(...)` whenever `s.jobs != nil` — the only Wave 22 PR-5 delta was adding the explicit nil-guard + 503 mapping (Edit (a)). Future maintainers: search `s.jobs.Enqueue` in `internal/application/clips/` to confirm wiring is still structurally-satisfied before assuming re-work. |
| (c) Typed sentinel `clips.ErrJobsUnavailable`  | `internal/application/clips/clip_ops.go` (sentinels block)               |
| (d) Issue-slug blocking/informational split    | `VerifyReport.Issues` godoc clarifies BLOCKING-only contract             |
| (e) HashRecovered legacy godoc rewrite         | `VerifyReport.HashRecovered` godoc expanded (always-false on verify)     |
| (f) Regression-test `_test.go` per read-only invariant | `internal/application/clips/clip_ops_test.go::TestVerify_HashInfoSeparateFromIssues/read_only_no_clip_mutation` |
| (g) Drop `_ = time.Now()` hack                 | `internal/application/clips/clip_ops.go` + `internal/api/assets/clips/clip_ops.go` (+ remove `"time"` import) |
| (h) Archive 10000 docstring                     | This section (this file) + the inline comment shortens to a pointer      |

Plus puny cleanups that fell out of the polish pass:

- Removed the giant verbose S1b narrative from the inline `Cleanup`
  comment blocks (both api-side and service-side); replaced with a
  4-line summary that points here for the historical detail.
- Api-side verifyClip drive-check section gained a 3-line note
  explaining the Wave 22 PR-5 polish: the BLOCKING vs INFORMATIONAL
  contract on `result["issues"]` (mirrors the typed `VerifyReport.Issues`
  godoc on the service side).
- `cleanup requires jobs service` HTTP 503 message body on the api
  side now mirrors `clips.ErrJobsUnavailable` verbatim so dashboards
  grep one stable string across both layers.
