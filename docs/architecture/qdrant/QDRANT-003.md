# QDRANT-003 — schema canonico, reindex verificato e alias switch sicuro

> **Stato:** `BLOCKED / DA RIAPRIRE`  
> **Audit baseline:** `main@c72949a362656f05222f333adf67b1b0eee973ae` — 26 giugno 2026  
> **Owner suggerito:** `internal/infrastructure/qdrant/` + `cmd/admin/reindex_qdrant.go`  
> **Branch suggerito:** `codex/qdrant-003-reindex-verifier`

## OBIETTIVO

Avere un solo manifest Qdrant e una pipeline di reindex che possa promuovere una nuova collection fisica soltanto dopo verifiche complete e bloccanti.

La pipeline canonica deve essere:

```text
SQLite AssetStore
  -> PayloadMapper
  -> IndexWriter
  -> nuova collection fisica immutabile
  -> ReindexVerifier
  -> SwitchAlias atomico
  -> retention/rollback della collection precedente
```

## STATO REALE

### Completato

- `DefaultV3Schema()` è la fonte canonica per vector channel, dimensioni, sparse channel, payload index e alias runtime;
- `qdrant.Config` non duplica più nomi/dimensioni dei vector channel né permette `DisableAlias`;
- `PayloadMapper` valida dimensioni, vettori vuoti e NaN/Inf;
- `IndexWriter` e `CollectionManager` sono cablati nel composition root;
- `cmd/admin reindex-qdrant` usa la pipeline canonica;
- l'alias switch è bloccato quando `SwitchReport.Ready` è falso;
- esiste un `ReindexVerifier` con count, scroll, missing/orphan, payload minimum e controllo versioni.

### Blocker attuali

1. **Verifier incoerente con il point-ID canonico.** `verifier.go` chiama `PointIDToAssetID(pt.ID)`, ma il reverse helper è stato eliminato perché UUID v5 è one-way. Il confronto deve usare `payload["asset_id"]`.
2. **Chiave embedding-version errata.** Il verifier cerca `embedding_version`, mentre il mapper emette chiavi per canale come `embedding_version_text`, `embedding_version_visual`, ecc.
3. **Golden queries simulate.** `GoldenQueriesOK` viene inizializzato a `true` senza eseguire query.
4. **Filter smoke simulato.** `FiltersOK` viene inizializzato a `true` senza testare source/category/media_type/language/workspace/lifecycle.
5. **Dead-letter non cablata nel comando admin.** `NewReindexVerifier(..., nil, ...)` salta il controllo.
6. **Scroll incompleto non blocca sempre.** Il raggiungimento del limite massimo viene soltanto loggato; una verifica parziale non può autorizzare l'alias switch.
7. **Collection target non garantita nuova.** Il comando usa il nome fisico del manifest; deve rifiutare reindex in-place sulla collection attualmente servita dall'alias.
8. **Versione/retention non persistite come stato operativo verificabile.** `CollectionRetentionDays` resta advisory e non c'è prova automatica della conservazione della collection precedente.

## TASK DI HANDOFF

### A. Riparare il verifier e renderlo compilabile

In `internal/infrastructure/qdrant/verifier.go`:

- eliminare ogni dipendenza da `PointIDToAssetID`;
- leggere e validare `payload["asset_id"]`;
- verificare anche:

```go
pt.ID == AssetIDToQdrantPointID(assetID)
```

- considerare payload senza `asset_id` un errore bloccante;
- aggiungere test per ID valido, payload mancante e point-ID non canonico.

### B. Validare le versioni per canale

Usare `IndexSchema.DenseVectors[*].ModelVersion` come SSOT.

Per ogni canale attivo e presente nel punto:

```text
embedding_version_<channel> == schema.DenseVectors[channel].ModelVersion
```

Non usare una chiave generica che il mapper non produce.

### C. Rendere reali tutti i gate di promozione

Implementare e iniettare porte tipizzate per:

- dead-letter count;
- golden query runner;
- filter matrix runner;
- schema/alias target validation;
- collection-version persistence.

Nessun campo `*OK` può partire da `true` senza aver eseguito la verifica.

### D. Vietare reindex in-place

