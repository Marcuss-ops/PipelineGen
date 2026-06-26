# ANNULLATO — SUPERATO DA PG-034 (June 2026)

# Surrogate per la search ibrida soppressa: il backend di ricerca canonico
# non-Qdrant è `internal/application/assets/search/service.go::Search` (ricerca
# cross-provider SQLite-only). Le chiamate legacy dirette alle route Qdrant
# (POST /recommend, GET ?mode=semantic) ricevono `errSemanticSearchRemoved`
# come sentinella "fail loud" — PG-034 ha rimosso integralmente la capability,
# quindi non esiste alcuna delega ibrida reale da implementare.

# QDRANT-004 — Search API, hybrid retrieval, filtri e delivery sicura

## Stato

BLOCCATO da QDRANT-001, QDRANT-002 e QDRANT-003.

## Obiettivo

Esporre una sola API privata e professionale per la ricerca media. Il backend Go deve trasformare la query in embedding, interrogare Qdrant, applicare filtri e ranking, idratare i risultati da SQLite e restituire URL autorizzati. I client non devono conoscere collection, vector name, path locali o dettagli del provider embedding.

Flusso finale:

```text
client Python/C++/frontend
        ↓
POST /internal/v1/media/search
        ↓
application MediaSearchService
        ├─ policy e filtri
        ├─ embedding query
        ├─ VectorIndex.Search
        ├─ hydration SQLite
        └─ delivery URL autorizzato
```

## Problemi attuali da eliminare

- I client possono interrogare direttamente Qdrant.
- `SearchResult` espone `local_path` e Drive link come dati operativi.
- Il metodo chiamato `HybridSearch` combina soltanto ricerche dense e non usa realmente il sparse vector dichiarato.
- `SparseVectorName` e `QueryText` sono presenti nel contratto ma non governano una query sparse reale.
- I filtri sono limitati a source, category, media type e language.
- Non è garantito un filtro `workspace_id`/tenant su ogni query.
- Metadata in Qdrant possono essere considerati erroneamente fonte canonica.
- Non esiste una spiegazione stabile del perché un risultato è stato scelto.
- Non esiste un contratto unico per text, transcript e visual search.
- Limiti, score threshold e ranking policy possono essere decisi direttamente dal chiamante.

## Decisioni architetturali

1. Usare `POST /internal/v1/media/search`, non query GET crescente e non versionata.
2. L'API accetta una query semantica e filtri applicativi, non vector raw per i client normali.
3. Solo application decide vector channels, pesi, top-K interno e soglie.
4. Qdrant restituisce ID e score; SQLite idrata metadata canonici.
5. Non restituire path locali a client remoti.
6. Restituire un `delivery_url` protetto o un asset reference.
7. `workspace_id` deve essere applicato dal contesto autenticato, non fidandosi del payload.
8. La hybrid search deve usare realmente dense + sparse oppure essere rinominata finché non lo fa.
9. I canali non disponibili vengono rifiutati o ignorati secondo policy esplicita, mai simulati.
10. Nessun handler importa il client Qdrant concreto.

## Contratto API proposto

```http
POST /internal/v1/media/search
Authorization: Bearer <service-token>
Content-Type: application/json
```

```json
{
  "query": "computer potente acceso durante la notte",
  "channels": ["text", "visual"],
  "limit": 10,
  "filters": {
    "status": ["ACTIVE"],
    "media_type": ["video"],
    "language": ["it", "en"],
    "category": ["technology"],
    "duration_ms": {
      "min": 3000,
      "max": 30000
    },
    "created_at": {
      "from": "2025-01-01T00:00:00Z"
    }
  }
}
```

Risposta:

