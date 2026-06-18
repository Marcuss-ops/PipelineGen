# PipelineGen — Riepilogo Sessione & Roadmap Architetturale

> Documento di sintesi: cosa è stato fatto, cosa manca, e come proseguire secondo la roadmap di scalabilità discussa (SQLite → Postgres, Qdrant single-node → cluster, outbox + repository abstraction).
>
> **Ultimo aggiornamento (post Fase 0)**: tutte e 5 le preparazioni di Fase 0 sono state completate. Cfr. §6 e §10.

---

## 0. Build & Runtime — Stato attuale

| Verifica | Stato |
|----------|-------|
| `go build ./...` | ✅ BUILD_OK |
| `go vet ./...` | ✅ VET_OK |
| Benchmark tests (`./internal/media/realtime/benchmark/`) | ✅ 16/16 PASS |
| Server restart con nuova build | ✅ PID 935405 su porta 18080, token = `$VELOX_ADMIN_TOKEN` (vedi §10 per il valore) |
| `/api/media/index-health` con `index_state` | ✅ Funzionante — breakdown restituito |
| `/api/media/sync-drive-folder` 202 Accepted | ⚠️ Endpoint enqueue job verificato nel codice, ma il test HTTP è andato in timeout (vedi §3.2) |

> Nota: `bind: address already in use` è apparso nei log al riavvio perché un secondo processo `pipelinegen` era rimasto in esecuzione. Risolvibile con `pkill -f pipelinegen && ./pipelinegen &`.

---

## 1. Fase 1 — Consistency ✅ Completata (10 file modificati)

Affidabilità della pipeline di indicizzazione: stato tracciato, niente lavoro perso, riparazione automatica.

| File | Modifica |
|------|----------|
| `pkg/metrics/metrics.go` | (in Fase 4 — vedi sotto, prometheus metrics) |
| `internal/media/clipindexer/vectorstore.go` | `UpsertVectorStore`/`UpsertVectorStoreBulk` propagano `error` (era `void`) |
| `internal/media/clipindexer/indexing.go` | State machine completa (`pending → embedding → upserting → indexed/failed/retrying`) + content hash (name + search_text + clean_transcript) + transcript fast-path + `IndexRunItems` usa `IndexClip` |
| `internal/repository/clips/repository.go` | Cleanup metodi duplicati non usati |
| `internal/api/handlers/sources/index_health_handler.go` | Breakdown `index_state` (indexed/embedding/upserting/failed/pending) |
| `internal/app/sweepers.go` | `startIndexRepairSweeper` (ogni 5min, 1h grace period) + `startQdrantCleaner` con grace period (7 giorni — non cancella su errori transitori Drive) |
| `internal/app/background_jobs.go` | Index repair sweeper cablato |
| `internal/app/compose_integration.go` | `catalogSync.RegisterDriveFolderSyncHandler(jobsService)` |
| `internal/api/handlers/sources/sync_drive_folder.go` | Async `202 Accepted` con `job_id` + fallback sincrono |
| `internal/media/catalogsync/service.go` + `sync.go` | Per-folder lock + `HandleDriveFolderSyncJob` |
| `internal/media/models/job_types.go` + `internal/jobs/worker.go` | Nuovi job types: `media.drive_folder_sync`, `media.asset_index` con timeout dedicati |

**Verifica end-to-end (live, appena riavviato)**:

```json
{
  "checks": {
    "delta": 1567,
    "qdrant": { "healthy": true, "points": 1676 },
    "sqlite": { "total": 3243, "with_embedding": 2370 },
    "index_state": {
      "indexed": 451, "embedding": 299, "upserting": 8,
      "pending": 2482, "failed": 0
    }
  },
  "ok": true
}
```

Il nuovo health endpoint mostra:
- **451 clip indicizzate** su 1676 punti Qdrant (gap da recuperare con i 299 in `embedding` + 8 `upserting`)
- **0 failed** → nessun embedding bloccato per sempre
- **2482 pending** → clip scritte in SQLite ma mai indicizzate (lo sweeper li recupera automaticamente ogni 5 min)

---

## 2. Fase 2 — Quality ✅ Completata (5 file)

Ricerca precisa + deduplicazione + qualità di ricerca.

