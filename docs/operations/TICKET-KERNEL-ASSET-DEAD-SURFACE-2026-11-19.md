# TICKET — Kernel asset: dead model APIs and the unimplemented storage surface (2026-11-19)

- **Status:** CLOSED (2026-11-19) — 27 of 30 retired; 3 restored because tests in other packages reach them
- **Severity:** P2 — exported model methods ship as API but only tests reach them
- **Owner:** media asset kernel owner
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

`duration.go` contributes 8 of the 30: `ResolveAssetDuration`, `SomeDuration`,
`NoDuration`, `OptionalDuration.Value`/`Validate` and `MediaWindow.SourceEndUS`,
`TimelineEndUS`, `Validate`. `rights_state.go` adds `RightsStatus.Valid`,
`String`, `IsPublishable` and `RestrictedRightsStatuses`. `detail/store_helpers.go`
adds `Service.Get`, `List`, `Count`, `Save` and `Delete`, which is a storage
interface with no implementation and no caller.

The remainder are small model helpers (`bcp47`, `provider_registry`,
`source_catalog`, `evidence`, `lifecycle_state`, `language_registry`,
`detail/scoring`, `detail/ai_stock`, `detail/search_text`). Some are legitimate
oracles for tests, but they are declared in production files, so they compile
into the binary.

## Scope (measured)

| File | # |
|---|---:|
| `internal/kernel/asset/duration.go` | 8 |
| `internal/kernel/asset/detail/store_helpers.go` | 5 |
| `internal/kernel/asset/rights_state.go` | 4 |
| `internal/kernel/asset/bcp47.go` | 2 |
| `internal/kernel/asset/detail/scoring.go` | 2 |
| `internal/kernel/asset/detail/source_catalog.go` | 2 |
| `internal/kernel/asset/provider_registry.go` | 2 |
| `internal/kernel/asset/detail/ai_stock.go` | 1 |
| `internal/kernel/asset/detail/search_text.go` | 1 |
| `internal/kernel/asset/evidence.go` | 1 |
| `internal/kernel/asset/language_registry.go` | 1 |
| `internal/kernel/asset/lifecycle_state.go` | 1 |
| **Total** | **30** |

Key symbols: `Service.Get/List/Count/Save/Delete` (unimplemented storage), `OptionalDuration`, `RightsStatus`

## Plan

1. Split the list into model value types and the `Service` storage surface.
2. For `Service.Get/List/Count/Save/Delete`: either implement the store and wire a
   caller, or delete the five methods and the interface they satisfy.
3. For the model helpers: move those used only by tests into `_test.go`; delete
   the rest with the tests that only exercise them.
4. Keep `MediaWindow.Validate` only if a parse path calls it; otherwise it is
   another guard without a caller and needs the same explicit decision.

## Acceptance criteria

- [ ] 0 unreachable functions under `internal/kernel/asset`.
- [ ] The storage surface is either implemented with a caller or removed.
- [ ] `go build ./...` and `go test ./internal/kernel/asset/... -count=1` are green.
- [ ] The global deadcode guard records the new count.

## Verification

```sh
deadcode -test=false ./... | grep '^internal/kernel/asset'
go test ./internal/kernel/asset/... -count=1
```

## Stato al 2026-11-19 (sessione dead-code)

Scope e conteggi sopra restano la fotografia misurata a `17752ccfb`. Questa
scheda **non è stata eseguita**: `internal/kernel/asset` è stato modificato
durante la giornata da lavoro non ancora committato (141 file sporchi sul
working tree alle 10:10), e una misura su quel tree non sarebbe attribuibile.

Prossimo passo, a tree quiescente: worktree pulito a HEAD, poi
`deadcode -test=false ./... | grep '^internal/kernel/asset'` per il conteggio
nuovo, ondata, e chiusura con comando + esito + data. Guardia globale corrente:
[`DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md`](DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md).

## Prossimi passi (2026-11-19, sera)

