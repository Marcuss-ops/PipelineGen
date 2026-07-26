# Verify-Main Workflow — Canonical Pre-push Gate and 4-tier Family

**Owner**: this doc is the operator-facing canonical reference for the
`make verify-main` pre-push gate and the broader 4-tier `verify-*` gate
family that ships with the repo. Lockstep surfaces: `Makefile`
(canonical target definitions), `README.md` (operator entry point that
cites this doc), and `AGENTS.md` git-workflow (HONOUR-RULE binding).

**Audience**: every contributor pushing to `main`.

> **Single-most-important invariant**: the pre-push gate is
> `make verify-main`. It is **unchanged** by the 4-tier refactor —
> exactly the same recipe CI runs, no diagnostic flags, no
> substitution. If `make verify-main` is RED locally, the push is
> BLOCKED (fail-closed, per AGENTS.md).

---

## The 4-tier verify-* family

Verification is partitioned into **4 tiers** along the use-case axis
(dev loop → pre-push → pre-deploy → post-deploy). Each tier has a
deterministic scope, a fixed cost envelope, and a single canonical
owner (the Makefile). Tiers 1 + 2 fire on every push; tier 3 fires
before deploy; tier 4 requires live credentials (Chrome + scraper + Drive
+ Qdrant + VELOX_ADMIN_TOKEN) and is therefore manual.

| # | Tier     | Target                  | When                                    |
|---|----------|--------------------------|-----------------------------------------|
| 1 | fast     | `make verify-fast`       | dev loop, every commit (seconds)        |
| 2 | **main** | `make verify-main`       | **pre-push** (canonical, fail-closed per AGENTS.md) |
| 3 | release  | `make verify-release`    | pre-deploy, post-merge on `main`         |
| 4 | live     | `make verify-live`       | post-deploy / staging / prod (manual)   |

Locate the tier targets in the Makefile SSOT — do not hardcode line
numbers in this doc (the SSOT may grow):

- `grep -nE '^verify-fast:' Makefile`
- `grep -nE '^verify-main:' Makefile`
- `grep -nE '^verify-release:' Makefile`
- `grep -nE '^verify-live:' Makefile`

For **tier 3 and tier 4** detail (when / cost / per-battery debug
pattern / auth contract / known gaps), see
`docs/operations/verify-release-and-live.md` — that doc complements
this one WITHOUT SSOT duplication (tier 1 + 2 here; tier 3 + 4 there).

---

## Transitive composition of verify-main

`verify-main` is a thin transitivity root for the canonical pre-push
surface. It is the union of four independent batteries that share
nothing except the `scripts/hooks/pre-push` invocation. Locate the
recipe with `grep -nE '^verify-main:' Makefile`.

```
                                  verify-main
                                       │
        ┌──────────────────────────────┼──────────────────────────────┐
        │                              │                              │
   verify-fast                   verify-unit                  verify-node
        │                              │                              │
   foundation + static          core + infrastructure       native probe + npm test
        │                       + api + commands              (better-sqlite3 + Node tests)
        │                       (race-tested;
        │                        EXCLUDES ./tests/...)
        │
        ├── verify-foundation   toolchain (go/node) + secrets +
        │                       format + tidy + hook-syntax lint
        └── verify-static       `go vet ./...` + `go build ./...`

        verify-architecture
            └─ `go run ./cmd/architecture-aggregate --dry-run`
             + `go run ./cmd/archcheck`
             (governance + AST cross-package invariant scan)
```

### Key properties

- **Headless by design**: no Chrome, no scraper session, no Drive, no
  Qdrant. Runs on a CI runner.
- **Fail-closed**: any failing sub-gate exits non-zero. No `|| true`,
  no fallback, no continue-on-error.
- **CI parity**: local must match CI exactly. CI runs the same recipe
  without diagnostic flags.
- **Disjoint from tier 3**: `verify-main` deliberately excludes
  `./tests/...` (those integration suites belong to `verify-integration`
  → `verify-release`).

---

## The 10 verify-artlist-* sub-gates (granular debug pattern)

`make verify-main` does NOT include Artlist. The 10 `verify-artlist-*`
sub-gates are surgical debug entry points intended for iterating on
Artlist lib changes without paying for the full live battery. Locate
them with `grep -nE '^verify-artlist-[a-z]+:' Makefile`.

> The composite `make verify-artlist-live` runs all 9 granular gates
> sequentially via `tests/operational/artlist/run_all.sh` and is the
> post-deploy certificate. The 9 granular are the iteration tools.