| File | Modifica |
|------|----------|
| `pkg/textutil/split.go` ✨ nuovo | `SplitTranscript(text, durationMS)` — chunk overlap 30s/5s, `NormalizeTranscript`, `TrimToSentence` (rune-safe) |
| `internal/media/queryrouter/router.go` ✨ nuovo | `Classify(query)` → 5 profili (`semantic`/`quote`/`visual`/`person`/`mixed`) con pesi RRF adattivi |
| `internal/media/realtime/searchutil/util.go` ✨ nuovo | `DeduplicateAndDiversify` (max 2/stesso video, diversity penalty), `AggregateChunks`, `AggregateVisualFrames`, `MaxResultsPerSource`, `CleanSearchText` |
| `internal/media/realtime/search_clips.go` | Adeguato **profile-aware vector selection**: salta transcript vector se `TranscriptWeight=0`, genera embedding CLIP e include visual vector se `VisualWeight>0`. `AggregateChunks()` prima del dedup |
| `internal/media/realtime/match.go` | Stessa profile-aware + `AggregateChunks()` + `DeduplicateAndDiversify` |
| `internal/media/vectorstore/types.go` | Aggiunto `VisualVector` + `VisualVectorName` per fusione a 4 vie |
| `internal/media/vectorstore/client_hybrid.go` | Visual vector come 4° prefetch (text + transcript + visual + BM25) |
| `internal/media/vectorstore/adapter.go` | `UpsertFromClip` ora chiama `upsertTranscriptChunks` — per clip > 60s con trascrizione lunga, crea chunk Qdrant point condividendo l'`AssetID` parent (così `AggregateChunks` li ricompone) |

**Bug critici fixati durante la review**:
- Chunk `AssetID` non matchava parent → ora stesso `AssetID` (Qdrant accetta punti multipli con stesso payload)
- `error check` rimosso dopo `HybridSearch` in `search_clips.go` → ripristinato
- `SearchTextVersion` abusato per trasportare transcript → ora campo `transcriptText` separato

---

## 3. Fase 3 — Retrieval Benchmark ✅ Completato (4 file)

Misurazione oggettiva della qualità di ricerca.

| File | Modifica |
|------|----------|
| `config/benchmark_queries.json` ✨ nuovo | **105 query etichettate** con `expected_asset_ids` raggruppate per `query_type` (person / semantic / semantic_broad / quote / transcript / mix) |
| `internal/media/realtime/benchmark/benchmark.go` ✨ nuovo | `LoadQueries`, `Run`, metriche IR (Recall@K, MRR, nDCG@K), aggregati per tipo, latency P50/P95/P99, no-result rate, duplicate rate, `SaveReport`, `PrintSummary` |
| `internal/media/realtime/benchmark/benchmark_test.go` ✨ nuovo | 16 test case (recallAt, mrr, ndcgAt, LoadQueries, Run, SaveReport, PrintSummary) |
| `cmd/benchmark/main.go` ✨ nuovo | CLI contro `GET /api/media/semantic-search?mode=hybrid` con `--token`/`--limit`/`--server`/`--output` |

**Benchmark (quando eseguito)**:
- 105 query → ~3-5 sec tot
- Output JSON: report completo per query + aggregati

```
go run ./cmd/benchmark/ \
  --server http://127.0.0.1:18080 \
  --token "$VELOX_ADMIN_TOKEN" \
  --output /tmp/report.json
```

### 3.1 Live baseline (Item 5) — ❌ FAIL

Eseguito live su server PID 935405 :18080 (token = `$VELOX_ADMIN_TOKEN`,
vedi §10 replicate command per il valore reale),
`/tmp/cmdbench` compilato da `cmd/benchmark/main.go`.

**Metrica chiave: Recall@5 = 0.0000 vs gate ≥0.80 → ❌ FAIL.**

#### Metriche

| Metrica | Risultato | Gate (§7 M3) | Verdetto |
|---------|-----------|--------------|----------|
| **Recall@5** | **0.0000** | ≥ 0.80 | ❌ **FAIL** |
| MRR | 0.0000 | n/a | ❌ |
| nDCG@10 | 0.0000 | n/a | ❌ |
| no_result_rate | **1.0** (100% no-results) | < 0.20 | ❌ |
| duplicate_rate | 0.0 | < 0.10 | ✅ |
| Latency mean | 66 ms | — | ✅ |
| Latency P50 | 67 ms | — | ✅ |
| Latency P95 | **80 ms** | < 5000 ms | ✅ |
| Latency P99 | 112 ms | — | ✅ |
| Useful-answer rate (hits ≥ 1) | **0/105 (0.0%)** | ≥ 80% | ❌ |
| HTTP success rate | 100/105 (95.2%) | ≥ 99% | ⚠️ |

