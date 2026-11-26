# 02 — Asset transfer: porte del consumer, errori propagati, stati terminali

**Data:** 2026-11-26
**Priorità:** P1
**Stato:** **DONE**
**Area:** `internal/capabilities/jobs`, `internal/app/wiring`,
`internal/capabilities/assets/providers/stock/enrichment`, `internal/kernel/event`

## 1. Risoluzione asset: nessun errore inghiottito

`AssetTransferServiceImpl.resolve` interrogava quattro fonti e scartava ogni
errore (`if err == nil ...`), poi restituiva `(nil, nil)`: un database
irraggiungibile era indistinguibile da un asset inesistente.

Ora:

- ogni porta ha il contratto **`(nil, nil)` = assente; errore = guasto da
  propagare**, e gli adapter traducono `sql.ErrNoRows` in `(nil, nil)`;
- `asset.ErrNotFound` è riconosciuto come assenza, ogni altro errore è
  restituito con il nome della lookup (`resolve asset %s: canonical media
  lookup: ...`);
- `(nil, nil)` resta solo quando **tutte** le fonti hanno risposto "assente", e
  `Download` lo traduce in un errore esplicito.

Test (`internal/capabilities/jobs/asset_transfer_resolve_test.go`):

| Test | Pin |
| --- | --- |
| `TestResolveSurfacesCanonicalLookupError` | un errore Postgres arriva al chiamante, non diventa "assente" |
| `TestResolveSurfacesWorkerOutputLookupError` | un errore SQLite arriva al chiamante |
| `TestResolveTreatsTypedNotFoundAsAbsence` | `asset.ErrNotFound` non blocca il fallback |
| `TestResolveReturnsNilOnlyWhenEverySourceIsEmpty` | `(nil, nil)` solo con tutte le fonti vuote; `Download` fallisce |

## 2. Outbox enrichment: atomicità e stati terminali

**Atomicità.** `EmitAssetPublished` apre una transazione propria, esegue
`Enqueue` dentro la tx e committa prima di restituire: `nil` significa che la
riga è in `outbox_events`. Verificato anche l'ordine nel handler:
`UpdateEnrichedMetadata` → `emitAssetPublishedV1`, e un errore di emit fa
fallire il job. La finestra "UPDATE fatto, evento no" è quindi recuperabile: il
job viene ritentato, l'UPDATE è idempotente e la chiave dell'evento è derivata
in modo deterministico da `(chunk_id, content_hash, versione)`.

**Il buco vero era un falso successo.** Con `Inserted=false` l'emitter
registrava `existing_status` e restituiva `nil` **sempre**, anche quando la riga
preesistente era `dead_letter`: un evento mai consegnato veniva riferito come
consegnato.

Nuova classificazione, con un solo owner in `internal/kernel/event`:

```go
IsTerminalOutboxStatus("dead_letter") // true (già esistente)
IsFailedOutboxStatus("dead_letter")   // true  (nuovo: terminale di FALLIMENTO)
IsFailedOutboxStatus("completed")     // false (terminale di successo)
```

