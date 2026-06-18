# [FOLLOWUP] Pre-existing build errors in `internal/app/` on 812e980

**Status:** Tracking — surfaced during rebase validation of PR-12 + storage facade
**Branch:** `main` @ `bcca7d6` (origin/main @ `812e980`)
**Severity:** Medium — `go build ./...` does not pass on either branch. `go build ./internal/media/storage/...`, `./internal/media/images/...`, `./internal/media/fullimages/...`, `./internal/api/handlers/sources/...`, `./internal/sources/artlist/...` all compile.

---

## Summary

`internal/app/` does not compile on a clean `origin/main` (812e980) — independent of the storage-facade rebase work. The same 8 errors appear with no local changes applied. They look like scaffolding that PR-12 (canonical asset columns + `assetrepo.Repository` + `LocationEnricher` wiring) was mid-flight on.

This doc tracks the breakage so the team can decide the path forward (fix-forward / revert-and-rebuild / branch into a surgical pre-PR).

---

## Reproduction (deterministic)

```bash
cd /home/pierone/Pyt/Pipeline\ Gen

# 1. Origin/main, NO local commits applied
git worktree add /tmp/clean-main origin/main
( cd /tmp/clean-main && go build ./... )
# → exits 1 with errors A–H below

# 2. Local HEAD (after rebase)
go build ./...
# → exits 1 with the SAME errors A–H

# This proves the errors are pre-existing and not introduced by the rebase.
```

---

## Verbatim error list (origin/main @ 812e980 + local HEAD, identical)

| # | Kind | Symbol / Symptom                                          | File:line                          |
|---|------|-----------------------------------------------------------|------------------------------------|
| A | undefined type | `DriveDestinations`                          | `internal/app/compose_core.go:43`, `internal/app/service_types.go:61` |
| B | undefined type | `scheduler.LifecycleScheduler`               | `internal/app/service_types.go:80` (new at `compose_integration.go:185` too) |
| C | undefined type | `outboxevents.Pool`                          | `internal/app/service_types.go:103` |
| D | undefined variable | `mod` (possibly module ref)            | `internal/app/assets.go:148`       |
| E | missing field | `coreDeps.AssetProcessingRepo`              | `internal/app/artlist.go:236`      |
| F | missing field | `coreDeps.AssetVersionsRepo`                | `internal/app/artlist.go:237`      |
| G | missing field | `services.deliverySvc`                       | `internal/app/background_jobs.go:289` |
| H | wrong arguments to `drivecleanup.NewService` | called with `(artlistRepo, driveUploader, log, true)`; expects 0 | `internal/app/assets.go:77`, `internal/app/drive.go:35` |
| I | type mismatch | `models.JobTypeSystemCleanup` is a `JobType`, not `string` | `internal/app/background_jobs.go:161` |

(The earlier shorthand "6 errors" covered unique-symbol counts; the full surface is 9 sites across 7 files.)

---

## What likely caused it

- `812e980` is the apex of the team's PR-12 (asset repo migration). It added `LocationEnricher`, wired `assetrepo.Repository` into `compose_core.go`, and removed the dual-write bridge.
- The wiring changes require consumer updates (`DriveDestinations`, `coreDeps.Asset*Repo`, `services.deliverySvc`, the rebuilt `drivecleanup.NewService` signature, and the `JobType` typed-constant migration). The consumer files in `internal/app/` appear to have been left at the old shape — possibly because PR-12 was merged mid-refactor, or because the consumer migration belongs to a follow-up PR.
- The `scheduler.NewLifecycleScheduler(cfg, jobsService, log)` call site at `compose_integration.go:185` exists, but `service_types.go:80` references a type (`*scheduler.LifecycleScheduler`) that should have been declared in the scheduler package — the type wasn't introduced.
- `outboxevents.Pool` and `mod` look like the team introduced scoped export aliases (likely in `internal/repository/outboxevents/` and a `module` package rename) but didn't update the consumer field.

---

## Recommended fix strategies

### Option 1 — Surgical fill-in (smallest delta, recommended)
1. **DriveDestinations**: declare a minimal type in `internal/app/` (or in a shared package if more callers are expected):
   ```go
   type DriveDestinations struct { SoundEffects, ImageVideo, … string }
   ```
2. **scheduler.LifecycleScheduler**: declare the type in `internal/app/scheduler/` if missing; instantiate as today.
3. **outboxevents.Pool**: declare the type in `internal/repository/outboxevents/` (likely `type Pool struct { … }`).
4. **mod**: investigate rename — likely `internal.app.module` was renamed to `internal.app.mod`. Roll-back alias OR update the reference at `assets.go:148`.
5. **AssetProcessingRepo / AssetVersionsRepo + services.deliverySvc**: add the missing fields to `CoreDeps` / `services` structs (likely zero-value nil is acceptable).
6. **drivecleanup.NewService**: re-introduce the dropped `(artlistRepo, driveUploader, log, dryRun bool)` signature; update both call-sites to use the new args.
7. **JobTypeSystemCleanup**: cast to `string(models.JobTypeSystemCleanup)` OR update the consumer field to `JobType`.

This is ~80–120 LOC of mechanical fix across ~7 files. Estimated effort: 1–2 hours. Risk: low (no semantic changes, just type/field presence).

### Option 2 — Revert PR-12 apex and re-apply on a feature branch
Slower and riskier. Useful only if PR-12 needs broader re-design.

### Option 3 — Split the consumer migration into a follow-up PR
Keep PR-12 merged (asset repo is canonical, storage facade is unaffected) and accept `go build ./...` still failing until a dedicated `internal/app/` migration PR lands. **This is what's happening today by accident** — making it explicit is a 30-min option.

---

## Non-goals

- The `internal/media/storage/` facade bug fix (PR-12 rebase) IS already landed on `bcca7d6` and not affected by these errors.
- Runtime paths exercised by `go build ./internal/media/{storage,images,fullimages}/...`, `./internal/api/handlers/sources/...`, `./internal/sources/artlist/...` all compile cleanly.
- No data loss; this is a wiring gap, not a schema gap.

---

## Verify (after fix lands)

```bash
go build ./... && echo BUILD_OK
go test -count=1 ./internal/app/...
go vet ./internal/app/...
```

Expected: `BUILD_OK`, tests pass, vet silent.

---

## Owner

Suggested owner: whoever owns PR-12 / `assetrepo.Repository` wiring.

---

**Filed by:** post-rebase validation pass
**Captured at:** commit `bcca7d6` on `main`