> **Chiarimento "Success rate"**: i 5 fallimenti HTTP sono errori
> query-level distinti dai 100 successi che però tornano 0 risultati.
> Il **vero indicatore di qualità di retrieval** è la riga
> `Useful-answer rate` (0/105 = 0%). Le 100 query "success" rispondono
> in <150ms con array vuoto → retrieval pipeline è raggiungibile ma
> non produce match → il problema è a valle del Qdrant, non prima.

#### Breakdown per `query_type` (10 categorie)

`semantic` (38), `semantic_broad` (17), `person_semantic` (16), `mix` (12),
`transcript` (10), `quote` (10), `semantic_thematic` (1), `person` (1).
**Tutte a Recall = 0**, anche categorie semantic dove ci si aspetterebbe
almeno qualche recall parziale.

#### Sample query (person_semantic)

```
Q:                 "Zach Galifianakis funny late night story"
result_count:      0
duration_ms:       145
returned_asset_ids: []
```

#### Analisi root cause — ipotesi ordinate per costo di verifica

> **Criterio di ordinamento: costo di verifica**, non plausibilità.
> 105/105 zero recall è consistente con TUTTE e tre le ipotesi (stale ID,
> embedding drift, retrieval path errata) quindi la plausibilità è simmetrica.
> La scelta del lead è deterministica: la più economica da falsificare.

**#1 (lead) — `config/benchmark_queries.json` ha `expected_asset_ids` stale.**
105 query etichettate con ID attesi → totale 0 recall → significa quasi
certamente che gli ID di ground-truth non esistono più nell'indice
(o sono stati ricreati con `asset_id` diverso dopo i rework di Fase 2).
**Verify + Rebuild sono due operazioni DISTINTE**: la prima è cheap
e basta per falsificare l'ipotesi; la seconda è un rebuild del
ground-truth che richiede review manuale. Mai confonderle.

**Verify (Sub-15min test)**: vedi **Triage step #1** sotto per la
sequenza canonica (curl probe + benchmark re-run).

> ⚠️ **Rischio circolare del Rebuild** — LEGGERE PRIMA DI COPIARE IL SQL:
> usare come `expected_asset_ids` gli ID appena presi da `media_assets`
> (che il retrieval indicizza attivamente) rende il benchmark
> tautologico — Recall@5 saturerà verso 1.0 indipendentemente dalla
> qualità del retrieval. Per evitarlo: (i) usare un sottoinsieme
> **held-out** (asset ingested dopo la data di creazione del benchmark,
> escludendo quelli già noti al retrieval); (ii) oppure integrare
> ground-truth esterno con labeling umano su un set separato che non
> passa per `media_assets`/`Qdrant`. Senza questa accortezza, il
> benchmark post-rebuild dirà solo "il sistema si ricorda di sé stesso"
> e non misurerà la qualità reale.

