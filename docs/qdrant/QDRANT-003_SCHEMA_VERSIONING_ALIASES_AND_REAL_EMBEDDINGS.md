# ANNULLATO — SUPERATO DA PG-034 (June 2026)

PG-034 ha rimosso integralmente la capability Qdrant. I ticket QDRANT-001..005
sono tombstonati (vedi commit series di questa chiusura). L'audit trail di
ciò che la capability doveva essere è preservato qui sotto come sola
traccia documentale; nessuna implementazione sarà mai prodotta perché
l'obiettivo del ticket è già risolto dall'assenza della capability.

# QDRANT-003 — Schema versionato, alias atomici ed embedding reali

## Stato

BLOCCATO da QDRANT-001 e QDRANT-002.

## Obiettivo

Trasformare la collection Qdrant da contenitore implicito e statico a indice versionato, validato e migrabile senza downtime. Eliminare vector visual/audio sintetici e rendere ogni embedding tracciabile per modello, dimensione e versione.

Struttura finale:

```text
collection fisica: media_assets_v3_e5_768_siglip_768
alias runtime:      media_assets_current
SQLite asset:       index_version=v3
payload Qdrant:     embedding_manifest versionato
```

## Problemi attuali da eliminare

- `EnsureCollection` considera valida qualsiasi collection esistente.
- Non vengono confrontati vector name, dimensioni, distance metric e sparse vector.
- I campi `CollectionVersion`, `CollectionAlias` e `DisableAlias` non governano realmente la migrazione.
- La creazione usa nomi vector configurabili, mentre l'upsert usa nomi hardcoded.
- L'audio embedding è dichiarato ma non sempre scritto.
- Lo script legacy genera visual/audio vector casuali deterministici senza significato semantico.
- Embedding model e versione non sono sufficientemente verificati.
- Una modifica della dimensione può rompere il runtime o richiedere cancellazione manuale.
- I payload index per i filtri non sono garantiti.

## Decisioni architetturali

1. Le collection fisiche sono immutabili rispetto allo schema dei vector.
2. Ogni breaking change crea una nuova collection fisica.
3. Il runtime legge e scrive tramite un alias canonico.
4. Lo switch alias avviene soltanto dopo reindex e verifica.
5. SQLite conserva la versione canonica dell'indice per asset.
6. Nessun vector viene scritto se non è prodotto da un modello reale e registrato.
7. Modello, dimensione, normalizzazione e versione fanno parte del contratto.
8. Non usare vector sintetici come placeholder.
9. Non eliminare la collection precedente finché il rollback window non è scaduto.

## Manifest embedding canonico

```go
type EmbeddingSpec struct {
    Channel       string
    Model         string
    ModelVersion  string
    Dimensions    int
    Distance      string
    Normalized    bool
    PreprocessVer string
}

type IndexSchema struct {
    Version        string
    PhysicalName   string
    RuntimeAlias   string
    DenseVectors   []EmbeddingSpec
    SparseVectors  []SparseSpec
    PayloadIndexes []PayloadIndexSpec
}
```

Il manifest deve essere definito in un solo package canonico, non duplicato tra config, script e adapter.

## Canali consentiti

### Text

- modello reale multilingua;
- dimensione verificata;
- prefisso query/document coerente con il modello;
- normalizzazione dichiarata.

### Transcript

- può usare lo stesso modello text, ma resta un vector name distinto;
- preprocessing e versioning separati;
- non copiare automaticamente il text vector se il transcript è assente.

### Visual

- CLIP, SigLIP o modello visual reale;
- input derivato da frame/keyframe reali;
- nessun hash pseudo-random.

### Audio

- CLAP o modello audio reale;
- opzionale fino alla disponibilità del servizio;
- nessun vector fake per mantenere la collection piena.

### Sparse

- BM25/SPLADE realmente prodotto e interrogato;
- non dichiarare il canale se il runtime non lo supporta.

## Scope consentito

- `internal/infrastructure/qdrant/**`
- config Qdrant/vector search
- embedding ports e adapter
- application indexing
- migration/reindex command
- composition root
- test Qdrant e embedding
- docker/testcontainer configuration
- documentazione schema

## Fuori scope

- API search pubblica completa: QDRANT-004.
- Reconciler e cleanup finale: QDRANT-005.
- Nuovo provider GPU obbligatorio.
- UI di amministrazione.
- Cancellazione immediata delle collection precedenti.

## Sequenza operativa A–Z

### A. Preparazione

- [ ] Sincronizzare `main`.
- [ ] Confermare QDRANT-001 e QDRANT-002 completati.
- [ ] Inventariare config, vector name, dimensioni e model version attuali.
- [ ] Cercare tutte le stringhe hardcoded `text`, `visual`, `audio`, `transcript`, `bm25_text`.
- [ ] Cercare ogni generazione di vector sintetico.