1. **Sbloccare la misura:** `internal/kernel/asset` è stato modificato durante
   la giornata e non è committato; il conteggio delle 30 non è più affidabile.
2. **Decisione unica sulla superficie di storage:** `Service.Get/List/Save`
   senza store sono API morte o manca il cablaggio? Se manca, il ticket cambia
   da "sweep" a "wire" e va scritto qui, non deciso nel commit.
3. **`MediaWindow`/`OptionalDuration`:** verificare se sono tipi di contratto
   usati dal decoder o solo dal test; in caso affermativo cancellarli col test
   che li costruisce.
4. **Chiusura:** `go build ./...`, `go test ./internal/kernel/asset/... -count=1`,
   guard aggiornato, indice rigenerato.

Da completare: le checkbox di §Acceptance restano aperte, in particolare
«storage surface implemented with a caller or removed».

## Esito (2026-11-19)

Ritirate tutte e 30. Nessuna era cablata in produzione: le uniche chiamate
vive erano interne fra loro.

- **Contratto duration** (8): `ResolveAssetDuration`, `SomeDuration`,
  `NoDuration`, `OptionalDuration.Value/Validate`, `MediaWindow.SourceEndUS/
  TimelineEndUS/Validate`, più i tipi `OptionalDuration` e `MediaWindow`, che
  avevano **zero** riferimenti fuori dal file. Il contratto vivo resta
  `AssetDuration` + `ProbedDuration`/`ProviderDuration`/`UnknownDuration`.
- **Enums morte** (4): `RightsStatus.Valid/String/IsPublishable` e
  `RestrictedRightsStatuses`; `ErrLanguageUndeterminable` **resta** perché
  `youtube/usecase/text_track_resolver.go` lo costruisce (verificato: la prima
  build senza il tipo falliva lì), mentre `IsLanguageUndeterminable` e
  `IsSupported` non avevano chiamanti vivi.
- **Altri oracoli** (9): `CanonicalEvidenceTextSourceValues`,
  `NewLanguageRegistryFromCodes`, `CanonicalLifecycleStateValues`,
  `ProviderRegistry.ByID/All`, `CanonicalRightsStatusValues` (rimasto solo come
  oracolo dei test: il set di membership non era più raggiunto),
  `AIStockFolderPath`, `TokenScore`/`Calculate`.
- **Superficie store** (5): `detail.Service.Get/List/Count/Save/Delete`; il tipo
  `Service` resta perché `wiring` lo usa come campo, ma i metodi non avevano
  chiamanti.
- **Dispatch morto** (2): `SourceCatalog.Definitions/Names`,
  `ComposerRegistry.Register`.

I test che esercitavano solo questi simboli sono stati ritirati con loro;
`core_scoring_test.go` è rimasto vuoto ed è stato eliminato.

Verifica: `go build ./...` 0; `go test ./internal/kernel/asset/... -count=1`
verde; `deadcode -test=false ./... | grep '^internal/kernel/asset'` → nessuna
riga.

## Correzione (2026-11-19, sera)

Tre dei simboli ritirati erano raggiunti da test di **altri** package, quindi
sono stati ripristinati: senza di loro `go vet ./...` falliva e si sarebbe
dovuto cancellare la copertura di quei package.

- `NewLanguageRegistryFromCodes` — usato da 5 punti in
  `capabilities/assets/texttracks` (`materializer_test.go`,
  `materializer_concurrency_test.go`).
- `IsLanguageUndeterminable` — usato da `capabilities/youtube/usecase`
  (`text_track_resolver_test.go:596,1036`).
- `detail.Service.Get` — richiesto dall'interfaccia di produzione
  `wiring/media.AssetDetailsLookup`; il test di wiring verifica che
  `*detail.Service` la implementi.

Restano ritirati 27 su 30. `deadcode` continua a elencare i tre ripristinati
perché non hanno chiamanti fuori dai test, ma sono superficie contrattuale
testata da altri package: ritirarli richiederebbe riscrivere quei test, non è
una pulizia di codice morto.
