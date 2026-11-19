# TICKET — App wiring: test-only composition helpers (2026-11-19)

- **Status:** OPEN (2026-11-19) — 16 unreachable functions in `internal/app/wiring`
- **Severity:** P2 — a second composition root (`WireMinimal`) and adapters that nothing calls
- **Owner:** composition root owner
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

The wiring package declares a minimal composition root
(`WireMinimal`, `minimalLifecycle.Start/Stop/AddProbe`) that only
`health_wiring_test.go` reaches, plus adapters and resolvers with no caller:
`newMiddlewareRateLimitAdapter`, `newMiddlewareFeatureFlagsAdapter`,
`wireSourcingAtomic`, `isAIImageIngestSource`, `NewSceneTextGenerator`,
`NewAssetDestResolver`, `SafeCall`, `gpuGateSlots`/`resolveGPUGateSlots`,
`RemoteFinalJobAdapter.attachFinalJobDriveSources`, `finalJobAdapterString` and
`RemoteFinalJobAdapter.resolveHits`.

A test-only composition root is the riskiest kind of dead code: it looks like a
supported startup path, so a future change can wire production through it and
inherit behavior no production test exercises.

## Scope (measured)

| File | # |
|---|---:|
| `internal/app/wiring/wire_services_orchestration.go` | 4 |
| `internal/app/wiring/adapters_middleware.go` | 2 |
| `internal/app/wiring/rendering/runtime.go` | 2 |
| `internal/app/wiring/script/remote_final_job.go` | 2 |
| `internal/app/wiring/assets_register_sourcing.go` | 1 |
| `internal/app/wiring/lifecycle/runtime.go` | 1 |
| `internal/app/wiring/module_media_ingest.go` | 1 |
| `internal/app/wiring/script/remote_final_job_resolve.go` | 1 |
| `internal/app/wiring/script_generation_runtime.go` | 1 |
| `internal/app/wiring/wire_assets.go` | 1 |
| **Total** | **16** |

Key symbols: `WireMinimal`, `minimalLifecycle`, `resolveGPUGateSlots`, `attachFinalJobDriveSources`

## Plan

1. Decide `WireMinimal`: either it is the supported minimal startup (then add
   a production caller and a test that proves it boots) or it is deleted with
   the lifecycle test that uses it.
2. Delete the unused adapters/resolvers, or move the ones that only a test uses
   into `_test.go`.
3. For `attachFinalJobDriveSources` / `resolveHits`: confirm the live remote
   final-job path never needs them before deleting; if it does, wire them and
   cover the branch.

## Acceptance criteria

- [ ] 0 unreachable functions under `internal/app/wiring`.
- [ ] One composition root remains, or `WireMinimal` has a production caller and
      a boot test.
- [ ] `go build ./...` and `go test ./internal/app/wiring/... -count=1` are green.
- [ ] The global deadcode guard records the new count.

## Verification

```sh
deadcode -test=false ./... | grep '^internal/app/wiring/'
go test ./internal/app/wiring/... -count=1
```