```json
{
  "ok": true,
  "query": {
    "normalized": "computer potente acceso durante la notte",
    "channels_used": ["text"],
    "index_version": "v3"
  },
  "results": [
    {
      "asset_id": "asset-id",
      "score": 0.87,
      "matched_channels": ["text"],
      "reason": "semantic_text_match",
      "name": "Gaming PC at night",
      "media_type": "video",
      "duration_ms": 12000,
      "delivery_url": "/internal/v1/media/assets/asset-id/content"
    }
  ],
  "request_id": "request-id"
}
```

## Porte application

Riutilizzare contratti esistenti se equivalenti.

```go
type MediaSearchService interface {
    Search(ctx context.Context, query MediaSearchQuery) (MediaSearchPage, error)
}

type VectorIndex interface {
    Search(ctx context.Context, query VectorSearchQuery) ([]VectorHit, error)
}

type MediaReadRepository interface {
    GetMany(ctx context.Context, assetIDs []string) ([]MediaAsset, error)
}

type AssetDeliveryService interface {
    BuildAuthorizedURL(ctx context.Context, asset MediaAsset) (string, error)
}
```

## Scope consentito

- domain/application media search
- API media search e DTO
- Qdrant search adapter
- embedding query adapter
- SQLite read repository
- delivery URL service
- composition root
- auth scope e audit log
- test unitari/integration/search quality

## Fuori scope

- Reconciler e cleanup: QDRANT-005.
- Nuovo frontend.
- Addestramento di nuovi modelli.
- Esposizione pubblica internet dell'endpoint.
- Ricerca vector raw per utenti esterni.

## Sequenza operativa A–Z

### A. Preparazione

- [ ] Sincronizzare `main`.
- [ ] Confermare i ticket precedenti completati.
- [ ] Inventariare tutti i consumer Qdrant search esistenti.
- [ ] Inventariare route semantic/search già presenti.
- [ ] Cercare chiamate Python dirette a `/points/search`.
- [ ] Identificare eventuale application service già canonico.

### B. DTO e validazione API

- [ ] Richiedere query non vuota.
- [ ] Imporre lunghezza massima query.
- [ ] Imporre `limit` massimo configurato server-side.
- [ ] Validare channel contro capability reali.
- [ ] Validare range durata e date.
- [ ] Rifiutare filtri sconosciuti se il decoder è strict.
- [ ] Non accettare `collection`, `vector_name`, URL provider o API key dal client.
- [ ] Non accettare `workspace_id` libero quando deriva dall'auth context.

### C. Autenticazione e tenancy

- [ ] Montare l'endpoint sotto `/internal/v1`.
- [ ] Usare middleware service/worker auth esistente.
- [ ] Estrarre workspace/tenant dal contesto verificato.
- [ ] Applicare sempre il filtro workspace nella query Qdrant.
- [ ] Applicare workspace anche durante hydration SQLite.
- [ ] Testare che un token workspace A non recuperi asset workspace B.
- [ ] Audit log per query, caller, workspace e numero risultati.
- [ ] Non loggare integralmente query sensibili senza policy.

### D. Normalizzazione query

- [ ] Trim e normalizzazione Unicode.
- [ ] Applicare preprocessing richiesto dal modello.
- [ ] Separare prefisso query dal prefisso document.
- [ ] Registrare model/version usato.
- [ ] Timeout e cancellation.
- [ ] Cache embedding query opzionale con chiave modello+versione+testo normalizzato.
- [ ] Non usare fallback vector casuali o zero vector.

### E. Channel resolver

- [ ] Un solo resolver per channel → embedding spec → vector name.
- [ ] Verificare capability runtime.
- [ ] Se `visual` è richiesto ma non implementato, restituire errore tipizzato o usare policy documentata.
- [ ] Non simulare visual search con text vector.
- [ ] Non duplicare mapping in API, application e infrastructure.

### F. Dense search

- [ ] Usare alias runtime canonico.
- [ ] Usare vector name dal manifest QDRANT-003.
- [ ] Usare score threshold configurato per channel.
- [ ] Applicare top-K interno maggiore del limit finale per consentire fusione/hydration.
- [ ] Non esporre direttamente il payload Qdrant come risposta API.

