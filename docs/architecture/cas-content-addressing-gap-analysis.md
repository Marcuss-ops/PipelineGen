# CAS + Global Dedup + Deterministic Artifact Cache — Gap Analysis

Riferimento: design proposto (agosto 2026) per Content-Addressed Storage,
global byte-deduplication e computation deduplication nel Control Plane.
Questo documento confronta il design con lo stato attuale del codebase e
indica, punto per punto, cosa esiste già, cosa manca e dove va implementato.

Stato del repo alla data del documento: `main` @ `9db47ad97`.

## Design di riferimento (sintesi)

1. **Prima regola** — La Primary Key di ogni immutable content object è il
   SHA-256 dei byte; gli asset logici del Media Registry referenziano tali
   content object tramite `content_sha256`.
2. **Seconda regola** — Ogni trasformazione deterministica è globalmente
   cacheable mediante una cache key derivata da
   `source content hash + transform specification + processor/model version`.

## Cosa esiste già (inventario)

### Storage content-addressed (parziale)

| Componente | Dove | Stato |
|---|---|---|
| `LocalBlobStore` | `internal/application/assets/artifacts/local_blob.go` | **ESISTE** — layout `blobs/sha256/XX/<hash>`, SHA-256 calcolato durante lo staging, rename atomico, verifica hash post-write |
| Port `BlobStore` | `internal/application/assets/artifacts/types.go` | **ESISTE** — `Stage` / `VerifyAndPromote` / `Open` / `Delete` / `Stat` |
| `artifacts` table | `migrations/sqlite/051_artifacts.sql` | **ESISTE** — `sha256` con `UNIQUE INDEX WHERE sha256 != ''`, `storage_key`, `verified_at`, stato STAGING→VERIFYING→READY |
| `assets` table | `migrations/sqlite/054_asset_registry.sql` | **ESISTE** — `sha256 TEXT NOT NULL UNIQUE` + `asset_sources` (provenance) |
| `ArtifactService.ResolveAndRegister` | `internal/application/assets/artifacts/service.go` | **ESISTE** — bufferizza il contenuto, calcola SHA-256, fa dedup `GetBySHA256` PRIMA di scrivere il blob, registra provenance (`ArtifactSource`) anche su dedup hit |
| Staging store | `internal/infrastructure/artifacts/local_store.go` | **ESISTE** — SHA-256 durante la write via `io.MultiWriter`, scritture atomiche, quota |
| Port `ArtifactStagingStore` | `internal/application/ports/artifact_staging_store.go` | **ESISTE** — porta tipizzata con `ContentHash` |

### Identità logica `media_assets`

- `media_assets` è l'identità logica canonica (migration `033`, estesa da
  `059`, `094`, `096`, `152`).
- `binary_sha256` (migration `152`): colonna canonica per l'hash binario,
  backfillata da `file_hash` solo se `length(file_hash)=64` (difesa anti-MD5).
- `metadata_json.$.content_hash` / `file_hash`: hash legacy trasportati nel
  payload, usati da outbox/reindex (compatibilità).

### Media Registry ledger

- `registry_runs`, `registry_events`, `projection_registry`,
  `backup_registry` — migration `191_media_registry_ledger.sql`.
- Adapter: `internal/platform/sqlite/mediaregistry/ledger.go`.
- Contract: `internal/capabilities/mediaregistry/contract.go` +
  `invariants.go` (`ValidateCounts`).
- `registry_events` è **già** un audit log con `event_type`, `before_hash`,
  `after_hash`, `payload_json`, `run_id` — base naturale per i nuovi eventi
  CAS.
- `backup_registry` ha già `backup_type`, `sha256`, `verified_at`, `status`
  (CREATED/VERIFIED/RESTORED/FAILED).

### Dedup esistenti (perimetro attuale)

- **Artifact dedup globale**: `ResolveAndRegister` deduplica per SHA-256 su
  `artifacts` (limite: buffer in memoria 500 MB, non streaming).
- **Download dedup per-URL**: `HTTPSourceStager`
  (`internal/infrastructure/stager/http_source_stager.go`) usa un path
  deterministico `sha256(URL|section|...)`; stesso URL → stesso file locale,
  hash calcolato durante la write. **Non** è content-dedup: URL diversi con
  stessi byte vengono scaricati due volte.
- **Clip dedup per YouTube ID**: sweeper `runDedupSweep` +
  `FindDuplicatesByYouTubeID` (dedup logico per video ID, non per byte).