### B. Separazione del mega-file Qdrant

Dividere `internal/infrastructure/qdrant/service.go` senza cambiare comportamento nel primo commit logico del ticket:

```text
client.go
schema.go
collection_manager.go
index_writer.go
searcher.go
payload_mapper.go
errors.go
types.go
```

- [ ] `client.go`: HTTP, auth, timeout, retry hook.
- [ ] `schema.go`: manifest e validazione.
- [ ] `collection_manager.go`: create, inspect, alias, migration.
- [ ] `index_writer.go`: upsert/delete batch.
- [ ] `payload_mapper.go`: mapping configurato, non hardcoded.
- [ ] Non creare package paralleli.
- [ ] Mantenere un solo `Service` facade se serve per compatibilità interna temporanea; eliminarla entro fine ticket se diventa solo pass-through.

### C. Manifest unico

- [ ] Definire schema version esplicita.
- [ ] Definire physical collection name deterministico.
- [ ] Definire runtime alias.
- [ ] Definire dense vector attivi.
- [ ] Definire sparse vector attivi.
- [ ] Definire payload indexes.
- [ ] Validare nomi non vuoti e unici.
- [ ] Validare dimensioni positive.
- [ ] Validare distance supportata.
- [ ] Validare che alias e physical name siano distinti.

### D. Collection inspection reale

- [ ] Leggere schema collection da Qdrant.
- [ ] Confrontare vector names.
- [ ] Confrontare dimensioni.
- [ ] Confrontare distance metric.
- [ ] Confrontare sparse vectors.
- [ ] Confrontare payload indexes.
- [ ] Restituire report dettagliato delle differenze.
- [ ] Non restituire healthy se lo schema è incompatibile.

### E. EnsureSchema

- [ ] Se alias punta a collection compatibile: successo.
- [ ] Se alias manca ma esiste una sola collection compatibile: creare alias secondo policy esplicita.
- [ ] Se schema incompatibile: non mutare la collection esistente.
- [ ] Creare nuova physical collection versionata.
- [ ] Creare payload indexes.
- [ ] Non spostare alias prima del reindex.
- [ ] Fallire startup soltanto secondo capability policy già definita, senza skip silenziosi.

### F. Alias atomico

- [ ] Implementare lettura target alias.
- [ ] Implementare switch atomico tramite API Qdrant aliases.
- [ ] Registrare old target e new target.
- [ ] Supportare rollback esplicito.
- [ ] Vietare delete collection attiva.
- [ ] Vietare switch se verifica non è passata.

### G. Point mapping configurato

- [ ] Convertire `qdrantPointFromAsset` in metodo dipendente dal manifest.
- [ ] Usare vector name dal manifest.
- [ ] Includere audio soltanto quando presente e attivo.
- [ ] Verificare dimensione di ogni vector prima dell'HTTP call.
- [ ] Rifiutare NaN e Inf.
- [ ] Rifiutare vector vuoto per channel richiesto.
- [ ] Non saltare silenziosamente asset invalidi.
- [ ] Restituire errore tipizzato con asset ID e channel.

### H. Rimozione vector fake

- [ ] Eliminare `generate_normalized_vector` dallo script legacy.
- [ ] Eliminare visual/audio vector basati su hash.
- [ ] Cercare vector mock in produzione Go e Python.
- [ ] Se un channel non ha modello reale, marcarlo `UNAVAILABLE` o `PENDING` in SQLite.
- [ ] Non creare vector zero-filled.
- [ ] Non copiare text embedding nel visual/audio channel.

### I. Embedding service contract

```go
type EmbeddingService interface {
    EmbedText(ctx context.Context, inputs []TextInput) (EmbeddingBatch, error)
    EmbedImages(ctx context.Context, inputs []ImageInput) (EmbeddingBatch, error)
    EmbedAudio(ctx context.Context, inputs []AudioInput) (EmbeddingBatch, error)
    Describe(ctx context.Context) (EmbeddingCapabilities, error)
}
```

- [ ] Riutilizzare porte esistenti se equivalenti.
- [ ] Rendere le capability esplicite.
- [ ] Verificare che il modello restituito coincida con il manifest.
- [ ] Batchizzare le richieste.
- [ ] Supportare timeout e cancellation.
- [ ] Non passare URL modello nei payload job/API.

### J. Versionamento per asset

- [ ] Persistire `embedding_version` per channel o manifest completo.
- [ ] Persistire `search_text_version`.
- [ ] Persistire `index_version`.
- [ ] Considerare stale un asset con versione diversa dal target.
- [ ] Non sovrascrivere versioni senza registrare il completamento.

### K. Payload schema

Payload minimo consigliato:

```json
{
  "asset_id": "asset-id",
  "workspace_id": "workspace-id",
  "status": "ACTIVE",
  "source": "google_drive",
  "media_type": "video",
  "language": "it",
  "category": "technology",
  "duration_ms": 12000,
  "index_version": "v3",
  "embedding_version_text": "e5-v2",
  "embedding_version_visual": "siglip-v1",
  "source_version": "etag-or-hash",
  "updated_at": "RFC3339"
}
```

- [ ] Non usare Qdrant come unica copia di metadata complessi.
- [ ] Non salvare token o secret.
- [ ] Valutare di non salvare path fisici direttamente.
- [ ] Mantenere solo dati necessari a filtro, ranking e hydration.

### L. Payload indexes

Creare e verificare:

```text
workspace_id         keyword
status               keyword
source               keyword
media_type           keyword
language             keyword
category             keyword
style                keyword
channel_id           keyword
license               keyword
index_version        keyword
embedding_version_*  keyword
duration_ms          integer
created_at            datetime
updated_at            datetime
deleted_at            datetime
```

- [ ] Non ricreare index già compatibili.
- [ ] Segnalare index con tipo errato.
- [ ] Testare query filtrate.

### M. Reindex command/job

- [ ] Creare `media.index.rebuild_requested` o comando equivalente nel registry canonico.
- [ ] Supportare `dry_run`.
- [ ] Supportare subset per asset IDs o filtro.
- [ ] Indicizzare nella nuova physical collection.
- [ ] Salvare progress e failure per asset.
- [ ] Rendere il job resumable.
- [ ] Non bloccare il server HTTP.
- [ ] Non cancellare la vecchia collection.

### N. Verifica pre-switch

- [ ] Confrontare asset attesi e point count.
- [ ] Calcolare missing e orphan.
- [ ] Verificare dimensioni su campione e/o API schema.
- [ ] Eseguire query golden set.
- [ ] Verificare filtri payload.
- [ ] Verificare nessun errore dead-letter aperto.
- [ ] Produrre report firmato con target collection e versioni.

### O. Switch e rollback

- [ ] Switch alias soltanto dopo report OK.
- [ ] Registrare audit event.
- [ ] Eseguire smoke search dopo switch.
- [ ] Se smoke fallisce, rollback alias.
- [ ] Conservare la vecchia collection per retention configurata.
- [ ] Eliminazione vecchia collection soltanto tramite comando separato e conferma esplicita.

### P. Test

- [ ] Manifest validation.
- [ ] Collection compatibile.
- [ ] Collection incompatibile.
- [ ] Alias missing/existing.
- [ ] Switch alias atomico.
- [ ] Rollback.
- [ ] Vector dimension mismatch.
- [ ] NaN/Inf rejection.
- [ ] Channel non supportato.
- [ ] Payload index creation.
- [ ] Reindex resumable.
- [ ] Integration test con due collection e alias reale.

### Q. Rimozione legacy

- [ ] Eliminare nomi vector hardcoded dal mapper.
- [ ] Eliminare collection name hardcoded dagli script.
- [ ] Eliminare fake vector.
- [ ] Eliminare configurazioni dichiarate ma non usate.
- [ ] Eliminare `EnsureCollection` permissivo se sostituito da `EnsureSchema`.
- [ ] Eliminare schema paralleli in Python.
- [ ] Eliminare commenti che descrivono vector finti come reali.

### R. Validazione finale

- [ ] `gofmt`.
- [ ] Test Qdrant mirati.
- [ ] Test embedding mirati.
- [ ] Test integrazione con Qdrant reale.
- [ ] `go test ./...`.
- [ ] `go vet ./...`.
- [ ] `go build ./...`.
- [ ] archcheck ratchet.
- [ ] `git diff --check`.
- [ ] Rebase su `origin/main`.
- [ ] Commit mirato.
- [ ] Push su `main`.
- [ ] Verifica commit remoto.

## Stop conditions

Fermarsi se:

- non è possibile identificare il modello reale che ha prodotto i vector esistenti;
- un alias attuale punta a una collection con traffico non osservato;
- la reindicizzazione richiede risorse non disponibili e non esiste piano batch/resume;
- si propone di cancellare la collection attiva per correggere lo schema;
- un altro agente modifica gli stessi file Qdrant/config.

## Definition of Done

- [ ] Schema collection validato realmente.
- [ ] Collection fisiche versionate.
- [ ] Alias runtime atomico e rollback testato.
- [ ] Vector name e dimensioni provengono da un solo manifest.
- [ ] Nessun visual/audio vector fake resta.
- [ ] Ogni embedding è tracciato per modello e versione.
- [ ] Payload indexes presenti e verificati.
- [ ] Reindex senza downtime testato.
- [ ] Vecchia collection conservata per rollback.
- [ ] Test, vet, build e gate passano.

## Dipendenze

- Richiede QDRANT-001 e QDRANT-002.
- Deve precedere QDRANT-004 e QDRANT-005.