### G. Sparse search reale

- [ ] Verificare supporto Qdrant installato per sparse query.
- [ ] Implementare sparse encoder reale o BM25 pipeline definita.
- [ ] Scrivere sparse vector durante indexing.
- [ ] Interrogare `bm25_text` realmente.
- [ ] Eliminare campi sparse non usati se la capability non viene implementata.
- [ ] Non chiamare `HybridSearch` un metodo che non usa sparse retrieval.

### H. Fusion e ranking

Scelta consigliata iniziale: Reciprocal Rank Fusion con parametri server-side.

- [ ] Definire pesi/canali in config o registry canonico.
- [ ] Deduplicare per `asset_id`.
- [ ] Conservare score per channel per diagnostics.
- [ ] Calcolare score finale deterministico.
- [ ] Applicare tie-break stabile.
- [ ] Documentare la formula.
- [ ] Non permettere al client di impostare pesi arbitrari salvo endpoint admin dedicato.

### I. Filtri

Supportare almeno:

```text
workspace_id
status
source
media_type
language
category
style
channel_id
license
index_version
embedding_version
duration_ms range
created_at range
updated_at range
```

- [ ] Costruire filtro Qdrant con must/must_not/range.
- [ ] Verificare payload indexes.
- [ ] Applicare default `status=ACTIVE`.
- [ ] Escludere `DELETED` e `DELETE_PENDING`.
- [ ] Testare combinazioni multiple.
- [ ] Vietare full scan involontari per filtri ad alta cardinalità non indicizzati.

### J. Hydration SQLite

- [ ] Estrarre gli asset ID ordinati da Qdrant.
- [ ] Caricare tutti gli asset in una query batch, non N+1.
- [ ] Eliminare risultati che non esistono o non sono accessibili.
- [ ] Conservare l'ordine di ranking.
- [ ] Considerare il missing asset una metrica di inconsistenza.
- [ ] Non sostituire metadata SQLite con payload Qdrant obsoleto.

### K. Delivery sicura

- [ ] Non restituire `local_path` ai client remoti.
- [ ] Non restituire token Drive.
- [ ] Restituire asset ID e URL autorizzato.
- [ ] URL con scadenza o endpoint streaming protetto.
- [ ] Verificare workspace e permessi al download.
- [ ] Supportare range requests per video se il DataServer serve contenuti.
- [ ] Loggare accesso asset senza secret.

### L. Error envelope

Usare envelope comune:

```json
{
  "ok": false,
  "error": {
    "code": "VECTOR_SEARCH_UNAVAILABLE",
    "message": "Semantic search is temporarily unavailable",
    "retryable": true
  },
  "request_id": "request-id"
}
```

- [ ] Errori validazione non retryable.
- [ ] Timeout embedding/Qdrant retryable secondo policy.
- [ ] Schema mismatch non retryable e readiness false.
- [ ] Non esporre body Qdrant raw.

### M. Pagination

- [ ] Evitare offset profondo non sostenibile.
- [ ] Preferire cursor/search-after se necessario.
- [ ] Firmare il cursor o renderlo opaco.
- [ ] Includere index version nel cursor per evitare risultati incoerenti dopo alias switch.
- [ ] Limitare il numero massimo di pagine/result window.

### N. Cache

- [ ] Cache query embedding separata dai risultati.
- [ ] Chiave include model version e normalized query.
- [ ] TTL configurato.
- [ ] Non cache cross-workspace senza includere workspace/filtri.
- [ ] Invalidare result cache dopo index alias switch.
- [ ] Cache opzionale: nessun requisito per il primo completamento se non già presente.

### O. Search quality tests

Creare un golden set versionato, privo di dati sensibili:

```text
query
expected_asset_ids
required_filters
minimum_recall
```