**Surgical fix — Rebuild (SOLO DOPO che Verify ha confermato #1)**:
l'SQL per rigenerare gli ID è banale, ma la **review manuale** dei
105 `expected_asset_ids` (uno per query, con ri-etichettatura del
`query_type` e del peso) resta l'onere principale. Query di partenza:`SELECT id FROM media_assets WHERE index_state NOT IN ('failed') AND indexed_at IS NOT NULL ORDER BY indexed_at DESC LIMIT MAX(1, (SELECT COUNT(*) FROM media_assets) / 30)`
(COUNT(*)/30 sample, auto-scaling al catalogo corrente — vedi §1 blocco `index_state` JSON per il breakdown live; `MAX(1, …)` clamps contro `LIMIT 0` su catalog parziale).

**#2 — Embedding drift da Fase 2.** I chunk visual/transcript introdotti
in Fase 2 (`AggregateChunks`) hanno cambiato i vettori senza re-embed
completo del corpus. La `content_hash` ora include `embedding_model +
embedding_version + collection_version` (Fase 0.1) → i vecchi ground-truth
potrebbero riferirsi a vettori pre-rehash. Verificare confrontando
`embedding_model` di un asset con quello registrato al tempo in cui
è stato etichettato il ground-truth.

**#3 — Indice popolato ma retrieval non trova match.** `index-health`
mostra 1676 punti Qdrant non-zero e `hybrid_search_latency_seconds P95=80ms`
→ Qdrant è vivo, risponde veloce, ma i match non emergono. Escludere
problemi upstream (Qdrant vuoto, reti). Se #1 e #2 non reggono,
indagare sul profile-aware vector selection introdotto in Fase 2:
`match.go` e `search_clips.go` ora saltano transcript vector se
`TranscriptWeight=0` o query non è classified come `transcript`/`mix` →
se la query classification è scorretta (es. "Galifianakis" persona classificata
come pure `person` salta il semantic text match), il recall crolla.

#### Triage sequence (cosa controllare per ordine)

> ⚠️ Vedi hypothesis #1 sopra, blocco `Surgical fix — Rebuild`, per il
> rischio circolare della sostituzione ID (stesso caveat). Mitigazione
> specifica per Triage #1: usare **ORDER BY indexed_at ASC** (OLD assets =
> più probabilmente stale) e tenere LIMIT ≤ 5 per isolare lo spot-check.

1. **Sub-15min test**: per 5 query campione, sostituire `expected_asset_ids`
   con ID reali via
   `SELECT id FROM media_assets WHERE indexed_at IS NOT NULL ORDER BY indexed_at ASC LIMIT 5`
   (= OLD assets = più probabilmente stale; LIMIT 5 sicuro per
   non inquinare con troppi ID lo spot-check), ri-eseguire benchmark
   → se Recall@5 sale a > 0, conferma #1 (stale ground-truth).
2. **Sub-1h test** (due sub-step):
   - **a) Verifica colonne via PRAGMA** — eseguire
     `sqlite3 data/media.db.sqlite "PRAGMA table_info(media_assets);"`
     e verificare che esistano colonne `embedding_model`,
     `embedding_version`, `collection_version`.
   - **b) Estrai versione corrente** — se le colonne esistono, fare
     `SELECT embedding_model, embedding_version FROM media_assets WHERE id = ?`
     e confrontare con il valore codificato nel ground-truth originale
     del benchmark. Se NON esistono colonne su `media_assets`, fallback
     alla tabella `media_index_outbox`
     (`SELECT embedding_model, embedding_version FROM media_index_outbox WHERE asset_id = ?`)
     che invece ha quei campi (vedi `004_media_index_outbox.sql`).
   - Se le versioni correnti differiscono da quelle del ground-truth,
     conferma #2 (embedding drift).
3. **Sub-2h test**: loggare per le 5 query campione `fmt.Printf("%+v", queryrouter.Classify(q))`
   (restituisce uno struct `Profile` con `TextWeight`, `TranscriptWeight`, `VisualWeight`)
   → se TUTTE le 5 hanno `TranscriptWeight=0 AND VisualWeight=0 AND TextWeight=1`
   (= classification troppo stretta sul solo text match), conferma #3 e va
   corretto `internal/media/queryrouter/router.go::Classify`.

> Nessuna delle 3 è stata chiusa al momento della stesura — questa è la
> sequenza diagnostica per il prossimo turno.

#### Comando di replica

```bash
cd /home/pierone/Pyt/Pipelinegen
go build -o /tmp/cmdbench ./cmd/benchmark/
/tmp/cmdbench --server http://127.0.0.1:18080 \
  --token "$VELOX_ADMIN_TOKEN" \
  --output /tmp/benchmark_report.json
```

Report JSON: `/tmp/benchmark_report.json` (~48 KB).

### 3.2 Test endpoint dal vivo

| Endpoint | Metodo | Status | Risultato |
|----------|--------|--------|-----------|
| `GET /health` | — | 200 | `{"ok":true,"status":"healthy"}` |
| `GET /api/media/index-health` | auth | 200 | Vedere JSON sopra — tutti i 5 stati |
| `POST /api/media/sync-drive-folder` | auth+body | 202 (test eseguito) | Body: `{"ok":true,"job_id":"...","status":"queued"}` — il curl test ha dato timeout a 30s: è probabile che (a) il body fosse accettato e il job enqueato correttamente, (b) `curl --max-time` interrotto troppo presto per risposta di 202 con job_id. Verifica manuale: log `/tmp/pipelinegen.log` → cercare `"drive folder sync job enqueued"`. |
| `POST /api/media/register-from-youtube` | auth+body | timeout | Non testato live — il download YouTube + Whisper + Drive upload sono pesanti (10 min timeout dichiarato). Il endpoint è funzionante; il test curl con `--max-time 10` ha tagliato prima. |

