# Verify-Release and Verify-Live Workflow

**Owner**: this doc is the operator-facing canonical reference for `make verify-release`
(tier-3 pre-deploy gate) and `make verify-live` (tier-4 post-deploy gate).

**Lockstep surface**: complements `docs/operations/verify-main-workflow.md`
(tier 1 + 2 — dev loop + pre-push headless). This doc covers **tier 3 + tier 4
only**; do not duplicate the per-area Make-target reference or the
recommended dev-loop workflow that already lives in
`docs/operations/verify-main-workflow.md`.

**Audience**: every operator running pre-deploy certification or
post-deploy validation.

---

## (a) When to run `make verify-release`

`make verify-release` is the **pre-deploy gate (tier 3)**.

### Composition (per Makefile)

```
verify-release  =  verify-main  +  verify-integration
                 =  (verify-fast + verify-unit + verify-node + verify-architecture)
                 +  verify-integration   (= verify-go-tests = `go test -race ./tests/...`)
```

Verify the live position with `grep -nE '^verify-release:' Makefile` —
do not hardcode line numbers; the Makefile is the SSOT.

### When to run

- **After every merge commit lands on `main`** (post-merge verification of
  the integration surface).
- **Before triggering the deploy job** (final pre-deploy gate, last gate
  before the live tier begins).
- **On a fresh clone or after `git pull origin main`** — catches any drift
  between the pushed state and the deployed branch.

### What it costs

A few minutes for the inherited `verify-main` chain (headless tier 2),
plus several minutes more for `verify-integration` (Go tests under
`./tests/...` — some suites exercise cross-package integration surfaces
and may depend on Drive / Qdrant / scraper fixtures). **Total budget:
~5–15 min** depending on the `./tests/...` size at HEAD. These are
**approximate budgets** — measure on the actual operational host before
relying on these for scheduling.

### What to do if RED (fail-closed)

Per AGENTS.md fail-closed + "Never represent absence as success":

- **DO NOT proceed to deploy**. Tier 4 (`verify-live`) cannot compensate
  for a broken tier 3.
- Identify the failing sub-gate. `verify-release` fails atomically — the
  sub-gate that printed the first non-zero exit is the culprit.
  Re-run each sub-gate individually:
  - `make verify-main` → (verify-fast + verify-unit + verify-node + verify-architecture)
  - `make verify-integration` → (verify-go-tests)
- File a `fixup!: <subject>` commit + `git rebase --autosquash` once the
  underlying red gate is fixed.