- [ ] Test text search.
- [ ] Test transcript search.
- [ ] Test sparse search.
- [ ] Test hybrid fusion.
- [ ] Test language filter.
- [ ] Test duration filter.
- [ ] Test archived/deleted exclusion.
- [ ] Test cross-tenant isolation.
- [ ] Test deterministic ordering.
- [ ] Non usare come test visual una query con lo stesso vector dell'asset target: non misura qualità reale.

### P. Performance tests

- [ ] Misurare embedding latency.
- [ ] Misurare Qdrant search latency.
- [ ] Misurare hydration latency.
- [ ] P50/P95/P99 per endpoint.
- [ ] Verificare niente N+1.
- [ ] Test concorrenza con limiti realistici CPU-only.
- [ ] Imporre timeout globale request.

### Q. Metriche

- [ ] `media_search_requests_total`.
- [ ] `media_search_failures_total{code}`.
- [ ] `media_search_duration_seconds`.
- [ ] `media_search_embedding_duration_seconds`.
- [ ] `media_search_qdrant_duration_seconds`.
- [ ] `media_search_hydration_duration_seconds`.
- [ ] `media_search_results_total`.
- [ ] `media_search_missing_assets_total`.
- [ ] Channel e index version come label a cardinalità controllata.

### R. Rimozione legacy

- [ ] Eliminare search diretta Python verso Qdrant.
- [ ] Eliminare endpoint duplicati semantic search.
- [ ] Eliminare response con `local_path` come contratto principale.
- [ ] Eliminare mapping vector name dai client.
- [ ] Eliminare `HybridSearch` finto o renderlo realmente hybrid.
- [ ] Eliminare filtri duplicati implementati in più layer.
- [ ] Eliminare payload hydration direttamente da Qdrant.
- [ ] Non lasciare route legacy alias.

### S. Test API e integrazione

- [ ] Senza token → 401/403.
- [ ] Payload invalido → 400.
- [ ] Channel unsupported → errore tipizzato.
- [ ] Query valida → risultati idratati.
- [ ] Qdrant down → 503 retryable.
- [ ] Embedding down → 503 retryable.
- [ ] Asset missing SQLite → escluso e metrica incrementata.
- [ ] Workspace isolation.
- [ ] Delivery URL access control.
- [ ] Qdrant container reale con dense+sparse.

### T. Validazione finale

- [ ] `gofmt`.
- [ ] Test domain/application/API mirati.
- [ ] Test Qdrant search mirati.
- [ ] Golden set.
- [ ] Integration test Qdrant reale.
- [ ] `go test ./...`.
- [ ] `go vet ./...`.
- [ ] `go build ./...`.
- [ ] archcheck ratchet.
- [ ] `git diff --check`.
- [ ] Rebase `origin/main`.
- [ ] Commit mirato.
- [ ] Push su `main`.
- [ ] Verifica commit remoto.

## Stop conditions

Fermarsi se:

- QDRANT-003 non ha prodotto un alias/schema stabile;
- sparse search viene dichiarata senza encoder/index reale;
- la soluzione richiede restituire path locali a worker remoti;
- non è possibile applicare workspace isolation;
- si propone un secondo search registry o un nuovo client Qdrant fuori dall'adapter canonico;
- un altro agente modifica API media o searcher Qdrant negli stessi file.

## Definition of Done

- [ ] Una sola API privata di ricerca.
- [ ] Client ignora Qdrant, collection e vector name.
- [ ] Dense+sparse realmente implementati oppure naming corretto senza falsa hybrid.
- [ ] Filtri completi e indicizzati.
- [ ] Workspace isolation testata.
- [ ] Risultati idratati da SQLite.
- [ ] Nessun path locale o secret esposto.
- [ ] Delivery URL protetto.
- [ ] Golden set e performance baseline presenti.
- [ ] Route/search legacy eliminate.
- [ ] Test, vet, build e gate passano.

## Dipendenze

- Richiede QDRANT-001, QDRANT-002 e QDRANT-003.
- Deve precedere QDRANT-005 per permettere smoke test e metriche finali.
