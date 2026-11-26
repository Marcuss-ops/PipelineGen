# P2-08 — Dead code: 267 (tre main) / 216 (guard corrente)

**Data:** 2026-11-26
**Priorità:** P2
**Stato:** **DONE**
**Area:** `internal/capabilities/scripts`, `internal/platform/sqlite`,
`pkg/veloxclient`, `internal/app/knobs`, `scripts/ci/check-deadcode-baseline.sh`
**Relazione:** prosegue RA-03 e
`docs/operations/DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md`.

## 1. I due root set

| Radice | Comando | Funzioni |
| --- | --- | ---: |
| Tre entrypoint di produzione | `deadcode -test=false ./cmd/server ./cmd/worker ./cmd/admin` | **267** (266 dopo lo sweep) |
| Guard di produzione (corrente) | `scripts/ci/check-deadcode-baseline.sh` (esclude il test-support) | **216** (max **219**) |
| Vista `./...` con test-support (allo sweep) | `deadcode -test=false ./...` | **246** |
| Vista con i test (allo sweep) | `deadcode -test ./...` | **82** |
| Soglia storica | `docs/operations/deadcode-2026-11-21.txt` | **229** |

I numeri non sono equivalenti e nessuno di essi è una soglia unica:

- **267** misura i tre entrypoint allo snapshot finale: è il numero
  “operativo” del ticket storico `291`, sceso a **266** dopo il ritiro di 31
  funzioni e risalito di 1 per una funzione del WIP esterno;
- **219** è la baseline congelata del guard, ristretto ai package di
  produzione; la misura corrente è **216** (4 entry untracked esterne sono
  segnalate e non contano). Le quattro famiglie di test-support (`pgtest`,
  `sqltest`, `testsupport`, `mutationprobe`) sono oracoli, non codice di
  produzione, e sono state inventariate fuori dal guard (dettaglio §4);
- **82** era la vista test-aware allo sweep: le funzioni raggiungibili dai test
  non sono candidate alla cancellazione.

La soglia non è stata alzata: **229 → 219** è un ratchet verso il basso.

## 2. Acceptance

1. [x] Documentati i due root set e quale numero è il guard; `267`/`266` non
   sono usati come soglia.
2. [x] Classificati i 291/273 per famiglia: **31 ritirate** (zero-reference,
   nessun contratto), **238 test-only inventariate**, **8 rimaste** (3 nel WIP
   esterno `kernel/outbox`, 5 metodi potenzialmente legati a interfacce).
3. [x] Rimosse solo funzioni senza chiamanti né contratto, in un unico batch
   non committato; i costruttori usati dai test restano oracoli.
4. [x] `pkg/veloxclient` non toccato.
5. [x] Guard **216 ≤ 219 ≤ 229**; `deadcode -test` scende a 82 per rimozione
   di codice di produzione, non per cancellazione di copertura.

## 3. Check

```bash
bash scripts/ci/check-deadcode-baseline.sh
deadcode -test=false ./cmd/server ./cmd/worker ./cmd/admin | wc -l
go build ./... && go vet <package toccati>
go test ./internal/app/... ./internal/capabilities/... ./internal/kernel/media -count=1
go run ./cmd/cyclo-budget -check
go run ./cmd/archcheck --strict
```

## 4. Evidenza 2026-11-26

**Partenza:** guard `./...` a **273 > 229**. Delta vs baseline congelata:
**+100 / −56** (net **+44**); **+26** funzioni erano morte anche con i test,
**+18** erano test-only.

**Ritiro:** 31 funzioni zero-reference in 16 file, tutte in file tracciati:

- 5 costruttori/alias del processor persistence, 4 alias preparation, 5 alias
  renderqueue/public facade, 3 wrapper `buildbundle`, 2 funzioni `knobs`,
  2 selettori immagine, 1 selettore preset, 1 `MapPresetIDs`,
  1 `EntitySpokenWindowDuration`, 1 helper scene, 1 helper annotazioni,
  1 `WaitRenderQueueTerminal` (renderqueue), 1 `NewGenerationGate`,
  1 `NewOllamaChecker`, 1 `DefaultAssemblyReadyVideoContract`,
  1 `NewYouTubeAssetWriter`, 1 `specScenesFromInput`.

Allo sweep, dopo il ritiro: `deadcode -test=false ./...` = **246** (4 nuove funzioni sono
arrivate dal WIP esterno `kernel/outbox`, non da questa wave); test-aware =
**82**; intersezione truly-dead = **8**. Le 8 rimaste sono 3 funzioni del WIP
esterno outbox e 5 metodi (`MapGroupConfig.Pins`, `BatchItem.UnmarshalJSON`,
`StreamSignature.Fingerprint`, `Model.Identity`, `unimplemented.Translate`):
classificate **inventariare**, non cancellate alla cieca.

**Perimetro del guard:** le quattro famiglie di test-support sono fuori dal
computo di produzione. Con il filtro:

```text
dead-code guard: 4 untracked entries not counted (pending work)
dead-code guard: 216 unreachable production functions (max 219)
✅ dead-code guard passed
```

- `scripts/ci/check-deadcode-baseline.sh`: `MAX=219`, package list filtrata e
  conteggio dei soli file tracciati; un file untracked in corso
  (`internal/kernel/script/contract_version.go`, `SupportedEnvelopeVersions`)
  è segnalato come escluso, non conta e non alza `MAX`.
- `docs/operations/deadcode-2026-11-26.txt`: baseline congelata, **219** righe.
- `deadcode -test=false ./cmd/server ./cmd/worker ./cmd/admin` = **267** allo
  snapshot finale (**266** dopo il ritiro; +1 dal WIP esterno).
- `go build ./...` exit 0; `go vet` sui package toccati exit 0.
- Test verdi su `internal/app/...`, `internal/capabilities/...`,
  `internal/kernel/media`, `cmd/cyclo-budget`.
- `make verify-cyclo` exit 0 (40/40).
- `archcheck --strict` exit 0 (0 violazioni).
- `pkg/veloxclient` invariato.

## 5. Note

- Il test-support non è stato cancellato: è inventariato e fuori dal guard di
  produzione. Le 238 test-only restano coperte dai test.
- `sceneRenderDispatchSealed = false` resta nel tree: dopo il refactor esterno
  `setSceneRenderExpectations` delega a `renderunits.RecordExpectations` e lo
  passa ancora. Non è una funzione `deadcode`, ma è un wiring da chiudere in un
  ticket dedicato.
- Il WIP esterno `internal/kernel/outbox` e i file `outboxevents` modificati
  non sono stati toccati.