- **Aggregator 4-key dedup**: `search_backend_local.go` collassa duplicati
  per content hash nel risultato di ricerca.
- **Publisher content-dedupe**: `drive.Publisher` con ConflictPolicy hash-based.
- **Voiceover dedup**: `ErrDeduplication` + dedup key
  (`internal/application/voiceover`).
- **Cache deterministiche esistenti**: translation cache
  (migration `023`, `SHA256(source_text + target_language)`), mediamemory
  query cache (migration `165`), research cache (migration `174`),
  `CompiledAudioPlan.PlanSHA256` (`internal/capabilities/audio/contract.go`).

## Gap per punto del design

| # | Punto design | Stato | Gap |
|---|---|---|---|
| 1 | Tabella `content_objects` (sha256 PK, size, mime, storage_uri, verified_at, integrity_status) | **MANCA** | `artifacts` (051) è simile ma è per-artifact (job_id, kind, lifecycle), non per-content puro. Serve una tabella `content_objects` canonica nel Media Registry |
| 2 | `media_assets` = identità logica con `content_sha256` | **PARZIALE** | Esiste `binary_sha256`, ma non è collegata a una tabella content; serve la FK logica `content_sha256` e il collegamento `media_assets → content_objects` |
| 3 | Download con CAS (lookup pre-download, streaming SHA-256, discard duplicati) | **PARZIALE** | `HTTPSourceStager` fa streaming hash ma non fa CAS lookup pre-download né discard dei byte duplicati globali |
| 3b | `source_identity_registry` (Drive file ID→sha, Artlist ID→sha, URL+etag→sha) | **MANCA** | Non esiste. `asset_sources` (054) è provenance per-asset, non un registry source→content hash |
| 4 | Global byte deduplication | **PARZIALE** | Esiste solo per `artifacts` (via `ResolveAndRegister`). Non copre download/clip/media_assets |
| 5–7 | Artifact Cache deterministica (`artifact_cache`, key = source hash + operation + processor + params) | **MANCA** | Non esiste `artifact_cache`. Cache esistenti sono per-singolo-uso (translation, query) non general-purpose |
| 8 | Dedup preprocessing video (NormalizeSpec → key) | **MANCA** | Nessuna cache per normalizzazione/AVPacket. Il pattern deterministica di `CompiledAudioPlan.Seal()` è il precedente da replicare |
| 9 | Dedup estrazione audio (AudioExtractResolver → HIT) | **PARZIALE** | Esiste il determinismo del piano audio (`PlanSHA256`), ma non la cache dell'output estratta |
| 10 | Flusso finale REQUEST→SOURCE→CAS→Artifact Cache | **MANCA** | Dipende da 1, 3b, 5-9 |
| 11 | Metriche risparmio (hits/misses, avoided_download_bytes, avoided_whisper_seconds, avoided_processing_ms) | **MANCA** | `registry_events.payload_json` può trasportarli ma non esistono contatori/telemetria dedicati |
| 12 | Provenance multi-sorgente per content object | **PARZIALE** | `asset_sources` esiste (054) e `ArtifactSource` (artifacts); non collegato a `content_objects` |
| 13 | Invariant: filename/URL/folder non stabiliscono identità | **PARZIALE** | Il download dedup è per-URL (viola lo spirito); la dedup clip è per YouTube ID. Va codificato come invariant |
| 14 | No perceptual hash come identity canonica | **OK** | `PHash`/fingerprint non sono mai usati come identity |
| 15 | CAS anche per gli output (audio, video render, immagini, thumbnail, transcript) | **PARZIALE** | `artifacts` copre già gli output; manca la dedup deterministico del processing |
| 16 | Backup CAS + integrity sweep | **PARZIALE** | `backup_registry` registra backup ma non esiste `cas-verify` (hash(file)==registry.sha256) né missing/orphan detector |
| 17 | Eventi audit CAS (CONTENT_*, ARTIFACT_CACHE_*, CAS_CORRUPTION_*) | **PARZIALE** | `registry_events` può ospitarli (event_type libero), ma i tipi non sono definiti/emessi |

## Gap principali (dettaglio)

### G1 — `content_objects` e collegamento logico→fisico

- Migration nuova: `content_objects(sha256 PK, size_bytes, mime_type,
  storage_uri, created_at, verified_at, integrity_status)`.
- Migration nuova: `media_assets.content_sha256` (colonna) con backfill da
  `binary_sha256` dove valido (expand → backfill → cutover → contract).
- Contract in `internal/capabilities/mediaregistry` (o nuovo package
  `cas`), adapter in `internal/platform/sqlite`.