Prima di scrivere:

- risolvere il target attuale dell'alias;
- richiedere una nuova collection fisica distinta;
- rifiutare `targetCollection == activeCollection`;
- mantenere il vecchio target per rollback secondo retention;
- non cancellare automaticamente il precedente nello stesso comando.

### E. Rendere lo scroll completo o bloccante

Se il verifier non ha esaminato l'intera collection:

- `Ready=false`;
- errore esplicito nel report;
- nessun alias switch.

Il limite può essere configurabile, ma non può trasformare una verifica parziale in successo.

## LEGACY DA ELIMINARE

| Legacy | Dove | Azione richiesta |
|---|---|---|
| riferimento a `PointIDToAssetID` | `internal/infrastructure/qdrant/verifier.go` | usare `payload.asset_id` + verifica forward canonica |
| chiave generica `embedding_version` | verifier | validare chiavi per canale dal manifest |
| `GoldenQueriesOK: true` placeholder | verifier | runner reale e risultato bloccante |
| `FiltersOK: true` placeholder | verifier | filter matrix reale e risultato bloccante |
| dead-letter checker `nil` | `cmd/admin/reindex_qdrant.go` | iniettare repository/port reale |
| scroll-cap warning non bloccante | verifier | trasformare verifica incompleta in errore |
| reindex sulla collection fisica già attiva | admin flow | richiedere target nuovo e distinto |
| retention soltanto documentata | config/runbook | persistere e testare lo stato rollback |
| gate QDRANT soltanto nel Markdown | CI | promuovere a check obbligatorio |

## DEFINITION OF DONE

Il ticket può essere marcato `CLOSED` soltanto quando:

- `go build ./cmd/admin/... ./internal/infrastructure/qdrant/...` è verde;
- il verifier usa `payload.asset_id` e controlla il point-ID canonico;
- count atteso e reale coincidono esattamente;
- missing, orphan, payload issue e version mismatch sono zero;
- lo scroll è completo;
- dead-letter aperte sono zero;
- golden query e filter matrix sono state realmente eseguite e superate;
- il target è una nuova collection fisica diversa da quella attiva;
- alias switch avviene solo dopo tutti i gate;
- la collection precedente resta disponibile per rollback secondo policy;
- un test dimostra che ogni singolo gate negativo impedisce `SwitchAlias`.

## GATE ANTI-REGRESSIONE

```bash
set -euo pipefail

# Il reverse helper one-way non deve esistere né essere chiamato.
! rg -n 'PointIDToAssetID' internal/infrastructure/qdrant cmd/admin

# Nessun placeholder positivo nel verifier.
! rg -n 'GoldenQueriesOK:\s*true|FiltersOK:\s*true' \
  internal/infrastructure/qdrant/verifier.go

# Il comando admin deve cablare dead-letter reale.
! rg -n 'NewReindexVerifier\([^\n]*nil\s*/\*\s*deadLetter' \
  cmd/admin/reindex_qdrant.go

# Verifier + alias gate.
go test ./internal/infrastructure/qdrant/... \
  -run 'Test.*ReindexVerifier|Test.*SwitchAlias|Test.*RollbackAlias' \
  -count=1

# Admin command compilabile.
go build ./cmd/admin/... ./internal/infrastructure/qdrant/...
```

## TEST MINIMI DA AGGIUNGERE

- point count minore, maggiore e uguale all'atteso;
- `asset_id` mancante o vuoto;
- UUID Qdrant non corrispondente all'asset;
- missing e orphan ID;
- version mismatch su ciascun canale attivo;
- dead-letter > 0;
- golden query fallita;
- filtro workspace/lifecycle fallito;
- scroll interrotto o cap raggiunto;
- target uguale alla collection attiva;
- alias invariato dopo qualunque fallimento;
- rollback verso il target precedente.

## NON CHIUDERE SE

- un booleano di verifica è impostato a `true` per default;
- il comando compila soltanto reintroducendo una reverse mapping fittizia;
- il verifier controlla un campione ma dichiara verificata l'intera collection;
- l'alias può puntare a una collection reindicizzata in-place;
- retention e rollback restano soltanto istruzioni manuali.
