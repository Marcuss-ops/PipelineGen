# TICKET — App wiring: test-only composition helpers (2026-11-19)

- **Status:** CLOSED (2026-11-19) — 0 unreachable functions under `internal/app/wiring`; guard 445 → 377
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

## Esito (2026-11-19)

**Decisione `WireMinimal` (2026-11-19): rimosso.** Era un secondo composition
root raggiunto solo da `health_wiring_test.go`, costruiva un `AppDeps` con
`Handlers.Registry: nil` e un `minimalLifecycle` che scartava le probe: una
"startup" che non monta rotte e non espone readiness. Il rischio descritto dal
ticket (qualcuno lo scambia per una via di boot supportata) era reale e la via
canonica `WireServices`/`InitComposition` esiste già. Rimossi `WireMinimal`,
`minimalLifecycle` (`Start`/`Stop`/`AddProbe`) e il test che li costruiva.

**Fixture spostate nei test** (in produzione non avevano chiamanti):
`newMiddlewareRateLimitAdapter`, `newMiddlewareFeatureFlagsAdapter` (ora in
`health_wiring_test.go`), `isAIImageIngestSource` (in `media_ingest_test.go`).
Nessun file di produzione dichiara più un helper che solo un test usa.

**Ritirati con i loro test:**
`wireSourcingAtomic` (gate fail-closed mai chiamato da `newAssetRegisterService`:
il port `SourcingAtomicPort` non ha consumatori, quindi il gate non poteva
scattare), `NewSceneTextGenerator` (wrapper; il sito vivo usa
`scriptwiring.NewSceneTextGenerator`), `NewAssetDestResolver` (alias di una riga),
`SafeCall`, `resolveGPUGateSlots`/`gpuGateSlots` (duplicato di
`overlays.ResolveGPUGateSlots`, che è il lettore reale di `RENDERINGGEN_GPU_SLOTS`),
`attachFinalJobDriveSources`/`finalJobAdapterString` (il live path dei final job
risolve gli stock via `stockDriveReference`), `resolveHits` (accessor di metrica
senza consumatore; l'asserzione del test legge il contatore sotto lock), e
`NewRateLimitedPublisher`/`NewRateLimitedPublisherWithGate`/`rateLimitedPublisher.Publish`
(nessuna pubblicazione voiceover vive fuori dalla pipeline port-driven;
`NewDriveUploadGate`, che è il gate condiviso reale, resta).

**Verifica:** `deadcode -test=false ./... | grep '^internal/app/wiring/'` → vuoto;
`go vet` e `go test ./internal/app/wiring/... -count=1` verdi.
