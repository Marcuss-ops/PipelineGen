# P2-06 — Duplicazione residua: 28 → 20 gruppi cross-file

**Data:** 2026-11-26
**Priorità:** P2 (nessun bug noto; costo di manutenzione e deriva semantica)
**Stato:** **DONE**
**Area:** `internal/capabilities`, `internal/platform`, `internal/app`, `cmd`

## 1. Misura finale

`dupl -t 100 internal cmd pkg`:

| Misura | Prima | Dopo |
| --- | ---: | ---: |
| Gruppi totali | 157 | **149** |
| Gruppi di produzione | 55 | **47** |
| **Cross-file di produzione** | **28** | **20** |
| Gruppi a soglia 250 | 7 | 7 |

Il target `≤20` è raggiunto senza cancellare test e senza introdurre
un'astrazione generica non usata.

## 2. Acceptance

1. [x] **`parent_aggregator`**: una sola implementazione dello sweep in
   `capabilities/jobs/scheduling/parent_aggregation_sweep.go`; i due `Tick`
   delegano con il proprio `parentType` e il proprio `aggregateOne`.
   `parent_aggregation_sweep_test.go` pinna lista vuota, errore di lista
   (nessuna aggregazione) e continuazione dopo un errore di aggregazione; i
   test preesistenti dei due aggregatori coprono i rispettivi call site.
2. [x] **Conversioni clip**: `imagesregistry.ImageAssetToAsset` è l'unico
   owner della regola image → asset; `channels.ImageAssetToAsset` la espone e
   `capabilities/clips.ImageAssetToAsset` è un wrapper. L'artifact source
   catalog consuma l'alias invece di ricopiarla. `ClipToAssetNode` resta
   l'owner in `capabilities/clips`; `app/modules/clips` lo chiama direttamente
   e la copia locale è sparita. Il voiceover usa il canonico
   `imagesregistry.VoiceoverRecordToAsset` già esposto da `channels`.
3. [x] **Upload intent**: rimossa la `var _ interface { ... }` del repository
   SQLite, che duplicava la lista dei metodi del port. La soddisfazione è
   provata dal tipo di ritorno di `NewUploadIntentsAdapter`
   (`voiceover.UploadIntentsRepository`). La sequenza dei 5 step e la policy
   di idempotenza restano nel use case; il repository applica solo i guard
   SQL di persistenza.
4. [x] **5 `module.go`**: `submodule.BuildWithHandler` +
   `submodule.DefaultIdempotency` possiedono la validazione comune e il
   default idempotenza. Ogni package conserva solo il check della propria
   dipendenza tipizzata e il registrar.
   `modules_wiring_test.go` pinna nome, numero di route (1/1/2/2/7) e
   fail-closed su `EnabledFunc` per tutti e cinque i moduli.
5. [x] **`dupl -t 100` cross-file di produzione = 20** (da 28), senza
   cancellare test o introdurre astrazioni non usate.

## 3. Check

```bash
dupl -t 100 internal cmd pkg
go build ./...
go test ./internal/capabilities/... ./internal/platform/... ./internal/app/... -count=1
go run ./cmd/archcheck --strict
bash scripts/ci/check-deadcode-baseline.sh
```

## 4. Evidenza

**Merged owner introdotti**

- `internal/capabilities/assets/clips/submodule/submodule.go`:
  `DefaultIdempotency`, `BuildWithHandler`.
- `internal/capabilities/jobs/scheduling/parent_aggregation_sweep.go`:
  `RunParentAggregationSweep`.
- `internal/platform/sqlite/assets/imagesregistry/image_asset_converter.go`:
  `ImageAssetToAsset`.
- `internal/platform/drive/uploader_file.go`: helper condivisi `trash` e
  `delete`, usati da `TrashFolder`/`DeleteFolder`.
- `internal/capabilities/assets/providers/stock/stockpipeline/publish/publish.go`:
  `URLBasename` delega a `SanitizedURLBasename` (un solo parser URL).

**Test aggiunti**

- `internal/capabilities/jobs/scheduling/parent_aggregation_sweep_test.go`
- `internal/capabilities/assets/clips/modules_wiring_test.go`

**Output**

```text
dupl -t 100 internal cmd pkg
  gruppi totali: 149
  produzione: 47
  cross-file produzione: 20

go build ./...                                      → exit 0
go test capabilities + platform + app -count=1      → exit 0
go vet scheduling + clips                           → exit 0
archcheck --strict                                  → 0 violazioni
dead-code guard                                     → 216 / 219 GREEN
```

## 5. Residui (20 gruppi)

Non tutti sono logica di business:

- **alias di compatibilità deliberati** (`scripts/overlayplan/aliases.go` ↔
  `public_aliases_b.go`, `adapters` ↔ `vidrush/contract_aliases.go`);
- **boilerplate degli scanner archcheck** (5 gruppi di `WalkDir`/scan-root
  ripetuti in `cmd/archcheck/scan/{boundaries,governance}`);
- **forme strutturali**: due interfacce repository in `kernel/asset`, i
  registrar dei 5 moduli clips (processing/publication, bulk/ingest),
  `stock`/`stockbatches`;
- **blocchi brevi**: `imagesregistry` query loop, media processor
  `setupDirectories`, gencore `logPhaseError`, youtube `readCached`,
  translation/audio process lifecycle, soundeffects download, app wiring
  ingest service, vidrush identity validation.

Un ulteriore ratchet (<20) richiede un ticket dedicato sul boilerplate
archcheck e sugli alias, non un allargamento di questa scheda.