L'emitter restituisce `ErrEnrichmentEmitFailed` quando la riga preesistente è
`dead_letter`/`dead`, e resta `nil` per `completed`/`superseded` (replay
idempotente legittimo) e per `pending`/`processing` (l'evento esiste già).

Fault injection (`outbox_emitter_test.go`):

- `TestOutboxBackedAssetPublishedEmitter_DeadLetterReplayIsAnError` — per
  `dead_letter` **e** `dead` (spelling legacy) l'emit è un errore e la riga
  esistente non viene toccata;
- `TestOutboxBackedAssetPublishedEmitter_CompletedReplayIsSuccess` — il
  replay di un evento consegnato resta un successo.

Conseguenza operativa dichiarata: un evento in `dead_letter` **non** si recupera
con lo stesso `event_key`; serve una nuova chiave (azione operatore). Questo è
il motivo per cui il retry ora fallisce invece di mentire.

## 3. Disaccoppiamento: porte possedute dal consumer

Prima: `AssetTransferServiceImpl` aveva campi
`*assetindex.Service`, `*imagesrepo.ImagesRepository`,
`*sqliteassets.VoiceoversRepository` e importava `platform/drive`.

Dopo:

- `internal/capabilities/jobs/asset_transfer_ports.go` dichiara
  `WorkerOutputStore`, `ImageAssetLookup`, `VoiceoverLookup` e i due record
  minimi. `WorkerOutputStore` esprime **intenzioni**
  (`MarkUploading`/`MarkUploaded`/`MarkReady`), non scritture di record: il
  read-modify-write appartiene all'adapter, quindi la capability non può
  azzerare colonne che non modella.
- `internal/app/modules/registry/asset_transfer_adapters.go` implementa gli
  adapter SQLite (il layer di composizione che già possiede gli adapter
  dell'asset index, e l'unico che può conoscere entrambi i lati) e traduce
  `sql.ErrNoRows` in `(nil, nil)`. Non vive in `app/wiring`: il root è un
  hotspot registrato a 42 file / 9.8k LOC e non può crescere (vedi
  `architecture/package_hotspots.json`).
- `normalizeDownloadURL` usa `kernel/driveurl` invece di `platform/drive`: alla
  capability serve il parsing dell'URL, non il client Drive.
- Il blocco di defaulting (`asset_type=worker-output`, `source=worker`,
  `source_id=assetID`) era copiato in tre metodi; ora esiste una sola volta,
  nell'adapter.

`internal/capabilities/jobs` non importa più `platform/sqlite`, `platform/drive`
né `database/sql`. Restano 5 archi verso `platform` (`httpclient`,
`httpserver`, `httpserver/middleware`, `instaeditcalendar`, `observability`):
sono trasporto/telemetria della capability, non persistenza.

Misura: archi `capabilities → platform` **142** (146 documentato in
`refactor-p0-2026-11-26/05`).

Test: `internal/app/modules/registry/asset_transfer_adapters_test.go` pinna
l'invariante
che il vecchio codice inline non garantiva —
`MarkReady` deve preservare `operation_key`, `group_name`, `subfolder`,
`local_path`, `content_hash`, `metadata_json`; più i default di creazione e
`(nil, nil)` per una riga assente.

## 4. Precedenza delle fonti: SSOT prima delle proiezioni

`ARCHITECTURE.md`: PostgreSQL è autoritativo per il dominio media. Il vecchio
`resolve` interrogava **prima** l'indice SQLite e solo dopo la lookup canonica:
un asset committato su PostgreSQL poteva essere risolto da una riga SQLite
obsoleta.

Ordine attuale: lookup canonica (PostgreSQL) → proiezione worker-output →
immagini → voiceover. `TestResolvePrefersCanonicalMediaSSOTOverStaleIndex` pinna
che il path canonico vince su una riga stale dell'indice.

## 5. Cleanup: contesto staccato ma con deadline

`Release` dello staging usava `context.Background()`: giustamente staccato dalla
richiesta cancellata, ma senza limite. Ora `releaseStaging` usa
`context.WithTimeout(context.Background(), 30s)` in entrambi i punti (errore di
`os.Open` e `Close` del `ReadCloser`). `TestReleaseStagingIsDetachedButBounded`
verifica che il contesto passato allo stager porti una deadline.

Race detector sui package toccati:

```text
go test -race -count=1 \
  ./internal/capabilities/jobs \
  ./internal/capabilities/assets/providers/stock/enrichment \
  ./internal/kernel/event ./internal/kernel/script/imageverify \
  ./internal/capabilities/scripts/vidrush          → ok (2m08s)
go test -race -count=1 -run 'TestWorkerOutputStore|TestComposition|TestNewComposeRoot' \
  ./internal/app/wiring                            → ok
```

## 6. Acceptance

1. [x] Nessun errore infrastrutturale viene trattato come "asset assente".
2. [x] `(nil, nil)` ha un solo significato: tutte le fonti dicono "assente".
3. [x] La capability non dipende da implementazioni SQLite o dal client Drive.
4. [x] Le mutazioni worker-output preservano le colonne non modellate (test).
5. [x] La SSOT media precede le proiezioni (test).
6. [x] Un evento `dead_letter` non produce più un falso successo (test).
7. [x] Il cleanup dello staging è staccato e limitato nel tempo (test).
8. [x] Race detector verde sui package toccati.

## 7. Residui

- **Crash tra UPDATE ed emit**: coperto dal retry idempotente e documentato, ma
  non esercitato con un test di crash end-to-end (richiederebbe un worker reale
  e un kill in-finestra). Il rischio residuo è un evento non emesso finché il
  job non viene ritentato; non è un falso successo.
- **`WorkerOutputStore` è ancora backed da SQLite**: il disaccoppiamento è di
  dipendenza (la capability non conosce l'implementazione), non di storage.
