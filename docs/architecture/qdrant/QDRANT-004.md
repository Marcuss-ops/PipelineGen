# QDRANT-004 — media-search privato, hybrid reale e delivery firmata

> **Stato:** `BLOCKED / NON CHIUDIBILE`  
> **Audit baseline:** `main@e20d5e7fc4afd9f446d9d9e92703db639008b37f` — 26 giugno 2026  
> **Tipo verifica:** audit statico; nessuna esecuzione CI associata all'HEAD.

## OBIETTIVO

Una sola API privata deve garantire worker auth, isolamento workspace, hybrid dense+sparse reale, hydration SQLite sicura e delivery URL firmata. Una richiesta `mode=hybrid` non può mai degradare silenziosamente ad ANN.

## COMPLETATO

- il client Qdrant supporta `/points/query` con prefetch e RRF;
- workspace ID viene propagato al filtro Qdrant e alla query SQLite;
- il service rifiuta workspace vuoto e `default`;
- il signer richiede secret di almeno 32 byte e il builder usa `/internal/v1/deliver`;
- la mancata configurazione del signer produce errore, non URL vuota silenziosa;
- il module registry non monta mediasearch sotto `/api/internal`.

## BLOCKER ATTUALI

### 1. Route production ancora montata dopo `Setup()`

`cmd/server/main.go` chiama `SetMediasearchHandler` dopo che `NewServerWithHealth` ha già costruito il `gin.Engine`. Il test corrente configura invece il Router prima di `Setup()` e non intercetta il difetto production.

### 2. Hybrid request senza sparse channel

`mediasearch.Service` costruisce `search.VectorConfig` con il solo `TextVectorName`. `search.VectorConfig` non espone alcun `SparseVectorName`, quindi la richiesta hybrid non passa il canale `bm25_text` al vector store.

### 3. Fallback ANN silenzioso

Il search adapter crea il vettore sparse soltanto quando `SparseVectorName` è valorizzato. Con la richiesta corrente resta nil; `Searcher.HybridSearch` esegue quindi `Search()` dense-only. Il service continua però a trattare la richiesta come hybrid.

### 4. Drift `status` vs `lifecycle_state`

- `BuildPayload` scrive `status`;
- `DefaultV3Schema` indicizza `status`;
- il search adapter filtra `lifecycle_state`.

Il filtro lifecycle non usa quindi la stessa chiave posseduta dal manifest/payload. Questo può produrre zero risultati o una protezione inefficace, a seconda dei punti storici.

### 5. Hydration lifecycle incompleta

La query SQLite applica workspace, ma non imposta `filter.States`. Dopo la lettura scarta soltanto lo stato esattamente `deleted`; archived, pending, error e altri stati non-searchable possono attraversare l'hydration.

### 6. Locator nel DTO applicativo

`VectorSearchResult` conserva `LocalPath` e `DriveLink`, anche se il mapper non li popola più.

### 7. Commenti delivery con URL legacy

`delivery/signer.go` continua a descrivere esempi e un futuro handler su `/api/internal/v1/deliver`, mentre il percorso canonico costruito dal composition root è `/internal/v1/deliver`.

### 8. E2E assente

Non esiste una prova completa HTTP -> WorkerAuth -> embed -> tokenize sparse -> Qdrant Query API -> workspace hydration -> signed delivery URL.

## TASK RESIDUI

### A. Rendere hybrid fail-closed

Aggiungere il canale sparse a un resolver/config port canonico derivato da `IndexSchema`. Il service deve passarlo esplicitamente.

`mode=hybrid` senza sparse deve restituire un errore tipizzato, non chiamare ANN. ANN deve essere eseguita soltanto con `mode=ann`.

### B. Unificare le chiavi lifecycle

Scegliere una sola chiave canonica (`lifecycle_state` oppure `status`) nel manifest, mapper, query filter e migrazione dei punti storici. Vietare stringhe duplicate fuori dal resolver/schema comune.

### C. Rendere sicura l'hydration

Applicare in SQL gli stati ricercabili, oltre al workspace. Il filtro post-query deve essere difesa aggiuntiva e coprire tutti gli stati non-searchable.

### D. Correggere il route wiring

Passare il mediasearch handler prima di `Router.Setup()` e testare il costruttore production.

### E. Test E2E delivery

Verificare URL non vuota, firma, expiry, tamper di asset/workspace, secret mancante e route receiver reale.

### F. Ripulire DTO e documentazione

Rimuovere i locator applicativi e ogni riferimento a `/api/internal/v1/deliver`.

## LEGACY DA ELIMINARE

| Legacy | Dove | Azione |
|---|---|---|
| setter mediasearch post-Setup | server composition | injection pre-Setup |
| `VectorConfig` senza sparse | application search port | aggiungere resolver canonico |
| fallback ANN da HybridSearch | qdrant searcher | errore fail-closed |
| `status` / `lifecycle_state` drift | schema, mapper, adapter | una sola chiave SSOT |
| hydration che scarta solo deleted | read adapter/repository | SQL states allowlist |
| locator nel DTO | application search | eliminare campi |
| `/api/internal/v1/deliver` nei commenti | delivery signer | correggere documentazione |
| test Router non-production | routes test | E2E constructor test |
| gate soltanto Markdown | CI | required check |

## DEFINITION OF DONE

Il ticket può essere marcato `CLOSED` soltanto quando:

- la route è realmente presente nel server production e protetta da WorkerAuth;
- `mode=hybrid` produce dense+sparse+RRF oppure fallisce esplicitamente;
- ANN è selezionabile soltanto come mode distinta;
- manifest, payload e filtri usano la stessa chiave lifecycle;
- workspace e stati ricercabili sono applicati in Qdrant e SQL;
- nessun locator è presente nel DTO;
- ogni hit restituito ha una delivery URL valida;
- E2E tamper/expiry/auth è verde;
- CI impedisce la reintroduzione di fallback e drift di chiavi.

## GATE MINIMO

```bash
set -euo pipefail

rg -n 'SparseVectorName:' internal/application/mediasearch/service.go
! rg -n -U 'func \(s \*Searcher\) HybridSearch[\s\S]{0,1200}return s\.Search\(' internal/infrastructure/qdrant/searcher.go
! rg -n '^\s*(LocalPath|DriveLink)\s+string' internal/application/assets/search/ports.go
! rg -n '/api/internal/v1/deliver' internal/infrastructure/delivery
# Aggiungere un gate SSOT che impedisca la convivenza status/lifecycle_state.
go test ./internal/api/... ./internal/application/mediasearch/... ./internal/infrastructure/qdrant/... ./internal/infrastructure/delivery/... -count=1
```

## NON CHIUDERE SE

- il client supporta RRF ma il service non gli passa il canale sparse;
- HybridSearch può ancora chiamare ANN;
- payload e filtro lifecycle usano chiavi diverse;
- SQL filtra soltanto workspace ma non gli stati ricercabili;
- la route è provata soltanto con Router configurato manualmente;
- il receiver delivery è ancora “futuro” o solo documentato.
