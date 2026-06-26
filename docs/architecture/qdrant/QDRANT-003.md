# QDRANT-003 — schema canonico, reindex verificato e alias switch sicuro

> **Stato:** `BLOCKED / NON CHIUDIBILE`  
> **Audit baseline:** `main@e20d5e7fc4afd9f446d9d9e92703db639008b37f` — 26 giugno 2026  
> **Tipo verifica:** audit statico; nessuna esecuzione CI associata all'HEAD.

## OBIETTIVO

La nuova collection fisica può essere promossa soltanto dopo una verifica completa di identità, count esatto, payload, versioni, dead-letter, golden query e filtri. Il reindex non deve mai scrivere in-place sulla collection attiva.

## COMPLETATO

- `DefaultV3Schema()` è la fonte canonica per dense, sparse, payload index e alias;
- il mapper usa UUID v5 e valida dimensioni/NaN/Inf;
- il verifier legge `payload.asset_id` invece di tentare una reverse mapping UUID;
- l'alias switch viene bloccato quando `SwitchReport.Ready=false`;
- il comando admin usa mapper, writer, collection manager e verifier canonici.

## BLOCKER ATTUALI

### 1. Count non esatto

Il verifier segnala errore soltanto quando `actual < expected`; `actual > expected` può passare. Il gate `Ready` usa `ActualPoints >= ExpectedPoints`, mentre la promozione richiede uguaglianza esatta.

### 2. Version check incompatibile con il payload reale

Il verifier cerca la chiave generica `embedding_version` e confronta con `CurrentEmbeddingVersion="v3"`.

Il mapper emette invece:

```text
embedding_version_text
embedding_version_transcript
embedding_version_visual
embedding_version_audio
```

con valori modello come `2026-06-16-v1`. Inoltre il controllo è limitato ai primi 1000 punti.

### 3. Point ID non verificato

Il verifier legge `payload.asset_id`, ma non controlla:

```go
pt.ID == AssetIDToQdrantPointID(assetID)
```

Un punto con payload corretto e UUID errato può essere promosso.

### 4. Gate simulati

`GoldenQueriesOK` e `FiltersOK` partono da `true` senza eseguire alcuna verifica.

### 5. Dead-letter opzionale e non cablata

Il verifier accetta `deadLetter=nil`; il comando admin lo costruisce esplicitamente con `nil`, quindi il gate può essere saltato.

### 6. Scroll incompleto non bloccante

Al raggiungimento del safety cap il verifier logga un warning e può continuare verso `Ready=true`, pur non avendo esaminato l'intera collection.

### 7. Reindex potenzialmente in-place

Il comando usa il `PhysicalName` fisso del manifest, esegue `EnsureSchema` e scrive prima di risolvere il target attuale dell'alias. Non rifiuta preventivamente `targetCollection == activeCollection`.

### 8. API key omessa nel comando admin

Il client Qdrant del reindex riceve BaseURL e Timeout, ma non `cfg.Qdrant.APIKey`.

### 9. Retention soltanto advisory

`CollectionRetentionDays` è documentato come advisory; non esiste uno stato operativo o un job che provi conservazione e rollback.

## TASK RESIDUI

### A. Verifier esatto e completo

- count con `actual == expected`;
- controllo UUID canonico per ogni punto;
- versioni per ciascun canale dal manifest;
- scan completo o `Ready=false`;
- payload minimo e set missing/orphan completi.

### B. Gate reali

Iniettare porte obbligatorie per:

- dead-letter count;
- golden query runner;
- filter matrix runner;
- target/alias validator;
- retention state.

Nessun booleano `*OK` può partire da `true` per default.

### C. Reindex immutabile

Risolvere l'alias prima di scrivere, richiedere una nuova collection fisica distinta, conservare il precedente target e testare rollback.

### D. Config completa

Propagare API key e tutte le opzioni runtime necessarie al client admin senza duplicare schema/channel config.

## LEGACY DA ELIMINARE

| Legacy | Dove | Azione |
|---|---|---|
| `ActualPoints >= ExpectedPoints` | verifier | uguaglianza esatta |
| chiave `embedding_version` | verifier | versioni per canale dal manifest |
| confronto con `CurrentEmbeddingVersion="v3"` | verifier | usare `ModelVersion` reale |
| sample primi 1000 punti | verifier | scan completo o fail-closed |
| point UUID non verificato | verifier | forward check canonico |
| `GoldenQueriesOK: true` | verifier | runner reale |
| `FiltersOK: true` | verifier | filter matrix reale |
| dead-letter `nil` | admin command | checker obbligatorio |
| safety cap non bloccante | verifier | `Ready=false` |
| target fisso potenzialmente attivo | admin flow | collection nuova distinta |
| client admin senza API key | admin command | propagare configurazione |
| retention advisory | operations | stato/job/test rollback |

## DEFINITION OF DONE

Il ticket può essere marcato `CLOSED` soltanto quando:

- build e test admin/Qdrant sono verdi;
- count atteso e reale coincidono esattamente;
- ogni point ID corrisponde al relativo asset ID;
- tutte le versioni per canale corrispondono al manifest;
- l'intera collection è stata verificata;
- missing, orphan, payload issue, version mismatch e dead-letter sono zero;
- golden query e filter matrix sono realmente eseguite;
- la collection target è nuova e diversa da quella attiva;
- rollback e retention sono verificati;
- ogni gate negativo impedisce lo switch alias;
- la CI esegue i gate automaticamente.

## GATE MINIMO

```bash
set -euo pipefail

! rg -n 'GoldenQueriesOK:\s*true|FiltersOK:\s*true' internal/infrastructure/qdrant/verifier.go
! rg -n 'ActualPoints\s*>=\s*ExpectedPoints' internal/infrastructure/qdrant
! rg -n 'payload\["embedding_version"\]' internal/infrastructure/qdrant/verifier.go
! rg -n 'NewReindexVerifier\([^\n]*nil' cmd/admin/reindex_qdrant.go
go build ./cmd/admin/... ./internal/infrastructure/qdrant/...
go test ./internal/infrastructure/qdrant/... -run 'Test.*ReindexVerifier|Test.*SwitchAlias|Test.*Rollback' -count=1
```

## NON CHIUDERE SE

- anche un solo gate è preimpostato a successo;
- un controllo usa un campione dichiarandolo verifica completa;
- dead-letter può essere saltata;
- il target può coincidere con la collection attiva;
- retention e rollback sono soltanto runbook manuali.
