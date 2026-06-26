# QDRANT-004 — media-search privato, hybrid reale e delivery firmata

> **Stato:** `BLOCKED / DA RIAPRIRE`  
> **Audit baseline:** `main@c72949a362656f05222f333adf67b1b0eee973ae` — 26 giugno 2026  
> **Owner suggerito:** mediasearch application + Qdrant search adapter + API internal routing  
> **Branch suggerito:** `codex/qdrant-004-hybrid-e2e`

## OBIETTIVO

Esporre una sola API privata di ricerca media che garantisca:

- autenticazione worker;
- workspace isolation in Qdrant e SQLite;
- modalità `hybrid` realmente dense + sparse con RRF;
- nessun fallback ANN etichettato come hybrid;
- hydration esclusivamente da SQLite;
- delivery URL firmata, non vuota e temporanea;
- nessun locator server-internal nella risposta.

## STATO REALE

### Completato

- `Client.HybridSearchPoints` usa Qdrant `/points/query` con prefetch dense/sparse e fusione RRF;
- il client rifiuta sparse vector o sparse channel mancanti;
- il filtro Qdrant include `workspace_id` e lifecycle consentiti;
- l'hydration SQLite applica `workspace_id` per utenti non admin;
- il service rifiuta workspace vuoto e il sentinel `default`;
- `qdrant.SearchResult` non espone `LocalPath` o `DriveLink`;
- il delivery signer fallisce su secret mancante/corto e usa `/internal/v1/deliver`;
- il module registry pubblico non registra più mediasearch.

### Blocker attuali

1. **Route production non montata.** Il server chiama `Router.Setup()` prima di `SetMediasearchHandler`; il setter successivo non registra route nel motore Gin già costruito.
2. **Richiesta hybrid senza sparse channel.** `mediasearch.Service` costruisce `search.HybridSearchRequest` senza impostare `SparseVectorName`.
3. **Fallback ANN nel livello Searcher.** `Searcher.HybridSearch` esegue `Search()` quando `SparseQueryVector` è nil; il chiamante può continuare a presentare il risultato come hybrid.
4. **Test E2E insufficiente.** Non esiste una prova production che attraversi HTTP -> auth -> embed -> sparse tokenize -> `/points/query` -> hydration workspace -> signed URL.
5. **DTO applicativo con locator legacy.** `search.VectorSearchResult` mantiene ancora `LocalPath` e `DriveLink`, anche se l'adapter Qdrant non li popola.
6. **Delivery endpoint da provare end-to-end.** Il signer genera il path canonico, ma la chiusura richiede una prova che URL, firma, workspace, expiry e tamper rejection funzionino insieme al route wiring reale.

## TASK DI HANDOFF

### A. Rendere il percorso hybrid fail-closed

Il nome sparse deve provenire dal manifest/config adapter canonico, non da una stringa duplicata nel service.

Aggiornare il contratto `VectorConfig`/`ConfigPort` affinché esponga il canale sparse attivo, quindi valorizzare:

```go
search.HybridSearchRequest{
    SparseVectorName: vectorCfg.SparseVectorName,
}
```

Regole:

- `mode=hybrid` + sparse channel assente -> errore tipizzato `ErrSparseRequired` o capability unavailable;
- nessun fallback a `Search()` dentro `Searcher.HybridSearch`;
- ANN deve essere selezionata soltanto con `mode=ann`;
- la risposta deve riportare il mode realmente eseguito.

### B. Correggere il route wiring production

Dipendenza condivisa con QDRANT-002:

- passare `MediasearchHandler` al router prima di `Setup()`;
- rimuovere il setter post-Setup o renderlo impossibile da usare per il mount;
- aggiungere test tramite il costruttore production del server;
- verificare presenza di `POST /internal/v1/media/search` e assenza di `/api/internal/v1/media/search`.

### C. Testare la query Qdrant reale

Con un server Qdrant fake/httptest, verificare il body inviato a `/points/query`:

- almeno un prefetch dense;
- un prefetch sparse;
- `fusion: rrf`;
- stesso filtro workspace/lifecycle su entrambi;
- vector names provenienti dal manifest;
- nessuna chiamata legacy `/points/search` in modalità hybrid.

### D. Testare workspace e hydration

A parità di asset ID:

- un workspace non deve poter idratare righe di un altro workspace;
- admin bypass deve essere esplicito e testato;
- deleted/archived/non-searchable devono essere esclusi;
- hydration gap non deve far apparire un record di tenant diverso.