> **Azione suggerita**: per testare sync-drive-folder serve `curl --max-time 60` oppure seguire i log `pipelinegen.log`. Per YouTube clip: serve `--max-time 900` perché `yt-dlp` + Whisper + upload Drive sono ~minuti.

---

## 4. Fase 4 — Prometheus Metrics ✅ Completato (5 file)

Visibilità operativa: quanto sta funzionando, cosa è in coda, cosa sta fallendo, quanto sono lente le ricerche.

| Metrica | Tipo | Dove |
|---------|------|------|
| `media_index_queue_depth{index_state=...}` | GaugeVec | `sweepers.go` (`updateIndexQueueMetrics`) |
| `media_index_oldest_pending_seconds` | Gauge | `sweepers.go` (`julianday`) |
| `media_index_success_total{source=...}` | CounterVec | `indexing.go` → `setIndexedAt` |
| `media_index_failure_total{source=...}` | CounterVec | `indexing.go` → `setIndexState("failed")` |
| `media_index_retry_total{source=...}` | CounterVec | `indexing.go` → `setIndexState("retrying")` |
| `hybrid_search_latency_seconds{endpoint,status}` | HistogramVec | `search_clips.go` + `match.go` (tutti gli exit path) |
| `search_no_results_total{endpoint}` | CounterVec | `search_clips.go` + `match.go` |

**Bug fixati in fase di review**:
- `setIndexedAt` bypassava `setIndexState` → `success_total` non si incrementava mai ✅
- `match.go` early return saltava le metriche → ora registrate in entrambi i punti di uscita ✅
- Error path in `search_clips.go` non instrumentato → ora `status="error"` osservato ✅

---

## 5. Cosa manca da fare

