# QDRANT-001 — sidecar envelope, canonical point ID e search contract

> **Stato:** `PARTIAL / DA RIAPRIRE`  
> **Audit baseline:** `main@c72949a362656f05222f333adf67b1b0eee973ae` — 26 giugno 2026  
> **Owner suggerito:** embedding sidecar + `internal/infrastructure/qdrant/` + application search DTO  
> **Branch suggerito:** `codex/qdrant-001-sidecar-contract`

## OBIETTIVO

Avere un solo contratto operativo per l'indicizzazione multicanale:

- il sidecar restituisce embedding, dimensioni, modello e versione modello;
- `AssetID -> QdrantPointID` passa da una sola funzione canonica;
- Qdrant contiene soltanto dati necessari a ricerca, ranking e hydration;
- nessun path locale o link Drive attraversa il contratto di ricerca;
- workspace e lifecycle sono applicati anche durante l'hydration SQLite.

## STATO REALE

### Completato

- `AssetIDToQdrantPointID` esiste in un'unica implementazione e genera UUID v5 deterministici.
- `PayloadMapper.AssetToPoint` usa la funzione canonica invece di assegnare direttamente `asset.ID`.
- `workspace_id` viene propagato dal media-search adapter a `asset.Filter` e applicato in SQL.
- `qdrant.SearchResult` non espone più `LocalPath` o `DriveLink`.
- `BuildPayload` non scrive più `drive_link` o path filesystem nel payload Qdrant.
- la configurazione Qdrant non duplica più nomi e dimensioni dei canali posseduti da `IndexSchema`.
- i writer Python storici non scrivono direttamente nel database SQLite.

### Da finire

1. **Envelope sidecar incompleto.** Gli endpoint visual e audio importano già le costanti del modello, ma restituiscono ancora soltanto `embedding` e `dimensions`.
2. **DTO applicativo legacy.** `internal/application/assets/search/ports.go::VectorSearchResult` conserva `LocalPath` e `DriveLink`.
3. **Payload legacy già indicizzati.** Vecchi punti Qdrant possono contenere ancora `drive_link`; il decoder non lo usa più, ma il dato deve essere rimosso mediante rebuild o reconciler.
4. **Gate CI non obbligatorio.** I controlli descritti in questo ticket non sono ancora eseguiti dal wrapper CI principale.

## TASK DI HANDOFF

### A. Rendere canonico l'envelope del sidecar

Aggiornare almeno:

- `scripts/services/embedding_server/visual.py`
- `scripts/services/embedding_server/audio.py`
- gli altri endpoint embedding che usano un envelope equivalente
- DTO/client Go che deserializzano le risposte
- test Python e Go del contratto

Ogni risposta positiva deve avere questa forma minima:

```json
{
  "embedding": [0.0],
  "dimensions": 768,
  "model": "siglip",
  "model_version": "<versione-canonica>"
}
```

Regole:

- `model` e `model_version` non possono essere vuoti;
- `dimensions` deve coincidere con la lunghezza reale del vettore;
- il consumer Go deve rifiutare una versione non compatibile con lo schema attivo;
- nessun fallback deve inventare una versione mancante.

### B. Rimuovere i locator dal DTO applicativo

Eliminare `LocalPath` e `DriveLink` da:

- `internal/application/assets/search/ports.go::VectorSearchResult`
- mapper, mock, fixture e consumer associati

L'accesso ai byte deve avvenire tramite `asset_id` e delivery URL firmato.

### C. Ripulire i punti legacy

Integrare nel rebuild/reconciler QDRANT-005 una verifica che:

- individui payload contenenti `drive_link` o `local_path`;
- riscriva o ricrei il punto con il payload canonico;
- produca un conteggio osservabile dei punti ripuliti.

## LEGACY DA ELIMINARE

| Legacy | Dove | Azione richiesta |
|---|---|---|
| risposta sidecar senza `model` / `model_version` | `scripts/services/embedding_server/{visual,audio}.py` e endpoint equivalenti | introdurre envelope canonico e validazione consumer |
| `VectorSearchResult.LocalPath` | `internal/application/assets/search/ports.go` | eliminare campo e consumer |
| `VectorSearchResult.DriveLink` | `internal/application/assets/search/ports.go` | eliminare campo e consumer |
| payload Qdrant storici con `drive_link` | collection precedenti | rebuild o repair tramite reconciler |
| gate documentati ma non eseguiti in CI | `scripts/ci-architectural-checks.sh` | aggiungere gate automatici |

## DEFINITION OF DONE

Il ticket può essere marcato `CLOSED` soltanto quando:

- tutti gli endpoint embedding restituiscono modello e versione reali;
- il consumer rifiuta envelope incompleti o incompatibili;
- esiste una sola dichiarazione di `AssetIDToQdrantPointID`;
- nessun DTO di ricerca espone path locali o link Drive;
- workspace isolation è testata sia nel filtro Qdrant sia nell'hydration SQLite;
- un test dimostra che un punto legacy viene rilevato e ripulito;
- il gate automatico fallisce se una delle legacy viene reintrodotta.

## GATE ANTI-REGRESSIONE

```bash
set -euo pipefail

# Point-ID SSOT: una sola dichiarazione produttiva.
test "$(rg -n --glob '!**/*_test.go' \
  'func AssetIDToQdrantPointID\(' internal/infrastructure/qdrant | wc -l)" -eq 1

# Vietati locator nel DTO applicativo e nel DTO Qdrant.
! rg -n '^\s*(LocalPath|DriveLink)\s+string' \
  internal/application/assets/search/ports.go \
  internal/infrastructure/qdrant/types.go

# Vietata emissione dei locator nel payload Qdrant.
! rg -n 'payload\["(local_path|drive_link)"\]' \
  internal/infrastructure/qdrant

# Gli endpoint embedding devono emettere identificazione modello.
rg -n '"model"|"model_version"' \
  scripts/services/embedding_server/visual.py \
  scripts/services/embedding_server/audio.py

# Build e test mirati.
go test ./internal/infrastructure/qdrant/... \
  ./internal/application/mediasearch/... \
  ./internal/infrastructure/database/sqlite/assets/...
```

## NON CHIUDERE SE

- `model` o `model_version` sono valori hard-coded non collegati al modello realmente caricato;
- `LocalPath` o `DriveLink` restano disponibili nel contratto di search;
- la pulizia dei payload legacy è solo descritta ma non eseguibile;
- il gate vive soltanto in questo Markdown e non nella CI.
