# Post-Cascade Operational Readiness — June 2026

> **Status**: This is the **master post-cascade reference doc** for the
> PR1.7 youtube port cascade + PR2 canonical `DownloaderMetadata` DTO
> cascade that shipped on 2026-06-21.
>
> **Audience**: another-machine operator. Self-contained: works without
> prior context. Read top-down; metrics in §1, items in §2–§5, PR-by-PR
> plans in §6, day-1 commands in §7.
>
> **Cross-references**:
> - `AGENTS.md` (canonical project rules; this doc is **NOT** a replacement)
> - `ARCHITECTURE.md` (system architecture; consult for context)
> - `architecture/migration.yaml` (wave-by-wave source of truth)
> - `docs/migrations/internal-application-youtube.md` (per-cascade migration map)

---

## 1. Build + ship status snapshot

| Item | Status | Notes |
|------|--------|-------|
| Scoped `go vet` (5 packages: app + youtube + internal/youtube + cmd/server + cmd/worker) | **GREEN ✅** | PR1.7 cascade ship gate |
| Scoped `go build` (same 5 packages) | **GREEN ✅** | PR1.7 cascade ship gate |
| Scoped `go test -c` (application/youtube + infrastructure/youtube) | **GREEN ✅** | PR1.7 cascade ship gate |
| Cascade commit `e52bf89b` on `origin/main` | **merged --no-ff ✅** | `62ae9cd6` = merge commit |
| Branch list | only `main` + `pr/youtube-port-cascade-2026-06-21` | 3 archived branches cleaned last week |
| `Co-authored-by` trailer on cascade commit | ✅ | per AGENTS.md Git-Lesson-3 |
| **`go test ./...` full sweep** | **FAIL in 7 packages** ⚠️ | pre-existing tech debt outside cascade scope — see §3 |
| `archcheck --strict` flag | NOT exposed ⚠️ | Wave 16 future work; archcheck only runs in ratchet mode |
| `internal/application/youtube/` file count | **43** ⚠️ | mega-package violation; target 5-8 per AGENTS.md Pattern 5 |
| `internal/media/*` namespace | **102 files alive** ⚠️ | Wave 13 PENDING per migration.yaml |

### Decision matrix: what "operational" means right now

You CAN ship work that touches `internal/application/youtube/`, the cascade
ports, or the canonical `DownloaderMetadata` DTO. The cascade is green.

You CANNOT yet claim full operational readiness. Three blockers stand
between this codebase and "100% operational":

1. **3 BLOCKING latent-risk fixes** still pending from the cascade ship
   (Thumbnails data loss, searchRunnerStub silent-empty, typed-nil panic).
2. **`go test ./...` full sweep has 7 failing packages** unrelated to the
   cascade — investigated separately per §3.
3. **Mega-package violations and Wave 13 namespace cleanup** are still
   in flight; demos in `internal/application/youtube/*.go` should not
   grow a 44th file without splitting.

---

## 2. The 10-item cleanup checklist (post-cascade)

Severity tags: **BLOCKING** = ship-blocker for production /
**MEDIUM** = clean-code debt that should land before the next feature PR
on the same area / **LOW** = doc rot that won't materially affect runtime.

### [latent-risk] 3 BLOCKING

1. **Thumbnails:nil data loss** — `internal/infrastructure/youtube/metadata.go::GetVideoMetadata`
   sets `Thumbnails: nil` "to avoid leaking infra shape" while
   `DownloaderMetadata` defines the field. Front-ends or scripts that
   call `.Thumbnails[1].URL` panic silently. **PR scope**: translate
   `raw.Thumbnails` (anonymous-struct array from yt-dlp) →
   `[]youtubedto.VideoThumbnail` before assignment.

2. **searchRunnerStub silent-empty** — `SearchLive(ctx,...)` and
   `GetVideoInfo(ctx,...)` return `[]`/empty-DTO + warn-log without
   checking `ctx.Err()`. Request storms cancel contexts but the stub
   silently mask cancellations as "no results". **PR scope**: 1-line
   `if err := ctx.Err(); err != nil { return nil, err }` at the top
   of each stub method.

3. **Typed-nil composition panic** — The 12 new structural ports have
   `if s.x != nil` checks in `service.go`/`segment.go`/`subtitles.go`,
   but those checks do NOT protect against `(*Concrete)(nil)` casts
   to interface (which pass `!= nil` then panic on first method call).
   **PR scope**: introduce `pkg/portutil/isNilPort[T]` typed-nil guard
   utility + audit composition.go to use bare nil (not typed-nil) for
   not-yet-wired port fields.

### [ci/gate-expansion] 1 BLOCKING + 1 MEDIUM

