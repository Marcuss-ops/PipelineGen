# QDRANT-001 — sidecar envelope, point ID canonico e search contract

> **Stato:** `BLOCKED / NON CHIUDIBILE`  
> **Audit baseline:** `main@e20d5e7fc4afd9f446d9d9e92703db639008b37f` — 26 giugno 2026  
> **Tipo verifica:** audit statico del codice; nessuna esecuzione CI associata all'HEAD.

## OBIETTIVO

Un solo contratto per embedding e ricerca:

- sidecar con `embedding`, `dimensions`, `model`, `model_version`;
- una sola trasformazione `AssetID -> QdrantPointID`;
- nessun locator server-internal nei DTO o payload di ricerca;
- workspace e lifecycle applicati anche durante l'hydration SQLite;
- cleanup automatico dei punti creati dal contratto precedente.

## COMPLETATO

- `AssetIDToQdrantPointID` genera UUID v5 deterministici ed è usato dal mapper.
- `BuildPayload` non emette più `drive_link` o `local_path`.
- `workspace_id` viene propagato nell'hydration SQLite.
- la configurazione Qdrant non duplica più nomi e dimensioni posseduti da `IndexSchema`.

## LEGACY ANCORA PRESENTE

1. `scripts/services/embedding_server/visual.py` restituisce soltanto embedding e dimensioni in `/embed_visual`, `/embed_visual_from_image` e `/visual_analyze`.
2. `scripts/services/embedding_server/audio.py` restituisce soltanto embedding e dimensioni in `/embed_audio` e `/embed_audio_from_file`.
3. `internal/application/assets/search/ports.go::VectorSearchResult` espone ancora `LocalPath` e `DriveLink`.
4. Le collection già popolate possono conservare payload `drive_link` o `local_path`; non esiste ancora un cleaner/reconciler operativo.
5. `payload_mapper.go` contiene ancora commenti che descrivono una trasformazione simmetrica `PointIDToAssetID`, nonostante UUID v5 sia one-way e il reverse helper sia stato rimosso.
6. `search_adapter.go` conserva commenti contraddittori che dichiarano i locator rimossi dal DTO mentre i campi sono ancora presenti.
7. Il gate QDRANT-001 è documentato ma non viene eseguito da `scripts/ci-architectural-checks.sh`.

## TASK RESIDUI

### A. Envelope sidecar canonico

Ogni endpoint embedding positivo deve restituire:

```json
{
  "embedding": [0.0],
  "dimensions": 768,
  "model": "siglip",
  "model_version": "2026-06-16-v1"
}
```

Il consumer Go deve rifiutare envelope incompleti, dimensioni incoerenti e versioni incompatibili con `IndexSchema`.

### B. Eliminare i locator dal contratto applicativo

Rimuovere `LocalPath` e `DriveLink` da `VectorSearchResult`, mapper, fixture, mock e consumer. L'accesso ai byte deve passare da `asset_id` e delivery URL firmata.

### C. Ripulire i punti legacy

Il reconciler QDRANT-005 deve individuare e riscrivere i punti contenenti locator legacy, con dry-run, metriche e test idempotente.

### D. Ripulire commenti e documentazione obsoleti

Eliminare ogni riferimento a reverse mapping UUID e ogni dichiarazione di chiusura non coerente con il DTO reale.

## LEGACY DA ELIMINARE

| Legacy | Dove | Azione |
|---|---|---|
| envelope senza modello/versione | embedding sidecar visual/audio | aggiungere envelope e validazione consumer |
| `VectorSearchResult.LocalPath` | application search DTO | eliminare campo e consumer |
| `VectorSearchResult.DriveLink` | application search DTO | eliminare campo e consumer |
| payload storici con locator | collection Qdrant | repair/rebuild tramite reconciler |
| commenti `PointIDToAssetID` | payload mapper | correggere documentazione |
| commenti DTO contraddittori | search adapter | allineare commenti e codice |
| gate solo Markdown | CI | rendere il controllo obbligatorio |

## DEFINITION OF DONE

Il ticket può essere marcato `CLOSED` soltanto quando:

- tutti gli endpoint sidecar restituiscono modello e versione reali;
- il consumer verifica il contratto rispetto al manifest;
- nessun DTO di search espone locator;
- nessun payload nuovo contiene locator;
- i payload storici sono rilevati e ripuliti da un job idempotente;
- commenti e runbook descrivono soltanto il percorso reale;
- il gate automatico fallisce alla reintroduzione di ogni legacy.

## GATE MINIMO

```bash
set -euo pipefail

test "$(rg -n --glob '!**/*_test.go' 'func AssetIDToQdrantPointID\(' internal/infrastructure/qdrant | wc -l)" -eq 1
! rg -n '^\s*(LocalPath|DriveLink)\s+string' internal/application/assets/search/ports.go
! rg -n 'PointIDToAssetID' internal/infrastructure/qdrant
rg -n '"model"' scripts/services/embedding_server/visual.py scripts/services/embedding_server/audio.py
rg -n '"model_version"' scripts/services/embedding_server/visual.py scripts/services/embedding_server/audio.py
go test ./internal/infrastructure/qdrant/... ./internal/application/mediasearch/...
```

## NON CHIUDERE SE

- modello/versione sono inventati o scollegati dal modello caricato;
- anche un solo locator resta nel DTO;
- il cleanup dei punti storici è soltanto descritto;
- il gate non è eseguito dalla CI.