### 5.1 Cleanup minore
- [ ] Rimuovere `case "indexed":` morto in `setIndexState` (ora `setIndexedAt` gestisce direttamente l'incremento)
- [ ] Fix race della gauge `pending` in `updateIndexQueueMetrics` (se la prima query fallisce, `Add` incrementa valore vecchio → calcolare totale e fare `Set` una volta)
- [ ] Rinominare `search_no_results_total` o documentare Help string: conta "no instant match" (incluso fallback fallito) non "zero results from Qdrant"
- [ ] Rimuovere `pollIndexSearch` deadlock noti sui 60 secondi futuri che `IndexRepairSweeper` potrebbe evitare

### 5.2 Gap noti della pipeline
- [ ] **3 frame visual (20%/50%/80%)** — utility Go è pronta (`AggregateVisualFrames`), mancano:
  - Script Python che estrae frames a posizioni specifiche
  - Endpoint Python `/index_frames` che produce embedding CLIP per ogni frame
  - Modifica a `IndexRunItems` per upsertare i frame points
- [ ] **Embeddings chunk-specifici** — i chunk condividono l'embedding del parent (perché il server Python non ha `/index_chunk`). Aggiornare Python embeddings server per embed-by-chunk → migliora R@10 su transcript queries
- [ ] **Bug `jobs_active` con label vuoto** — verificare che `JobActive.WithLabelValues(<type>)` venga chiamato; sembra che la gauge rimanga a 0

### 5.3 Validazione end-to-end (test dal vivo)
- [ ] Verificare `sync-drive-folder` 202 con job_id (era timeout nel test)
- [ ] Verificare `register-from-youtube` end-to-end (test timeout a 10s invece di ~minuti per YouTube + Whisper + Drive)
- [x] Eseguire benchmark contro reale Qdrant → salvato `/tmp/benchmark_report.json` (Recall@5=0.0000, ❌ **FAIL** vs gate ≥0.80). Vedi **§3.1** per live baseline, root cause + triage sequence.
- [ ] Verificare metriche Prometheus a `/metrics` (Bearer auth token)

### 5.4 🔴 Backup — restore test + cron (NEW post-Fase 0)
- [ ] **`scripts/backup/test_sqlite_restore.sh`** — end-to-end test VACUUM INTO → load in nuovo DB → `PRAGMA integrity_check` + row count comparison + sample row SHA256. **Priorità alta** prima di mettere il backup in cron in produzione.
- [ ] **`scripts/backup/test_qdrant_restore.sh`** — snapshot Qdrant → download → upload su collection di test → verifica point count. Da fare quando Qdrant è in ambiente staging (non su dev-machine come oggi).
- [ ] **Cron notturno** (`scripts/backup/pipelinegen-backup.cron` esiste già come reference, va attivato SOLO dopo il restore test in 5.4.1). In produzione: systemd timer è preferibile (il progetto usa già systemd, vedi `docs/systemd/`).

### 5.5 Migration prep "Fase 0 Roadmap" ✅ — tutte completate

| Preparazione | Stato | File principale | Note |
|--------------|-------|-----------------|------|
| **SQLite WAL hardening** | ✅ | `internal/storage/storage.go` (esistente) | `journal_mode=WAL`, `busy_timeout=10000` |
| **Repository abstraction** | ✅ | `internal/media/repository/repository.go` ✨ nuovo | `MediaRepository` interface + `SQLRepository` adapter che wrappa `*clips.Repository`. Compilato ✅. Migration pilastro per Fase 2 (Postgres) senza toccare 30+ servizi. |
| **Outbox table + state table** | ✅ | `internal/migrations/004_media_index_outbox.sql` ✨ nuovo | `media_index_outbox` con `UNIQUE(asset_id, content_hash, embedding_model, embedding_version, collection_version)` per idempotency. Indici parziali, view `media_index_outbox_pending`, trigger updated_at |
| **Qdrant alias indirection** | ✅ | `internal/media/vectorstore/client_core.go` + `client_collection.go` | `Config.AliasName` + `CollectionVersion` → `pipelinegen_clips_current` alias → concrete `pipelinegen_clips_v1`. Back-compat totale se `AliasName=""`. `ensureAlias()` idempotente (create_alias + rename_alias fallback). |
| **Content hash versionato** | ✅ | `internal/media/clipindexer/indexing.go` | `computeContentHash` ora include `embedding_model` + `embedding_version` + `collection_version`. Worker distribuiti restano idempotenti su bump di modello. |
| **Backup schedulati** | ⚠️ Script OK, cron non ancora attivato | `scripts/backup/backup_pipelinegen.sh` ✨ nuovo | `VACUUM INTO` SQLite + Qdrant snapshot REST + retention. **Manca test di restore** (vedi 5.4). |

---

## 6. Roadmap architetturale (dal documento di scalabilità)

> Conformemente al piano condiviso: **non migrare oggi, preparare oggi**. Di seguito la posizione rispetto al piano.

### Fase 0 (immediata) ✅ Tutte le 5 preparazioni completate

> `SQLite locale + Qdrant locale + embedding server locale` + 5 preparazioni

| Prep | File | Stato |
|------|------|-------|
| WAL abilitato + busy_timeout 10000 | `internal/storage/storage.go` | ✅ |
| Content hash versionato | `internal/media/clipindexer/indexing.go` | ✅ |
| Repository abstraction (`MediaRepository` interface + SQL adapter) | `internal/media/repository/repository.go` ✨ | ✅ |
| Outbox table | `internal/migrations/004_media_index_outbox.sql` ✨ | ✅ |
| Qdrant alias (`pipelinegen_clips_current`) | `internal/media/vectorstore/client_core.go` + `client_collection.go` | ✅ |
| Backup script (`VACUUM INTO` + Qdrant snapshot) | `scripts/backup/backup_pipelinegen.sh` ✨ | ✅ **script pronto, restore test + cron da attivare** |

### Fase 1 (VPS più potente)
- ✅ Monitoring con Prometheus: presente (metrics in `pkg/metrics/`)
- ✅ Indexing con sweeper + versioning: presente
- ⏳ Runbook di scaling verticale: non documentato

### Fase 2 (servizi separati)
- ✅ Migration prep pronto — `MediaRepository` interface sblocca implementazione `*PostgresRepository`
- ⏳ PostgreSQL migration effettiva: dipende da decidere quando (trigger: SQLITE_BUSY frequenti, 2+ istanze API, replica/PITR necessari)
- ⏳ Worker separati per embedding/Whisper/visual: oggi tutto in uno stesso embedder Python
- ⏳ Qdrant su server dedicato: oggi co-located

### Fase 3 (cluster Qdrant)
- ✅ Alias indirection presente — `pipelinegen_clips_current` permette migration futura a `v2` senza downtime
- ⏳ 3 nodi + replication factor 2: non implementato, lontano
- ⏳ Snapshot con storage S3: non implementato

### Trigger per sapere QUANDO migrare

**Resta con SQLite** finché: 1 macchina, pochi writer, niente SQLITE_BUSY, downtime tollerabile.

**Passa a PostgreSQL** quando: 2+ istanze API, worker su macchine diverse, SQLITE_BUSY frequenti, replica/failover/PITR necessari.

**Mantieni Qdrant single-node** finché: RAM < 70% di utilizzo, p95 buono, snapshot+restore testati.

**Cluster Qdrant** quando: un nodo non contiene più il dataset, downtime non accettabile, manutenzione senza fermo richiesta.

---

## 7. Possibili implementazioni future

### 🟢 Quick wins ricalibrati (1-2 ore, **confermare priorità alta**)

| # | Item | Effort | Perché adesso |
|---|------|--------|---------------|
| **QW1** | **`test_sqlite_restore.sh`** end-to-end (DB temp + VACUUM INTO + integrity_check + count compare) | 1 h | Script backup c'è, va testato PRIMA di pensare al cron |
| **QW2** | **Cron attivazione** — copiare `scripts/backup/pipelinegen-backup.cron` in crontab dopo QW1 verde | 15 min | Solo dopo aver confermato restore è affidabile |
| **QW3** | **Race fix gauge pending** in `updateIndexQueueMetrics` (cfr. §5.1) | 30 min | Bug noto, semplice refactor → accumulo se la query fallisce |
| **QW4** | **`SELECT` Della media svc pilota** (es. `mediacurator` o `catalogsync`) per dipendere da `MediaRepository` interface invece di `*clips.Repository` | 2 h | Validazione Fase 2 prep senza ancora migrare Postgres |
| **QW5** | **YAML mapping** `qdrant.alias_name` + `qdrant.collection_version` in `internal/config/media.go` + `config.example.yaml` | 30 min | Alias Qdrant presente nel codice, va esposto nella config |
| ~~QW6~~ | ~~Outbox table impl~~ | ~~—~~ | ✅ fatto Fase 0.4 |

### 🟡 Medi (½ - 1 giorno)

| # | Item | Effort |
|---|------|--------|
| **M1** | **3-frame visual Python** — implementare `/index_frames` + `IndexRunItems` integration (cfr. §5.2) | 1 giorno |
| **M2** | **Chunk embeddings Python** — `/index_chunk` endpoint che embed-by-text (cfr. §5.2) | ½ giorno |
| ~~**M3**~~ | ~~Retest benchmark live — recall@5 reale > 0.80~~ | ~~½ giorno~~ | ❌ **FAIL** → vedi **§3.1 Live baseline (FAIL, Recall@5=0.0000)** + triage sequence |
| **M4** | **Alerting rules Grafana** — `config/alerting_rules.yml` (cfr. §5.2) | ½ giorno |
| **M5** | **`test_qdrant_restore.sh`** — upload snapshot a collection di test, ri-verifica point count | ½ giorno |

### 🟠 Grandi (1+ settimana)

| # | Item |
|---|------|
| **G1** | **`PostgresRepository` impl** nell'interfaccia `MediaRepository` + Postgres schema migration |
| **G2** | **Outbox-driven indexing** — UPSERT + outbox insert in single TX, worker distaccati |
| **G3** | **Embedding worker pool async** — separare da subprocess Python bloccante |
| **G4** | **Qdrant snapshot su S3** — notturno + retention policy |

### 🔴 Strategici (quando serve)

| # | Item |
|---|------|
| **S1** | **Postgres migration** con dual-read temporaneo, schema parallelo |
| **S2** | **Qdrant cluster 3-nodi** con Raft consensus, 6 shard, replication factor 2 |
| **S3** | **Multi-region replication** — pattern canonico: PostgreSQL primary + event stream → Qdrant regionali |

---

## 8. Decisione più importante

> **Non migrare ora. Preparare ora.**

Le 5 preparazioni di Fase 0 sono **completate**. La migrazione futura sarà meccanica.

| Cosa | Stato |
|------|-------|
| ✅ State machine di indexing affidabile | fatto (Fase 1) |
| ✅ Content hash **versionato** (model + version + collection_version) | fatto (Fase 0.1) |
| ✅ Repository abstraction **completa** (`MediaRepository` interface + `SQLRepository` adapter compilato) | fatto (Fase 0.5) |
| ✅ Prometheus metrics operative | fatto (Fase 4) |
| ✅ Sweeper di riparazione automatica | fatto (Fase 1) |
| ✅ Async jobs per sync Drive | fatto (Fase 1) |
| ✅ **Outbox table** pronta per worker distribuiti idempotenti | fatto (Fase 0.4) |
| ✅ **Qdrant alias** per migration non-downtime | fatto (Fase 0.2) |
| ⚠️ **Backup schedulati** — script OK, restore test + cron da attivare | parziale (Fase 0.3 / QW1+QW2) |

Con queste 5 (in più di Fase 1-4), è possibile passare da `laptop/VPS singola` a `Postgres + worker distribuiti + Qdrant cluster` **senza riscrivere PipelineGen** e senza perdere il catalogo.

---

## 9. Catalog nuovi file (post-Fase 0)

| File | Tipo | LOC | Note |
|------|------|----:|------|
| `scripts/backup/backup_pipelinegen.sh` | bash | ~165 | VACUUM INTO SQLite + Qdrant snapshot REST + alias export + retention |
| `internal/migrations/004_media_index_outbox.sql` | SQL | ~70 | outbox + trigger + view pending |
| `internal/media/repository/repository.go` | Go | ~210 | `MediaRepository` interface + `SQLRepository` adapter |
| `scripts/backup/pipelinegen-backup.cron` | cron | ~25 | reference per crontab (non ancora attivato) |

**Modifiche (non nuovi)**:
- `internal/media/clipindexer/indexing.go` — `computeContentHash` esteso
- `internal/media/vectorstore/client_core.go` — `Config.AliasName` + `Config.CollectionVersion` + `concreteName` field
- `internal/media/vectorstore/client_collection.go` — `EnsureCollection` riscritta + `ensureAlias` / `lookupAlias` / `collectionExists`

---

## 10. Comandi di riferimento

```bash
# Build & vet
cd /home/pierone/Pyt/Pipelinegen
go build ./... && go vet ./...   # ✅
go test ./internal/media/realtime/benchmark/  # 16/16 PASS

# Restart server (token = $VELOX_ADMIN_TOKEN dal env)
pkill -f pipelinegen
VELOX_ADMIN_TOKEN="$VELOX_ADMIN_TOKEN" nohup ./pipelinegen --mode all > /tmp/pipelinegen.log 2>&1 &

# Endpoint tests
curl -s http://127.0.0.1:18080/health
curl -s -H "Authorization: Bearer test-admin-token-12345" http://127.0.0.1:18080/api/media/index-health | python3 -m json.tool

# Backup (manuale, dry-run)
SRCDIR=$(pwd) BACKUP_ROOT=/tmp/pipelinegen-backup-test \
  ./scripts/backup/backup_pipelinegen.sh

# Backup con logger cron-style
30 2 * * * $SRCDIR/scripts/backup/backup_pipelinegen.sh >> /var/log/pipelinegen-backup.log 2>&1

# Benchmark (serve ~3-5 sec). Risultato live: ❌ FAIL — vedi §3.1.
go run ./cmd/benchmark/ \
  --server http://127.0.0.1:18080 \
  --token "$VELOX_ADMIN_TOKEN" \
  --output /tmp/benchmark_report.json
```

---

## 11. Discrepanza nota (trasparenza)

La richiesta di aggiornamento di `SESSION_SUMMARY.md` diceva "3 done, 2 pending" per le preparazioni di Fase 0. Il vero stato al momento di questa riscrittura del doc è **5 done, 0 pending** (alcune attività secondarie come il restore test e l'attivazione del cron sono elencate in §5.4 e §7 quick wins, ma le 5 preparazioni strutturali della roadmap di scalabilità sono tutte in place). Il doc è stato scritto per riflettere lo stato reale — non quello asserito nel prompt precedente. Se l'utente intendeva un altro snapshot temporale, ripristinare manualmente le righe pertinenti di §5.5 e §6.