4. **Verify gate expansion** — `scripts/ci-architectural-checks.sh` +
   `.github/workflows/ci.yml` do not run `go vet ./internal/api/...`
   or `go build ./cmd/worker/...`. The cascade ship passed because
   those commands were added on demand; without them in the gate, a
   port rename in a follow-up can land broken handler binaries + worker.
   **PR scope**: add the 2 commands to the CI script + workflow.

5. **archcheck `--strict` flag** — Wave 16 strict-mode ratchet has no
   CLI exposure (`scripts/archcheck -h` doesn't list it). Currently
   `wave15_exit_gate.sh` SKIPs the strict check. **PR scope**: expose
   `-strict` in `scripts/archcheck/main.go` + rerun baseline regeneration
   in strict mode to confirm zero residual violations.

### [code-cleanup] 2 MEDIUM

6. **`internal/application/youtube/` mega-package (43 files)** —
   AGENTS.md Pattern 5 caps feature dirs at 5-8 production files.
   **PR scope**: split by capability into subdirs: `segment/`, `metadata/`,
   `extractor/`, `cache/`, `jobs_integration/`, `searcher/`,
   `vitio_quality/`. Re-run scoped verify + archcheck refresh + AGENTS.md
   update (file-count list).

7. **YouTubeCacheEntry DTO home** — defined in
   `internal/application/youtube/ports.go` but is a literal SQLite row
   shape (VideoID + MetadataJSON) used only by `cacheStoreAdapter`.
   Per AGENTS.md Pattern 8, application layer is thin transport only;
   SQLite-row structs belong in `internal/infrastructure/database/sqlite/assets/`.
   **PR scope**: relocate + change adapter import.

### [test-coverage] 2 MEDIUM

8. **Adapter DTO-preservation unit test** — No test verifies that
   `raw.Thumbnails` JSON array survives the `ytDLPJSON → DownloaderMetadata`
   conversion intact. After fix #1 lands, add:
   `func TestMetadataFetcherAdapter_PreservesThumbnailsArray(t *testing.T)`.
   **PR scope**: 1 file in `internal/infrastructure/youtube/`.

9. **searchRunnerStub empty-state test** — Invalid `ctx` and the stub
   must return `ErrSearchNotImplemented` (or ctx.Err()) instead of `[]`.
   After fix #2 lands, add the assertion. **PR scope**: 1 file in
   `internal/app/youtube_adapters.go`.

### [docs-update] 1 LOW (but unblocks cross-team PR review)

10. **Master doc + per-area doc updates** — covered by THIS doc +
    `docs/migrations/internal-application-youtube.md` + AGENTS.md
    port-abstraction section + ARCHITECTURE.md section.

---

## 3. `go test ./...` pre-existing failures (NOT cascade-related)

The cascade ship gate (5 packages) passed. But `go test ./...` has
**7 packages failing today** outside the cascade scope. These are
NOT introduced by the cascade and NOT in scope for any cascade follow-up.
Investigation belongs in **separate** PRs (not bundled with cascade
follow-ups):

| Package | Failure type | Suggested owner | Wave |
|---------|--------------|-----------------|------|
| `cmd/admin` | Pre-existing — likely test scaffolding | admin-handler team | n/a (legacy) |
| `internal/application/artlist` | Pre-existing — artlist refactor followups | artlist team | Wave 12 in_progress |
| `internal/application/jobs` | Pre-existing — Wave 5_PR3 (alias cleanup) | jobs team | Wave 5 in_progress |
| `internal/infrastructure/config` | Unclear | infra team | Wave 3 done (audit needed) |
| `internal/infrastructure/database` | Unclear | infra team | Wave 10 in_progress |
| `internal/infrastructure/youtube` | Pre-existing — possibly yt-dlp subprocess mocks | youtube infra team | Wave 12 in_progress |
| `internal/media/catalogsync` | Pre-existing — Wave 11 in_progress | media team | Wave 11 in_progress |

**Action**: open a new tracking branch `pr/test-failure-audit-2026-06-21`,
audit each of the 7 packages in a 1-line summary, and dispatch
follow-ups to the right owner.

---

## 4. Wave status (per architecture/migration.yaml)

Quick reference (source of truth is the YAML, not this prose):

| Wave | Status | Notes post-cascade |
|------|--------|--------------------|
| 4A,B,C | done | cascade is a downstream effect of Wave 4A's canonicalization |
| 5 | in_progress | 5_PR3 (alias collapse) is the next PR — see §6.PR5 |
| 6 | done | scripts consolidation landed |
| 7 | done | assets unification landed |
| 8 | in_progress | 21 call sites to redirect; separate PR-block (not cascade follow-up) |
| 10 | in_progress | 33 active files; ratchet count not yet zero |
| 11 | in_progress | 44 active files; 2 sub-dirs migrated, 18 to go |
| 12 | in_progress | providers registry done; infrastructure extraction pending |
| 13 | pending | 89 files in `internal/media/*` blocking Wave 13 close |
| 14 | in_progress | api/{drive,realtime,…} → api/{assets,images,jobs,scripts} pending |
| 15 | in_progress | composition root aligned; bundle decomposition done |
| 16 | pending | zero-redundancy strict; needs archcheck --strict flag (§2.5) |
| 17 | pending | final verification + tag `architecture-clean-v1` |

---

## 5. Per-PR plans

### PR1 — Fix 3 BLOCKING latent-risk items together

**Branch**: `pr/post-cascade-latent-risks-2026-06-21` (off main)
**Scope**: only latent-risk fixes (Items 1-3 from §2)
**Verify**: scoped vet/build on 3 packages (application/youtube +
internal/infrastructure/youtube + internal/app) + scoped test-compile +
`go vet ./internal/api/...` + `go build ./cmd/worker/...` (recovery
of Items 4 expansion)

**Commits**:
1. `fix(youtube): preserve Thumbnails array in DTO conversion`
   - `internal/infrastructure/youtube/metadata.go::GetVideoMetadata`
     — translate `raw.Thumbnails` to `[]youtubedto.VideoThumbnail`
2. `fix(youtube): searchRunnerStub respects ctx.Err()`
   - `internal/app/youtube_adapters.go::SearchLive` + `GetVideoInfo`
3. `feat(portutil): typed-nil guard utility isNilPort[T]`
   - New file `pkg/portutil/isnil.go` + tests
   - Optional: replace `if s.x != nil` with `if portutil.IsNil(s.x)`
     in `service.go::md5String/md5File`, `subtitles.go::sliceSubtitles`,
     `segment.go::indexer` block

**Risks**:
- Commit 1 needs the test #8 to land in the same PR (otherwise rounds of
  pass/fail on downstream consumer tests).