| Make target                       | Script (under `tests/operational/artlist/`)  | Surface                                          | Cost          |
|-----------------------------------|-----------------------------------------------|--------------------------------------------------|---------------|
| `make verify-artlist-startup`  | `01_startup.sh`        | server / scraper / Chrome / SQLite / Qdrant / session auth | <30 s      |
| `make verify-artlist-search`   | `02_search_live.sh`    | `/api/artlist/search/live` (60s timeout)        | ~30 s         |
| `make verify-artlist-stream`   | `03_detail_stream.sh`  | `/detail` happy + STREAM_NOT_FOUND              | ~30 s         |
| `make verify-artlist-download` | `04_download.sh`       | `/download` + ffprobe DoD-exact                 | ~60 s         |
| `make verify-artlist-pipeline` | `05_pipeline_fresh.sh` | Gates 4 + 5 + 6 + 7 + 8 fresh-run 3/3          | ~3–5 min      |
| `make verify-artlist-drive`    | `06_drive.sh`          | Drive resolve per clip                          | ~30 s         |
| `make verify-artlist-index`    | `07_index.sh`          | SQLite + Qdrant integrity per clip              | ~30 s         |
| `make verify-artlist-cache`    | `08_cache_replay.sh`   | `cache_hit=true` replay                          | ~60 s         |
| `make verify-artlist-errors`   | `09_failure_modes.sh`  | SESSION_EXPIRED / STREAM_NOT_FOUND / SCRAPER_UNAVAILABLE + restartability | ~60 s |
| `make verify-artlist-live`     | `run_all.sh`           | Composite: all 9 granular gates sequentially (fail-closed chain) | ~10–30 min |

Conventions (binding):
- These 10 targets are **NOT** in the `verify-main` chain (the live
  battery requires Chrome + scraper + Drive + Qdrant — outside tier 2).
- Each granular gate shares helpers from
  `tests/operational/lib/{common,sqlite,artlist,artlist_runtime,velox_domain,drive}.sh`.
  Single canonical owner per fact; no curl/jq duplication across the
  9 sub-scripts.
- `SMOKE_DRY_RUN=1` short-circuits every sub-script: emits `[DRY]`
  banners describing what the battery would probe; no fake server, no
  mocked probe, no test-server sidecar (per AGENTS.md Simplicity &
  Minimalism).

Canonical iteration loop:

```bash
# 1. While iterating (no live-service hits):
SMOKE_DRY_RUN=1 make verify-artlist-stream   # describe; <1 s
SMOKE_DRY_RUN=1 bash tests/operational/artlist/03_detail_stream.sh

# 2. Then run for real (requires live stack + VELOX_ADMIN_TOKEN):
make verify-artlist-stream

# 3. After all 9 granular gates are green:
make verify-artlist-live    # composite certification (post-deploy)
```

---

## Pre-push gate invariant (binding)

```
make verify-main   # the ONLY pre-push gate, runs on every push
                   # = verify-fast + verify-unit + verify-node + verify-architecture
                   # = HEADLESS (no browser, no scraper, no Drive, no Qdrant)
                   # = fail-closed (any non-zero step BLOCKS the push)
                   # = CI parity (CI runs the same recipe, no diagnostic flags)
```

- Wired in by `make install-hooks` (canonical wire via
  `git config core.hooksPath scripts/hooks`).
- Hook file: `scripts/hooks/pre-push` — fail-closed, runs the recipe
  on every push.
- `git push --no-verify` is FORBIDDEN per AGENTS.md HONOUR-RULE,
  except in CI emergencies (and any such bypass MUST be paired with
  a follow-up `fixup!` commit + `git rebase --autosquash`).

---

## The OLD recipe (kept for reference, NOT in the pre-push chain)

The legacy composition `verify-base + verify-go + verify-architecture
+ verify-artlist` is **deprecated** as the pre-push chain per the
July 2026 refactor:

- `verify-go` transitively pulled `verify-go-tests` → the slow
  `./tests/...` surface (now moves to tier 3 via
  `verify-integration`).
- `verify-artlist` is a package-level Artlist diagnostic; the live battery is
  `artlist/run_all.sh` under `verify-artlist-live`
  which requires Chrome + scraper — not headless.

These legacy targets are **STILL PRESENT in the Makefile** for
backward compatibility with operator scripts and external CI that
invoke them directly:

- `make verify-go` — works standalone; not in chain.
- `make verify-base` — works standalone; not in chain.
- `make verify-artlist` — works as a developer diagnostic; not in
  chain.

If a legacy operator script invokes any of these directly, it still
works. The HONOUR-RULE binding of the **pre-push gate** is
`make verify-main` — the canonical verbatim recipe CI runs, with no
substitution.

---

## SSOT cross-references (no duplicates)

This doc intentionally does NOT repeat:

- The 4 `verify-live` batteries' per-battery cost / dependencies /
  auth contract / per-battery debug pattern — see
  `docs/operations/verify-release-and-live.md` (tier 3 + 4 SSOT).
- The per-battery diagnostic landing pages for the live surface —
  same doc, §(c).

**Canonical sources**:

- `Makefile` — locate any verify-* target with:
  - `grep -nE '^verify-(fast|foundation|static|main|release|live|architecture|unit|node|integration):' Makefile`
  - `grep -nE '^verify-artlist-[a-z]+:' Makefile`
  - `grep -nE '^verify-(images|artlist|script|vidrush)-live:' Makefile`
- `scripts/hooks/pre-push` — canonical pre-push hook.
- `docs/operations/verify-release-and-live.md` — tier 3 + 4 SSOT
  (complementary NOT duplicate of this doc).
- `AGENTS.md` — git-workflow + HONOUR-RULE invariant + Authentication
  SSOT (the canonical loader contract).
- `tests/operational/lib/{common,sqlite,artlist,artlist_runtime,velox_domain,drive}.sh`
  — shared lib for the 10 artlist sub-gates (single canonical owner
  per fact).