### E. Testare delivery firmata

Verificare:

- URL non vuota per ogni hit restituito;
- firma include workspace e asset ID;
- firma alterata -> rifiuto;
- workspace alterato -> rifiuto;
- asset ID alterato -> rifiuto;
- URL scaduta -> rifiuto;
- secret mancante -> startup error o capability non montata, mai risposta parziale.

### F. Eliminare locator dal DTO applicativo

Rimuovere `LocalPath` e `DriveLink` da `internal/application/assets/search/ports.go::VectorSearchResult` e aggiornare mock/consumer. Questa attività può essere eseguita insieme a QDRANT-001, ma QDRANT-004 non è chiudibile finché il contratto media-search li conserva.

## LEGACY DA ELIMINARE

| Legacy | Dove | Azione richiesta |
|---|---|---|
| `SetMediasearchHandler` dopo `Router.Setup()` | `cmd/server/main.go`, `internal/api/server.go` | wiring pre-Setup |
| fallback ANN da `HybridSearch` | `internal/infrastructure/qdrant/searcher.go` | restituire errore tipizzato |
| hybrid request senza `SparseVectorName` | `internal/application/mediasearch/service.go` | propagare SSOT dal manifest |
| locator nel DTO applicativo | `internal/application/assets/search/ports.go` | eliminare campi e consumer |
| test Router manuale che non copre server production | `internal/api/routes_test.go` | aggiungere E2E production constructor |
| smoke delivery non automatizzato | test API/delivery | introdurre roundtrip completo |
| gate QDRANT soltanto nel Markdown | CI | promuovere a check obbligatorio |

## DEFINITION OF DONE

Il ticket può essere marcato `CLOSED` soltanto quando:

- `POST /internal/v1/media/search` è realmente registrato nel server production prima dell'avvio;
- la route richiede worker auth;
- `mode=hybrid` produce sempre dense+sparse+RRF oppure fallisce esplicitamente;
- nessun codice effettua fallback ANN mantenendo l'etichetta hybrid;
- workspace e lifecycle sono applicati in Qdrant e SQLite;
- la risposta non contiene path locali o Drive link grezzi;
- ogni hit restituito ha una delivery URL firmata valida;
- test tamper/expiry/workspace mismatch sono verdi;
- un test verifica il body `/points/query` e vieta `/points/search` in hybrid;
- il gate automatico previene regressioni di route, fallback e locator.

## GATE ANTI-REGRESSIONE

```bash
set -euo pipefail

# Nessun fallback ANN nel metodo hybrid.
! rg -n -U 'func \(s \*Searcher\) HybridSearch[\s\S]{0,1200}return s\.Search\(' \
  internal/infrastructure/qdrant/searcher.go

# Il service deve propagare il canale sparse.
rg -n 'SparseVectorName:' internal/application/mediasearch/service.go

# Il client hybrid usa Query API + RRF.
rg -n 'points/query' internal/infrastructure/qdrant/client.go
rg -n 'fusion.*rrf|"rrf"' internal/infrastructure/qdrant/client.go

# Nessun locator nel DTO applicativo.
! rg -n '^\s*(LocalPath|DriveLink)\s+string' \
  internal/application/assets/search/ports.go

# Route production e test API.
go test ./internal/api/... ./internal/app/... \
  ./internal/application/mediasearch/... \
  ./internal/infrastructure/qdrant/... \
  ./internal/infrastructure/delivery/... \
  -count=1
```

## TEST MINIMI DA AGGIUNGERE

- `mode=hybrid` con sparse disponibile -> `/points/query` RRF;
- `mode=hybrid` senza sparse -> errore, zero query ANN;
- `mode=ann` -> ANN esplicita;
- worker token mancante/errato;
- workspace mancante, `default` e cross-tenant;
- lifecycle deleted/archived;
- signer secret mancante/corto;
- URL firmata roundtrip, tamper ed expiry;
- route presente nel server production;
- `/api/internal/v1/media/search` assente.

## NON CHIUDERE SE

- il client Qdrant supporta RRF ma il service non gli passa il canale sparse;
- un fallback dense-only è ancora raggiungibile in modalità hybrid;
- la route esiste soltanto in un test che configura direttamente `Router`;
- una hit può essere restituita senza delivery URL autorizzata;
- `LocalPath` o `DriveLink` restano nel contratto di search.