- Commit 3 must NOT change behavior for non-nil ports; only add
  detection of typed-nil panics. Keep `!= nil` semantics intact.

### PR2 — Expand CI gate (Item 4)

**Branch**: `pr/ci-gate-api-and-worker-2026-06-21` (off main)
**Scope**: add 2 commands to CI
**Verify**: full project vet + build (gate-expanded) must remain green

**Commits**:
1. `ci(archcheck): add go vet ./internal/api/... + go build ./cmd/worker/...`
   - `scripts/ci-architectural-checks.sh` — append 2 commands under
     a new `Check 6: handler-binary-link` section
2. `ci(workflow): mirror the new gate in GitHub Actions`
   - `.github/workflows/ci.yml` — add the same 2 commands under the
     test step

### PR3 — Split `internal/application/youtube/` mega-package (Item 6)

**Branch**: `pr/split-youtube-megapackage-2026-06-21` (off main)
**Scope**: split 43 files into 5-7 capability subdirs
**Verify**: scoped vet/build on all youtube-related packages + scoped
test-compile + archcheck baseline refresh

**Target subdir layout**:
```
internal/application/youtube/
├── segment/          (processSegment, segment_cache, segment_lifecycle) ~5 files
├── metadata/         (metadata_persist, metadata_enrich)              ~3 files
├── extractor/        (extractor_metadata, extractor_drive, extractor_segments) ~4 files
├── cache/            (searcher_cache + cache adapters + persistence helpers) ~4 files
├── searcher/         (searcher, searcher_metadata, search_topic) ~3 files
├── vitio_quality/    (vitio_quality scoring + checks)                  ~4 files
└── ports.go, types.go, service.go, register.go, util.go at root    ~5 files
```

**Risks**:
- Heavy rename blast radius. **Mandatory**: `git grep -l
  'internal/application/youtube\\\\.'` before splitting to surface
  all import sites + plan import updates in the SAME PR.
- Don't ship PR3 + the cascade follow-ups in the same release;
  cumulative import churn is hard to review atomically.

### PR6 — Wave 5_PR3 alias collapse (Item 7 of §2 + Wave 5 closure)

**Branch**: `pr/wave-5-pr3-alias-collapse-2026-06-21` (off main)
**Scope**: drop 3 zero-copy type aliases in
`internal/application/jobs/types.go` and migrate the single known
consumer (broker) to import `internal/domain/job` directly
**Verify**: scoped vet/build on jobs + infrastructure/jobs + cmd/worker