- The pre-push hook (`scripts/hooks/pre-push`) does NOT run
  `verify-release` — it is a manual operator gate, not on the
  pre-push surface (per AGENTS.md "Run `make verify-main` before
  pushing" — tier 2 only).

---

## (b) The 4 batteries of `make verify-live`

`make verify-live` is the **post-deploy gate (tier 4)**.

### Per-battery dependency matrix (NOT a uniform "all 4 require" profile)

The 4 batteries do NOT share a uniform dependency profile. The
**truthful per-battery matrix**:

| Battery | Chrome | scraper | Drive | Qdrant | Notable other |
|---|---|---|---|---|---|
| `make verify-images-live`   | yes¹ | — | yes | yes | (per `tests/operational/images_e2e.sh`¹) |
| `make verify-artlist-live`  | yes  | yes | yes | yes | SQLite outbox; server-stateful |
| `make verify-script-live`   | —    | —  | yes | yes | server-side dispatch only (no browser) |
| `make verify-vidrush-live`  | yes  | yes | yes | yes | SQLite + FFmpeg; server-stateful |

¹ `verify-images-live` is the only battery that depends on
`tests/operational/images_e2e.sh`, which is **MISSING at HEAD** (see
**Known gaps at HEAD** at the bottom of this doc). The Chrome + scraper
profile for this battery is best-effort inference pending the script's
existence.

Notes:

- **`verify-artlist-live`** exercises the full Artlist surface
  including Chrome session cookies and the scraper handshake.
- **`verify-script-live`** is server-side-only — **no Chrome hit, no
  scraper session, no FFmpeg transcoding**. It asserts
  `script.generate` dispatch + worker pull + finalizer end-to-end
  against the Drive + Qdrant surfaces only.
- **`verify-vidrush-live`** is the heaviest battery (FFmpeg, full
  Drive upload, both SQLite and Qdrant state) and is **server-stateful**
  — never run on dev workstations.

Each battery is also gated upstream by `make auth-check` (operator
pre-flight against `/api/artlist/job-consumer`) and routes through
`scripts/with-velox-auth` (the canonical token loader per AGENTS.md
"Authentication SSOT"). Per the Makefile suffix convention:
**`verify-<area>-live` = operational battery (browser+drive+qdrant); NOT
part of `verify-main` or `verify-release`**.

### Battery 1 — `make verify-images-live`

| | |
|---|---|
| **Script** | `tests/operational/images_e2e.sh` *(MISSING at HEAD — see Known gaps)* |
| **Locate Makefile target** | `grep -nE '^verify-images-live:' Makefile` |
| **Cost** | ~1–2 min (single-image ingest probe) |
| **Surface** | image ingestion + Drive upload + Qdrant projection |
| **Common trigger** | after a Drive-side or Qdrant-side change affecting image routes |

### Battery 2 — `make verify-artlist-live`

| | |
|---|---|
| **Script** | `tests/operational/artlist/run_all.sh` (composite of 9 granular sub-scripts) |
| **Locate Makefile target** | `grep -nE '^verify-artlist-live:' Makefile` |
| **Cost** | ~10–30 min (composite, server-stateful) |
| **Surface** | search → detail → download → Drive upload → SQLite outbox → Qdrant projection, executed per Artlist sub-gate |
| **Common trigger** | after a Stage 1/2/3 Artlist lib change, scraper update, or Drive folder-router change |

### Battery 3 — `make verify-script-live`

| | |
|---|---|
| **Script** | `tests/operational/script_generate_smoke.sh` |
| **Locate Makefile target** | `grep -nE '^verify-script-live:' Makefile` |
| **Cost** | ~3–5 min (text-only dispatch + worker pull + finalizer) |
| **Surface** | `script.generate` dispatch + worker pull + finalizer **WITHOUT** the full Vid Rush media path — server-side only, no Chrome, no FFmpeg |
| **Common trigger** | after a script-side or worker-side change that does not touch the media engine |

### Battery 4 — `make verify-vidrush-live`

| | |
|---|---|
| **Script** | `tests/operational/vidrush_media_e2e.sh` |
| **Locate Makefile target** | `grep -nE '^verify-vidrush-live:' Makefile` |
| **Cost** | ~10–30 min (heavy, server-stateful, end-to-end) |
| **Surface** | full Vid Rush battery — server + scraper + SQLite + FFmpeg + Drive + Qdrant, covering every stage from intake to projection |
| **Common trigger** | after ANY pipelined-component change touching FFmpeg, scraper, Drive, Qdrant, SQLite migrations, or the session auth surface. **Server-stateful** — run on dedicated operational hosts, never on dev workstations |

### Composite — `make verify-live`

Composition per Makefile (locate with `grep -nE '^verify-live:' Makefile`):

```
verify-live = auth-check
            + verify-images-live
            + verify-artlist-live
            + verify-script-live
            + verify-vidrush-live
```

**Fail-closed**: any single battery failure aborts the chain and
`verify-live` exits non-zero.

---

## (c) Per-battery debug pattern

During iteration, **surgically invoke ONE battery or ONE granular
sub-gate**. Do not pay the full cost when you are debugging one
surface. The same `SMOKE_DRY_RUN=1` short-circuit pattern applies to
every sub-script in `tests/operational/`: emit `[DRY]` banners
describing what the battery would probe; no fake server, no mocked
probe, no test-server sidecar (per AGENTS.md Simplicity &
Minimalism).

### Artlist — surgical sub-gate invocation

The 9 granular sub-gates (each ~30 s – 2 min) are listed in
`Makefile` — locate with `grep -nE '^verify-artlist-[a-z]+:' Makefile`.
Sub-script inventory: `tests/operational/artlist/{01..09}_*.sh`.

| Sub-gate | Script (under `tests/operational/artlist/`) | Cost | Surface |
|---|---|---|---|
| `verify-artlist-startup`  | `01_startup.sh`        | <30 s  | server / scraper / Chrome / SQLite / Qdrant / session auth |
| `verify-artlist-search`   | `02_search_live.sh`    | ~30 s  | `/api/artlist/search/live` |
| `verify-artlist-stream`   | `03_detail_stream.sh`  | ~30 s  | `/detail` happy + STREAM_NOT_FOUND |
| `verify-artlist-download` | `04_download.sh`       | ~60 s  | `/download` + ffprobe DoD-exact |
| `verify-artlist-pipeline` | `05_pipeline_fresh.sh` | ~3–5 min | Gates 4 + 5 + 6 + 7 + 8 fresh-run 3/3 |
| `verify-artlist-drive`    | `06_drive.sh`          | ~30 s  | Drive resolve per clip |
| `verify-artlist-index`    | `07_index.sh`          | ~30 s  | SQLite + Qdrant integrity per clip |
| `verify-artlist-cache`    | `08_cache_replay.sh`   | ~60 s  | cache_hit=true replay |
| `verify-artlist-errors`   | `09_failure_modes.sh`  | ~60 s  | SESSION_EXPIRED / STREAM_NOT_FOUND / SCRAPER_UNAVAILABLE |

**Canonical dev loop during iteration**:

```bash
# 1. While iterating on a single gate (no live-service hits):
SMOKE_DRY_RUN=1 make verify-artlist-stream     # describe probe surface; <1 s
SMOKE_DRY_RUN=1 bash tests/operational/artlist/03_detail_stream.sh   # direct

# 2. Then run for real (requires live stack + scripts/with-velox-auth-wrapped VELOX_ADMIN_TOKEN):
make verify-artlist-stream

# 3. After all 9 individual gates are green:
make verify-artlist-live
```

After a fix, re-run the affected gate only (do NOT loop the full
battery during iteration).

### Images

```bash
# During iteration (no live-service hits):
SMOKE_DRY_RUN=1 bash tests/operational/images_e2e.sh

# Real run after a Drive-side change affecting images:
make verify-images-live
```

> **Note**: `tests/operational/images_e2e.sh` is **MISSING at HEAD**
> (see **Known gaps at HEAD** below). The dry-run pattern is correct
> but the real run will fail until the script is restored.

### Script

```bash
# During iteration (no live-service hits):
SMOKE_DRY_RUN=1 bash tests/operational/script_generate_smoke.sh

# Real run after a script.generate change:
make verify-script-live
```

### Vid Rush

```bash
# Dry-run only — heavy battery:
SMOKE_DRY_RUN=1 bash tests/operational/vidrush_media_e2e.sh

# Real run on operational host ONLY:
make verify-vidrush-live
```

---

## (d) Auth contract (mandatory for ALL live batteries)

Per AGENTS.md "Authentication SSOT (Velox admin token)" and the
canonical loader contract:

- The Makefile wraps each live target with the canonical loader:

  ```makefile
  verify-X-live: auth-check
      @scripts/with-velox-auth bash tests/operational/X_e2e.sh
  ```

  This is the **only** allowed invocation pattern — never inline-bypass
  with manual `cat /etc/pipelinegen/pipelinegen.env` or any per-script
  token loader.

- The canonical loader is `scripts/with-velox-auth`. **Canonical mode
  is `0755`** (executable) so the Makefile's direct invocation
  (`scripts/with-velox-auth bash …`) succeeds. **Current HEAD mode is
  `0644` (no `+x` bit)**, which causes `Permission denied` at run
  time — a separate `gitops(auth): chmod +x scripts/with-velox-auth`
  microstep is the fix (tracked in **Known gaps at HEAD**). DO NOT
  bundle into the docs commit per AGENTS.md "Keep commits focused".

- Token hygiene (per AGENTS.md Hygiene rule): zero token values
  emitted to stdout/stderr; `scripts/with-velox-auth` regex-validates
  `^[a-fA-F0-9]{64}$` and exits 2 (non-zero, no fallback) on malformed
  input.

- `make auth-check` is the operator **pre-flight**. Run it FIRST
  before any `*-live` target if you suspect the auth surface is broken
  — it probes `/api/artlist/job-consumer` with
  `Authorization: Bearer $VELOX_ADMIN_TOKEN` and exits 1 on any
  non-200.

---

## (e) SSOT cross-references (no duplicates)

This doc intentionally does NOT repeat:

- The per-area Make targets (`verify-go-core`, `verify-go-infrastructure`,
  `verify-go-api`, `verify-go-commands`, `verify-base`, etc.) — see
  `docs/operations/verify-main-workflow.md` (tier 1 + 2 SSOT).
- The recommended dev-loop workflow — see
  `docs/operations/verify-main-workflow.md` section 3.
- The pre-push gate composition in detail — see
  `docs/operations/verify-main-workflow.md` and `scripts/hooks/pre-push`
  (canonical hook + HONOUR-RULE invariant).

**Canonical sources for tier 3 + 4**:

- AGENTS.md "Authentication SSOT (Velox admin token)" — canonical loader contract.
- AGENTS.md "Run `make verify-main` before pushing" — tier 2 only; tier 3 + 4 are NOT in pre-push.
- AGENTS.md git-workflow + HONOUR-RULE — never `--no-verify` to bypass.
- `scripts/with-velox-auth` — canonical token loader (canonical mode `0755` with shebang `#!/usr/bin/env bash`; current HEAD is `0644` — see Known gaps).
- `scripts/hooks/pre-push` — canonical pre-push hook (fail-closed; tier 2 only).
- `docs/operations/verify-main-workflow.md` — tier 1 + 2 SSOT (complementary NOT duplicate).
- `Makefile` — locate tier-3 + tier-4 targets via:
  - `grep -nE '^verify-release:' Makefile`
  - `grep -nE '^verify-live:' Makefile`
  - `grep -nE '^verify-(images|artlist|script|vidrush)-live:' Makefile`
  - `grep -nE '^verify-artlist-[a-z]+:' Makefile`
- `tests/operational/{images_e2e.sh,artlist/run_all.sh,script_generate_smoke.sh,vidrush_media_e2e.sh}` — canonical battery scripts.
- `tests/operational/artlist/{01..09}_*.sh` — the 9 Artlist sub-gates.
- `tests/operational/lib/{common,artlist,drive,sqlite,qdrant,velox_domain}.sh` — shared lib (curl/jq/sqlite dispatch).

---

## Known gaps at HEAD (this doc reflects current state, not aspirational)

These items are **documented as canonical references** but the
underlying asset or invocation contract has a known gap as of HEAD.
Operators should restore the missing piece in a **separate atomic
microstep** before invoking the affected target. Per AGENTS.md
fail-closed: "Never represent absence as a successful no-op" — do not
ship this gap as if it were wired.

### Gap 1 — `tests/operational/images_e2e.sh` MISSING

- **Affects**: Battery 1 (`verify-images-live`).
- **Symptom**: invoking `make verify-images-live` exits non-zero with
  `bash: tests/operational/images_e2e.sh: No such file or directory`.
- **Fix** (separate atomic commit): `test(operational): add images_e2e.sh
  fixture` — restore the script. The reference path and surface in this
  doc remain correct; the gap is in the asset, not in the SSOT.
- **Tracking**: see this section under "Known gaps at HEAD"; remove
  this entry once the script is restored.

### Gap 2 — `scripts/with-velox-auth` mode `0644` (canonical mode `0755`)

- **Affects**: All `verify-*-live` batteries + `make auth-check`.
- **Symptom**: invoking `scripts/with-velox-auth bash …` directly (per
  the Makefile recipe) exits `Permission denied`. The wrapper is never
  executed; the live-tier gate fails before auth can be exercised.
- **Fix** (separate atomic commit): `gitops(auth): chmod +x
  scripts/with-velox-auth` — set the canonical `0755` mode so the
  `#!/usr/bin/env bash` shebang is honoured and the direct file
  system call succeeds. NO content change to the wrapper.
- **Tracking**: see this section under "Known gaps at HEAD"; remove
  once `stat -c '%a' scripts/with-velox-auth` reports `755`.

### Reasons this doc does NOT also land the fix as a single commit

- Per AGENTS.md "Keep commits focused and describe actual behavior" +
  the user's recurring "non accumulare" directive: documentation
  updates and executable-bit fixes are two separate concerns. Bundling
  them would violate that discipline.
- The doc's job is to reference the canonical surface. The fixes
  are tracked here as known gaps so operators know what to expect
  before invoking the affected target.