- Riutilizzare il layout `blobs/sha256/XX/<hash>` già esistente in
  `LocalBlobStore` (non duplicare l'astrazione BlobStore).

### G2 — `source_identity_registry`

- Nuova tabella: `source_identity(source_type, source_ref, etag, sha256, ...)`.
- Prima del download: lookup per source → SHA noto → CAS hit → niente download.
- Integrazione nei downloader: `HTTPSourceStager` / Artlist / Drive sync.

### G3 — `artifact_cache` deterministica

- Nuova tabella: `artifact_cache(cache_key PK, source_sha256, artifact_kind,
  processor, processor_version, parameters_json, output_asset_id,
  output_sha256, created_at, status)`.
- Key = SHA256(CanonicalJSON{source, operation, processor, model, params}).
- Replicare il pattern `Seal()` di `CompiledAudioPlan` (già deterministico).
- Punti di inserimento: Whisper (`WhisperTranscriberAdapter` →
  `scripts/bridges/whisper_transcriber.py`, key = source_sha + model +
  language + vad), preprocessing video (NormalizeSpec), estrazione audio.

### G4 — Streaming hash + discard duplicati nel download

- `HTTPSourceStager.StageSourceV2` calcola già l'hash durante la write.
- Aggiungere: lookup CAS pre-download, e dopo il download se il byte-stream
  SHA corrisponde a un content object esistente → scartare i byte (nessuna
  seconda copia), collegare l'asset al content esistente.

### G5 — Metriche risparmio

- Contatori: `content_cache_hits/misses`, `transcript_cache_hits/misses`,
  `video_preprocess_hits/misses`, `audio_preprocess_hits/misses`,
  `avoided_download_bytes`, `avoided_whisper_seconds`, `avoided_processing_ms`.
- Dove: `internal/kernel/observability` (metriche) + `registry_events`
  (`cache_status`, `cache_key`, `source_hash` nel `payload_json`).

### G6 — Integrity scanner + backup CAS

- Comando admin `cas-verify`: per ogni `content_object`, `hash(file) ==
  registry.sha256`; riporta missing/corrupt/orphan.
- Eventi: `CAS_CORRUPTION_DETECTED`, `CAS_OBJECT_REPAIRED`.
- `backup_registry` esteso per accettare `backup_type='CAS'` (o riferimenti
  agli oggetti CAS nel manifest).

## Vincoli architetturali (AGENTS.md)

- SQLite è il canonical state store (`mattn/go-sqlite3`). Le nuove tabelle
  vanno in migration su `primary`.
- Nuovi package/contracts SOLO nei target root: `internal/capabilities`
  (contract/port), `internal/platform` (adapter SQLite), wiring in
  `internal/app`. Le zone migration-only (`internal/application`,
  `internal/infrastructure`, `internal/domain`, `internal/api`) NON ricevono
  nuove capacità/file.
  - Nota: `LocalBlobStore` e `ResolveAndRegister` vivono oggi in
    `internal/application/assets/artifacts` (zona migration-only): il nuovo
    layer CAS va costruito nei target root, riusando il layout FS esistente
    senza creare nuovi file legacy.
- Le trasformazioni deterministiche vanno in un registry/resolver/sampler
  condiviso (regola "new routing/resolution logic must enter a shared
  registry"), coerente con la seconda regola del design.
- Backend non disponibile → fail closed con errori tipizzati (no no-op).

## Definition of Done (target)

- [ ] Migration `content_objects` + collegamento `media_assets.content_sha256`
- [ ] `source_identity_registry` + lookup pre-download
- [ ] `artifact_cache` + chiave deterministica
- [ ] Reuse Whisper / video preprocess / audio extract
- [ ] Metriche risparmio + eventi audit CAS
- [ ] `cas-verify` + backup CAS + invarianti documentati

## File di riferimento

- `migrations/sqlite/051_artifacts.sql`, `054_asset_registry.sql`,
  `152_add_canonical_metadata_columns.sql`, `191_media_registry_ledger.sql`
- `internal/application/assets/artifacts/{local_blob.go,types.go,service.go}`
- `internal/infrastructure/stager/http_source_stager.go`
- `internal/platform/sqlite/mediaregistry/ledger.go`
- `internal/capabilities/mediaregistry/{contract.go,invariants.go}`
- `internal/capabilities/audio/{contract.go,compiler.go,resolve.go}`
- `internal/kernel/observability/`
- `scripts/bridges/whisper_transcriber.py`