Per `architecture/migration.yaml` Wave 5_PR3:
> Removes the three zero-copy forwarding type aliases from
> `internal/application/jobs/types.go` (Store, StartJob, RequeueResult)
> and migrates the single known consumer
> (`internal/infrastructure/jobs/local/broker.go`) to import
> `domain/job` directly.

### PR7 — Test sweep failure audit (per §3)

**Branch**: `pr/test-failure-audit-2026-06-21` (off main)
**Scope**: 1 audit branch + report, NO code changes (only investigation)
**Verify**: produces a written summary that follows up to the next 1-2
PRs targeting each owner

This is the WORKTREE you do from the "other machine". The audit is
**read-only investigation**; produce 7 short paragraphs (one per
package) describing the failure mode and likely root cause.

---

## 6. Don't-do list

These are patterns **to avoid** when extending the cascade area:

1. **Don't pass `(*Concrete)(nil)` casts to a port field in the
   `ServiceDeps{}` literal.** The `if s.x != nil` pattern does NOT
   protect you; composition-side wiring must use bare `nil:` or a
   real concrete.

2. **Don't add a port method to an empty-marker pattern** (e.g.
   `type MyPort interface{}`). Empty markers are opaque injection
   tokens; adding methods to them silently breaks compile-time
   structural-assertions that the cascade introduced.

3. **Don't reintroduce `internal/media/<feature>/` paths as new
   dependencies.** Wave 13 is in_progress; new code lives in
   `internal/application/<feature>/` or `internal/infrastructure/`,
   NOT in `internal/media/`.

4. **Don't bypass `NewService(ServiceDeps{...})` with the old
   SetXxx setters.** The setter cascade was deliberately collapsed
   in PR1.7; using a setter reintroduces drift from the canonical
   constructor.

5. **Don't claim cascade-ship is "fully operational" until Items 1-3
   in §2 are landed.** Green build ≠ green operations.

---

## 7. Day-1 commands from another machine

```bash
# Clone + verify state
git clone https://github.com/Marcuss-ops/PipelineGen.git
cd PipelineGen
git checkout origin/main
git log --oneline -3
# Expected top: 62ae9cd6 merge: ship pr/youtube-port-cascade-2026-06-21

# Re-verify the cascade ship state
go vet ./internal/application/youtube/... ./internal/infrastructure/youtube/... \
       ./internal/app/... ./internal/domain/asset/... ./cmd/server/...
go build ./internal/application/youtube/... ./internal/infrastructure/youtube/... \
         ./internal/app/... ./internal/domain/asset/... ./cmd/server/...
go test -c -o /dev/null ./internal/application/youtube/
go test -c -o /dev/null ./internal/infrastructure/youtube/
# All GREEN expected.

# Investigate the 7-package test failures (§3)
go test ./... 2>&1 | grep -E '^FAIL|^ok' | head -20

# Open one of the PR branches (e.g. PR1 = latent-risks)
git fetch origin
git checkout -b pr/post-cascade-latent-risks-2026-06-21 origin/main
# Make changes; verify; commit per AGENTS.md Git-Lesson-3 (Co-authored-by trailer)
# Land via: git push origin HEAD; merge --no-ff to main locally

# Re-run archcheck baseline refresh after committing changes
go run ./scripts/archcheck --update
# Edit scripts/archcheck/baseline-justifications.md with any new entries
git diff scripts/archcheck/baseline.json scripts/archcheck/baseline-justifications.md

# Re-run the wave15 exit gate
bash scripts/wave15_exit_gate.sh
```

---

## 8. Cx cross-references

When in doubt, the canonical answers live at:

| Topic | File | Why |
|-------|------|-----|
| Critical rules for agents | AGENTS.md | CRITICAL - authoritative on rule conflicts |
| System architecture | ARCHITECTURE.md | Diagram + module ownership |
| Wave-by-wave status | architecture/migration.yaml | Single source of truth |
| Ratchet + alias accounting | scripts/archcheck/baseline-justifications.md | Per-entry justifications |
| Per-wave migration history | docs/migration-maps/*.md | Historical context |
| Most recent operational changelog | docs/CHANGELOG_2026-06-03.md | Recent structural changes |
| This doc's narrow scope | docs/POST_CASCADE_OPERATIONAL_READINESS.md | You are here |

The next operator: read §1, §2, then **pick one PR per work session**.
The latent-risk items in §2 BLOCKING should NOT wait for the longest
PR to land — fix #1 or #4 individually in 1-line PRs if needed.

---

*Last updated: 2026-06-21 (post-cascade ship, working tree clean on
`pr/post-cascade-docs-2026-06-21`). Next refresh: after PR1 from §5
lands.*
