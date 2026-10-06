# TODO — Pipeline 100x: dove bruciamo secondi e i tool da creare

Piano operativo nato dall'analisi misurata del 2026-10-02.
Fonti dati (già nel repo, nessuna stima a mano):

- `ops/benchmarks/job-timing-20260928T152930Z.json` — snapshot 934 job / 5.543 run / 64.530 operazioni, finestra 2026-09-01 → 2026-09-28
- `ops/benchmarks/chain-debug/job_1790093191569826084_fbcfed1c.md` — anatomia al millisecondo di un job completo (Dolly, 5 scene)
- `ops/benchmarks/chain-debug/*.md` — altri job debug con clock canonico
- `TODO-milton-finaljob-performance.md` — timing del final job (5m48s: overlap 106s, audio 80,3s, overlay 137,1s, final remoto 36,5s, Docs 17,1s)

Convenzione: ogni voce ha **Dove** (pacchetto/punto d'intervento), **Guadagno** stimato
dai dati, **Verifica** (metrica before/after sullo stesso snapshot). I numeri "ore"
sono ore/settimana misurate sullo snapshot.

---

## 0. IMPLEMENTATO — 10 quick win (2026-10-02, tutti con test verdi, build + gofmt + vet puliti)

Implementati SENZA toccare TTS né traduzioni né la struttura end-to-end. Ogni voce:
file, env opzionale, e la voce della roadmap (§4) che chiude.

| # | Quick win | Dove | Env (default) | Roadmap |
|---|---|---|---|---|
| 1 | Keep-alive residente configurabile | `platform/ollama/client` (`client_keepalive.go`, `client_core.go`, `client_generate.go`) | `PIPELINEGEN_OLLAMA_KEEP_ALIVE` (30m) | B1 |
| 2 | Anti-starvation aging nel claim (+1 priorità per ora di coda; PeekQueued in lockstep) + lane interattiva dedicata (§0.1) | `platform/sqlite/jobs/repository_claims.go`, `repository_jobs_crud.go` + `app/wiring/lifecycle_job_runner.go` | `PIPELINEGEN_INTERACTIVE_WORKERS` (2; 0 = rollback) | A1 (COMPLETA) |
| 3 | Cache identità (path+size+mtime) del probe durate, solo successi | `platform/media/render/probe_cache.go` + wiring `wire_stock_pipeline.go` | — | F3 |
| 4 | Launch order longest-first nel fan-out di localizzazione (risultati restano in ordine editoriale) | `capabilities/localization/service.go` | — | A4 (parziale) |
| 5 | Warm multi-modello all'avvio | `app/wiring/lifecycle_worker.go` | `PIPELINEGEN_WARM_MODELS` (solo modello configurato) | B1 |
| 6 | Gate ricerca Artlist 1→4 (era serializzato su TUTTO il processo; retry 429 invariato) | `capabilities/scripts/adapters/vidrush_registry_searchers.go` | `PIPELINEGEN_ARTLIST_SEARCH_CONCURRENCY` (4) | E3-adjacent |
| 7 | Persist deadline sampler 3s→12s (era < busy_timeout 5s: falliva ogni ~14s in produzione) | `platform/sqlite/performance/resources.go` | — | J1 (parziale) |
| 8 | `active_ms` popolato nella proiezione SQLite (wall − blocked, clamp ≥0; il JSON kernel resta invariato) | `platform/observability/run_recorder.go` | — | J2 |
| 9 | `--concurrent-fragments` yt-dlp (inerte quando aria2c prende il via) | `platform/ytdlp/cmd_builder.go` + `downloader/downloader_ytdlp.go` | `PIPELINEGEN_YTDLP_CONCURRENT_FRAGMENTS` (4) | F2 (parziale) |
| 10 | Client object store dedicato (pool keep-alive, header deadline; era `http.DefaultClient` nudo sul path resumable disk-free) | `platform/drive/uploader_source.go` | — | A3-adjacent |

Note operative:
- Il chunk-size resumable Drive (16MB hardcoded nell'SDK google-api) NON è raggiungibile
  senza fork del client generato: voce A3 resta aperta sul fronte "chunk/parallel upload".
- Test riparato nel passaggio: `TestLocalizedRenderEnqueuer_RejectsMissingRequestedSubtitleLanguage`
  ora abilita i sottotitoli nel fixture (il contratto opt-out `render.subtitles.enabled=false`
  salta legittimamente il requisito lingua quando i sottotitoli NON sono richiesti).
- Applicazione runtime: le voci 1, 5, 6, 9 richiedono solo env (default già più veloci dove
  sicuro: 6 e 9 attivi da subito); il resto è codice attivo al prossimo rebuild + restart di
  `pipelinegen.service`.

### 0.1 A1 completa — lane interattiva + benchmark (2026-10-02)

Lane dedicata per la famiglia latenza-critica nel pool worker, sopra le migrations jobs
REALI (nessun fake):

- **Lane:** pool `interactive` (width 2, `PIPELINEGEN_INTERACTIVE_WORKERS` 0..8, 0 = rollback
  al pool unico pre-lane) che claima SOLO `script.generate` + `script.generate_item`;
  pool `general` = complemento positivo materializzato dalla registry canonica
  (`appjobs.Compose().AllTypes()`), quindi ogni nuovo tipo batch viene ereditato automaticamente.
- **Anti-leak testato:** 5 claim generali sotto backlog 50 non toccano MAI il job interattivo;
  la lane lo claima al PRIMO claim. Con backlog 2.000: primo claim interattivo in ~1ms
  (budget accettato 100ms). Registry sanity: entrambi i tipi lane registrati (lane mai dark).
- **File:** `app/wiring/lifecycle_job_runner.go`, test `lifecycle_interactive_lane_test.go`,
  `lifecycle_interactive_lane_starvation_test.go`, bench `lifecycle_interactive_lane_bench_test.go`.

Numeri misurati (Xeon E3-12xx v2, migrations sqlite_jobs reali, `go test -bench BenchmarkA1_`):

| Prova | Lane (dopo) | Pre-lane (prima) | Effetto |
|---|---|---|---|
| Tempo al primo claim interattivo, backlog 50 | **~1,2 ms** (posizione claim #1) | **~48 ms** (posizione claim #51) | **~40×** a velocità store — in produzione è moltiplicato per il tempo di ESECUZIONE batch (era il wait 3,6h) |
| Primo claim interattivo, backlog 2.000 | ~1 ms (test scala, <100ms budget) | scala lineare col backlog (~2.000 claim) | il pre-lane cresce col backlog, la lane no |
| Churn create+claim, filtro complemento (~30 tipi) | ~1,0 ms/op | ~1,1 ms/op (non filtrato) | il filtro non costa nulla (rumore) |

Comando di replica: `cd refactored && go test -run '^$' -bench 'BenchmarkA1_' ./internal/app/wiring`

### 0.2 A2 completa — fan-out traduzioni a espansione piena (2026-10-02)

Il job `asset.text.materialize` espande ora le 10 lingue IN PARALLELO dentro il job:

- **Dove:** `texttracks/materializer.go` (fan-out errgroup esistente, ora default 10);
  knob su `config.MultilingualConfig.TextTracksFanout` con env
  `PIPELINEGEN_TEXTTRACKS_FANOUT` (clamp 1..16; 1 = rollback sequenziale).
- **Determinismo:** `normalizeReportOrder` ristabilisce l'ordine canonico dei candidati
  nei report (il fan-out parallelo appendeva in ordine di completamento).
- **Effetto atteso (misurato dallo snapshot):** un materialize con 10 lingue a ~6,9s
  di traduzione media passa da ~69s seriali a ~max(per-lingua) ≈ 7–10s — è il nodo
  della starvation 13,26× (`asset.text.materialize` in coda 44,3h per 3,3h di lavoro).
- **Prove:** test a barriera (6 chiamate simultanee o fallimento), cap in-flight,
  rollback=picco 1, ordine deterministico, `-race`, env override via loader config.

### 0.3 Ondata 2026-10-03 — B2, B3, A3, F1, I1 (tutte con test verdi, build+vet+gofmt puliti)

**ATTIVAZIONE IN PRODUZIONE (2026-10-03 08:45-08:50 UTC, finestra senza job in volo):**

- `bin/pipelinegen` ricostruito (`make build-server`) e servizio riavviato;
  `scripts/systemd/pipelinegenctl restart-verify` → **PASS**; il PID registra
  `PIPELINEGEN_DRIVE_UPLOAD_CHUNK_MB=256` (A3 attiva) e
  `PIPELINEGEN_YTDLP_MIRROR_ROOT=/var/cache/pipelinegen/ytdlp-mirror` (F1 attiva);
  env file `/etc/pipelinegen/pipelinegen.env` aggiornato con backup
  (`.bak-20261003T0845Z`). B2 e I1 sono codice attivo del nuovo binario.
- Ollama riavviato con `OLLAMA_NUM_PARALLEL=1` (drop-in deployato + repo
  `scripts/systemd/ollama.service.d/gpu.conf` allineati, backup `.bak-20261003T0845Z`).
  **Misurazione post-restart (gemma4:e2b, ~960 token prompt): il KV prefix cache
  FUNZIONA** — stessa `prompt_eval_count` ma `prompt_eval_duration` 226ms (layout
  legacy) → **49ms (4,6×)** sulla seconda chiamata con prefisso condiviso (il layout
  B3). Era zero riuso con `NUM_PARALLEL=3` (6/6 richieste identiche re-evalutate).
  Trade-off registrato: 1 slot serializza i fan-out a prefisso condiviso piccolo
  (traduzioni); il dominante script-generate (66% del wall, prefisso ~90% nel layout
  B3) ci guadagna.

**ATTIVAZIONE IN PRODUZIONE #2 (2026-10-03 09:35 UTC, dopo SUCCEEDED del job
`job_1791019997722245926_8677626d`):** deploy del fix K2 (drain publish pool
incondizionato, sezione K). `make build-server` (09:34:16Z) →
`pipelinegenctl restart-verify` → **PASS**; nuovo PID 717995 (09:35:11Z).
Evidenza: `runner_document_drain_test.go` RED→GREEN; verify-agent PASS.
**VERIFICA IN PRODUZIONE (journal 10-03 09:36→10:00, PID 717995/758532):**
`voiceover publish pool drained` (runner_phase_document.go:227) appare ANCHE su
run con `DOCUMENT=SKIPPED` (es. run_1791020540094845725, generated=7) — drain
incondizionato attivo. VERIFICA K1 (job_steps + payload 10-01→10-03): 102 run
pt-BR — 22 `DOCUMENT=SKIPPED` + 80 senza step (cancellate/fallite prima del
phase) — ZERO con docs attivo; le uniche 19 con `DOCUMENT=COMPLETED`
(207,2s in 3 giorni) sono collaudi EN mappe/overlay con docs esplicito.
**VERIFICA ORFANI K2 (script `scripts/verify_ptbr_orphans.py`,
`watch_ptbr_orphans.sh`):** sulle prime run post-fix (10-03 10:27/10:32) →
drain `publish_pool_drain` presente DENTRO la run, **0 upload voiceover dopo
il termine**, 0 righe Google Docs, tutti gli step publish COMPLETED;
run pt 10-02 (pre-fix): 0 orfani anche lì (drain sincrono). L'unico publish
post-run osservato è un upload overlay del contratto remote-render (downstream
del final job), non un orfano voiceover.
- **Layout `PIPELINEGEN_OLLAMA_SEGMENT_PROMPT_LAYOUT=shared-prefix` resta OFF**:
  cambia la semantica del prompt e la regola repo esige un canary editoriale
  (budget parole + QA) prima del flip; il knob è pronto e il suo gain misurato.

- **B2 COMPLETA — constrained decoding sugli ultimi due caller JSON scoperti**: `platform/youtube/ollama_clip_metadata_builder.go` (era `SimpleGenerate(..., nil)` e poi parse JSON → retry/fallback spesi su prosa) e `platform/ollama/generate_metadata.go` (era `Chat(..., nil)` + parser tollerante). Ora entrambi portano il vincolo top-level `format: "json"`; il parser tollerante resta come difesa in profondità. I caller JSON già coperti prima: ranker research, analyzer semantici, batch translation, visual planner, enrichment stock. Il path script genera PROSA per contratto (OutputModePlainText) → nessun JSON da vincolare. *Prove:* `TestGenerateVideoMetadataPinsTopLevelJSONFormat`, `TestOllamaClipMetadataBuilderPinsJSONFormat` (fissano il campo wire top-level).
- **B3 CODICE COMPLETA, GAIN GATED SU CONFIG SERVER**: layout shared-prefix env-gated
  (`PIPELINEGEN_OLLAMA_SEGMENT_PROMPT_LAYOUT=shared-prefix`, default `legacy`):
  lo split `buildSegmentHeader`/`buildSegmentBody` è byte-identico al brief legacy
  (testato), l'header condiviso + plainTextInstruction vengono renderizzati PRIMA del
  task template e l'assegnazione per-segmento ULTIMA come istruzione assoluta
  (`segmentAssignmentBlock`), così il run condiviso è KV-cacheabile tra le chiamate
  del fan-out (proprietà pinnata da test: il prefisso comune tra due assegnazioni
  diverse copre l'intero blocco condiviso). **Misurazione live su gemma4:e2b locale
  (2026-10-03): il deployment Ollama 0.30.4 con `OLLAMA_NUM_PARALLEL=3` NON riusa
  MAI il prefisso KV — nemmeno per richieste IDENTICHE (6 ripetizioni: prompt_eval
  identico ogni volta; anche `/api/generate`).** Quindi il riordino da solo non
  produce guadagno su QUESTA deployment: l'azione è operator, non codice —
  `OLLAMA_NUM_PARALLEL=1` (+ restart Ollama, finestra senza job in volo) abilita il
  riuso del prefisso e il layout diventa attivo con lo stesso default. Il batching
  per-cue traduzioni esiste già (`TranslateBatchWithModel`, 12 segmenti/chunk); il
  batching N-scene-per-chiamata resta escluso per contratto editoriale (perde la
  QA per-segmento con retry mirato).
- **A3 COMPLETA — chunk size resumable configurabile**: il TODO assumeva il chunk
  16MB non raggiungibile senza fork dell'SDK; NON è vero per google-api v0.274.0:
  `Media(r, googleapi.ChunkSize(n), googleapi.ContentType(t))` fa upload RESUMABLE
  con chunk a scelta (gensupport: non-singleChunk → ResumableUpload). Knob
  `PIPELINEGEN_DRIVE_UPLOAD_CHUNK_MB` (default 256, clamp 1..1024, multipli 256KiB).
  File > 1 chunk → path Media+ChunkSize (un master ~500MB: ~32 PUT → 2); file ≤ 1
  chunk → path storico `ResumableMedia` (16MB SDK, un PUT riconosciuto) byte-per-byte
  — gli artefatti audio 6–7MiB mantengono le semantica resumable. Il protocollo
  resta resumable (sessione + Content-Range + resume su offset riconosciuto). Il
  chunk parallelo su UNA sessione NON è supportato dal protocollo Drive (PUT
  sequenziali sull'offset): "4–8 chunk concorrenti" è irraggiungibile per protocollo,
  non per SDK. *Prove:* `TestPutFile_BigChunkResumableWire` (5.5MiB @ 1MiB → 6 PUT
  sequenziali, byte tutti consegnati, verifica post-upload verde),
  `TestPutFile_SmallFileKeepsHistoricalResumableRoute`, `TestResumableChunkBytes_EnvContract`.
- **F1 COMPLETA — mirror locale per video-ID**: cache content-addressed nel path
  full-source, **OPT-IN** (`PIPELINEGEN_YTDLP_MIRROR_ROOT` a un path persistente nel
  service environment; unset/empty = disabilitato — una cache condivisa cambia il
  comportamento osservabile di ogni Download e non può essere un default silenzioso;
  il test suite esistente resta ermetico senza env). Chiave = videoID+signature formato (format arg canonico +
  override + merge) → formati diversi non condividono entry. Hit = hard-link (fallback
  copia) + stesso gate `VerifyFile` di un download fresco; entry corrotta = evict +
  redownload. Same-key in volo = singleflight (job concorrenti NON gareggiano su
  YouTube). Solo YouTube full-source: Artlist/generici e le sezioni restano sul path
  rete. *Prove:* `TestMirror_SecondDownloadOfSameVideoServedFromCache` (2 chiamate →
  1 download di rete), `TestMirror_FormatSignatureIsolatesEntries`,
  `TestMirror_NonYouTubeURLNeverMirrored`, `TestMirror_CorruptedEntryEvictedAndRedownloaded`,
  `TestMirror_ConcurrentSameKeySingleFlight`.
- **I1 COMPLETA (quota raggiungibile) — preflight pre-enqueue**: la tassonomia dei
  fallimenti prevedibili era già in gran parte gateata inline durante la
  certificazione Milton (budget audio ±40ms vs audio certificato, durata minima
  scena 100ms con borrowing, codec/shape audio AAC-LC 48kHz stereo + copy_eligible,
  stock reject-list, SSOT gate overlay con frame grid). Aggiunto il gate UNICO
  tipizzato `enforceFinalJobPreflight` in coda a `BuildFinalJobPayloads`: durate
  scena valide (fail-closed su NaN/Inf/zero) e **media locator risolvibile per ogni
  scena** — la classe `images selected = 0, expected 1` che al Milton è costata un
  intero attempt remoto ora fallisce in locale prima della PREPARE. *Prove:*
  `TestFinalJobPreflight_MissingMediaLocatorFailsClosed`, `..._DriveFileIDLocatorIsAccepted`,
  `..._InvalidDurationFailsClosed`, `..._CertifiedAudioMustBePositive`, `..._ValidPayloadAdmitted`.
- **B4 NON RAGGIUNGIBILE via API Ollama (documentato)**: l'API Ollama 0.30.4 non
  espone speculative decoding (nessun draft-model/verify parameter su /api/generate
  o /api/chat; unica API wire in uso). Serve una migrazione inference server
  (llama.cpp server con `--model-draft`, o vLLM). Non implementabile senza cambiare
  il server: la voce resta aperta su quel perno.
- **G1 NON RAGGIUNGIBILE in sicurezza (documentato)**: il concat copy-only di chunk
  AAC codificati indipendentemente porta il priming encoder (~2048 campioni) più
  l'arrotondamento a frame AAC (21,33ms @48kHz) PER OGNI confine: 8 chunk ≈
  170–340ms di drift sulla durata certificata — esattamente la classe di bug dei
  92-scene/frame-alignment già fixata a peso oro. Il guadagno stimato (15,5s → ~3s)
  non giustifica il rischio sul contratto di durata certificata; `-threads:a 0`
  (già applicato, ~0,35s campione) resta l'unico gain sicuro. Da rivisitare solo con
  un concat sample-accurate verificato su QA audio completo.

### 0.4 Ondata 2026-10-05 — strumentazione overlay_render, created_at, diagnosi prepare_join

Snapshot di riferimento: `ops/benchmarks/job-timing-20261005T164155Z.json` (e `job-timing-20261005T153538Z.json` della mattina). Fonti: run_observability / run_stage_observations / run_operation_observations + jobs.db + job_events.

**TREND PRINCIPALE (dove va il tempo ADESSO):** `overlay_render` domina le 48h (43% del lavoro di stage: 2,10h completati + 0,72h falliti) e la sua media è triplicata in una settimana: 74s/run (09-28) → 219s (09-30) → 227s (10-04) → **271s (10-05, max 722s)**. La coda storica (741,9h vs 135,7h wall, 85%) resta il quadro macro ma A1/A2 hanno curato i sintomi su script.generate: max queue 600s (era 12.926s), ollama.generate 0,76h nelle 48h (era dominante).

**1. CORRELAZIONE overlay_render ↔ piani nuovi (misurata, non speculata):**
- Il wall dello stage è composto al ~88% da Σ`renderinggen.render` + encode (medie 48h su 28 run: stage 269,7s = render 223,6s + encode 12,0s + upload 0,4s; submit ~0).
- Il VOLUME di item per run è esploso: **21 ops (run 10-03) → 133 (10-04) → 231 (10-05)**, coerente con i payload brasiliani 10-04/10-05 (`max_important_phrases_per_segment` 1→5, `images_per_scene` 1→2, `max_entities_per_segment` 6→8, piani showcase/heavy-phrase). Un piano che raddoppia gli item raddoppia il wall: il collo è la dimensione del piano, NON la velocità della GPU per item.
- Controesempio wait-dominated: la run 722s del 10-03 ha solo 86s di render (26KB di report) — il resto è attesa GPU lane (le ops del 10-03 portano timeline NULL, la scomposizione fine è possibile solo per le run post-fix).
- Pearson report-size↔durata r=0,508 (n=28): il piano (dimensione del report) spiega metà della varianza; l'altra metà è lane wait.
- **Azione editoriale:** prima di ottimizzare il motore, misurare il costo per-item dei preset heavy-phrase/showcase e decidere se il budget frasi (5 per segmento) vale il wall. **Azione strumentale (FATTA, vedi sotto):** submit/wait ora canonici — le prossime run mostreranno la lane wait esplicita.

**2. STRUMENTAZIONE overlay_render (IMPLEMENTATA 2026-10-05, test verdi):**
- `OperationSubmit` ("submit") e `OperationWaitCompletion` ("wait_completion") entrano nel vocabolario kernel; `recordQueueBoundaryPhases` (render_queue_completion.go) proietta le due metà del boundary misurate dal chiamante sotto StageOverlayRender/component `render_queue`, accanto alle fasi worker (`recordRenderingGenPhases`). Il wall di stage si scompone in submit + wait + Σ fasi worker SENZA un secondo timer: il wait resta anche WaitCompletion run-level (proiezione stage-bound dello stesso intervallo). Regola no-fake-zero: metà non misurata → nessuna riga.
- Fix `created_at`: le righe owner-measured (renderinggen/chronon) arrivavano senza created_at e venivano salvate '' → 33.259 righe NON databili escluse da OGNI query a finestra (evidenziato nella nuova vista `overlay_render` dello snapshot). Il recorder ora timbra la riga alla scrittura quando il kernel la lascia vuota; queued/started/finished restano owner-owned (NULL, mai backfill). Prossime run: scomposizione interrogabile per giorno/finestra.
- `timing_snapshot.py`: `operation_observations` ora è un dict `{operations, overlay_render}`; la vista `overlay_render` somma per componente/operazione del solo stage (boundary vs fasi) e riporta il conteggio delle righe storiche non datate.

**3. DIAGNOSI prepare_join 53% (87 falliti / 163 nelle 48h) — ROOT CAUSE TROVATA:**
- `prepare_join` PROPAGA l'errore del ramo semantic (`generation_handler` fail-closed); il fallimento avviene prima del join: `vidrush semantic certification failed: CERTIFIED=false (IMAGE FANOUT: images available = 0/1, expected at least 1/2/5; CROSS-SCENE REUSE: asset già bound ad altro segmento)`.
- Schema osservato: fallimento certificazione → run attempt 1/3 → deferral 5s → attempt 2 → 3 → budget esaurito → il job muore con errore TERMINALE mascherato `read durable run result: context canceled` (Get(ctx) con ctx già annullato dal runner che ha finito di fallire). 62 dei 75+ messaggi CERTIFIED=false dal 10-04 sono "images available = 1, expected at least 2" = piani dual-image che ricevono 1 immagine.
- La certificazione sta facendo il suo lavoro (rifiuta asset riutilizzati/fotteri); il DIFETTO era di osservabilità: il root cause editoriale era raggiungibile solo dai job_deferred (tentativi 1-2), mai dalla colonna error_code terminale. **FIX APPLICATO:** `deriveErrorCode` classifica `CERTIFIED=false` → `SEMANTIC_CERTIFICATION_FAILED` PRIMA delle euristiche generiche (il "render" nel messaggio lo mandava a ENQUEUE_FAILED). Pin: caso in `TestDeriveErrorCode`.
- Residuo aperto: il `context canceled` terminale rimane fuorviante come MESSAGGIO (il codice ora parla). Proposta per la prossima ondata: in `incompleteRunError`, quando `updated.ErrorMessage != ""` non usare affatto il `getErr` (già fatto) E ritentare la Get con `context.WithoutCancel` nel handler per leggere l'esito reale; non fatto qui perché tocca il path di completamento handler (rischio fuori dal perimetro osservabilità di oggi).
- Sui vidrush.verify/acquire "falliti" (1.167/660): rumore di polling (0,01-0,27s), non incidono sul wall; non azione.

**Verifica in produzione (quando il binario viene ridistribuito):** nuova run brasiliana → `SELECT operation, component, duration_ms FROM run_operation_observations WHERE stage='overlay_render' AND run_id=...` deve mostrare submit/wait_completion/render/encode con created_at popolato; un fallimento certificazione deve esporre error_code=SEMANTIC_CERTIFICATION_FAILED su jobs.error.

```
HTTP submit
  -> script.generate (Ollama gemma4:e2b/e4b, 1 chiamata per segmento)
  -> translation (LLM, per scena x lingua)
  -> TTS edge-tts (per scena, rete) + word-timing
  -> scene_analysis (VisualNER Rust + frasi/parole deterministico)
  -> media plan / stock / internet images
  -> audio compile (Rust mix + AAC) + overlay_render (RenderingGen -> Chronon3d)
  -> final job remoto (worker 51) -> Drive publish
```

Il rendering NON è il collo: `renderinggen.render` 3,6s medi, render loop 77–247 fps,
final remoto 36,5s per 22 minuti di video. Il collo è **attesa + modelli + rete**.

---

## 2. Anatomia al secondo di un job reale (Dolly, 5 scene, 153s audio, EN, 156,7s wall)

| # | Fase | Tempo | Composizione misurata |
|---|---|---|---|
| 1 | generate | **103,3s (66%)** | ollama.warm 38,2s (cold load) + 5× generate 20,0s + 3,6s queue + overhead |
| 2 | render clip locali (GPU, conc. 5) | 57,6s wall | prepare 0,6–2,5s + render_loop 4,4–11,2s + **chronon_queue_wait fino a 17,2s/scene** + startup 5,9s |
| 3 | audio_compile | 43,7s stage | audio_render 12,9s (di cui aac 9,0s) — il resto è join/attesa render |
| 4 | TTS | 20,1s | 5× edge-tts ~4s/scene |
| 5 | audio_publish | 4,8s | upload Drive master |
| 6 | post_writer_finalize | 4,7s | 3× Drive publish (11s lavoro, overlapped) |
| 7 | probe/hash/plan/checkpoint | ~0,7s | già ottimizzato |

Con modello caldo: **~118s**. Dentro i render locali: ~30% del wall è attesa GPU lane,
~20% prepare+startup, solo ~35% render loop vero.

Log sporco: il resource sampler fallisce `context deadline exceeded` ogni ~14s
(contesa SQLite) — vedasi J1.

---

## 3. La settimana per operazione (21–28 set, tutte >0,1h)

| Operazione | Call | Ore | Avg | Max |
|---|---|---|---|---|
| **coda (queue wait, tutti i tipi)** | 984 | **122,9h** | 449s | 12.926s |
| tts.synthesize | 4.925 | 17,8h | 13,0s | 153s |
| worker.execution FAILED | 210 | 10,7h | 184s | 1.511s |
| ollama.generate | 3.232 | 9,4h | 10,4s | 80s |
| translator.translate | 4.079 | 7,9h | 6,9s | 155s |
| youtube download (stock+clip) | ~760 | ~7,7h | 10–35s | 596s |
| chronon.render_clip | 494 | 5,2h | 37,8s | 818s |
| nlp.extract | 4.785 | 4,9h | 3,7s | 339s |
| drive_publish (finalize+upload) | ~7.700 | ~10h | 4–6s | 372s |
| aac_encode | 514 | 2,2h | 15,5s | 74s |
| ollama.warm | 129 | 1,0h | 27,3s | 600s |
| artlist.resolve | 2.183 | 1,9h | 3,1s | 113s |
| google_docs.publish | 520 | 1,1h | 7,7s | 43s |
| remote_final_job | 8 ok / 46 ko | 1,2h | 327s / 61s ko | 664,9s |

Fallimenti per tipo: script.generate 94 job / 2,6h sprecate; youtube_clip.extract
24 / 2,4h; remote_final_job 46. Totale ~5h/settimana di calcolo buttato + 10,7h di
worker.execution fallite.

Tre gap di strumentazione dallo snapshot:

- `active_ms` mai popolato (instrumentation_gap)
- `asset.text.materialize` in coda 13,26× il suo lavoro (44,3h coda / 3,3h lavoro)
- head-of-line blocking: script.generate in coda fino a 3,6h

---

## 4. ROADMAP TOOL

### A. Orchestrazione — il moltiplicatore (~100h/settimana di attesa)

- [x] **A1. Priority lanes nel queue** (COMPLETA 2026-10-02: anti-starvation aging +
      lane interattiva dedicata). Lane: `app/wiring/lifecycle_job_runner.go`
      (`interactiveJobTypes` = script.generate + generate_item; pool interattivo
      width 2; pool generale = complemento positivo dalla registry `appjobs.Compose()`;
      rollback: `PIPELINEGEN_INTERACTIVE_WORKERS=0`). Prevenzione head-of-line:
      nessun batch (materialize/extract/cleanup) può più ritardare una generazione
      utente (misurata: 3,6h). *Prova:* `TestA1_InteractiveLaneNeverStarvesBehindBatchQueue`
      (migrations jobs reali) + benchmark §0.1. *Verifica prod:* p95 `queue_wait_ms` per job_type prima/dopo.
- [x] **A2. Batcher intra-job 10 lingue** (COMPLETA 2026-10-02). Il fan-out parallelo
      per-lingua esisteva già in `texttracks/materializer.go` ma era attivo SOLO con
      `SetConcurrency(4)` hardcoded in un punto (e 1 = seriale altrove). Ora:
      default **10 = espansione completa** (una chiamata in volo per lingua — il job
      paga ~1 traduzione invece di ~10 seriali), knob operator su config SSOT
      `media.multilingual.texttracks_fanout` / env `PIPELINEGEN_TEXTTRACKS_FANOUT`
      (clamp 1..16, 1 = rollback sequenziale documentato), tutti i translator verificati
      concurrency-safe (Argos subprocess per call, ArgosServerTranslator ha il proprio
      semaforo, Ollama è HTTP). Fix determinismo: l'ordine del report segue l'ordine
      canonico dei candidati (`normalizeReportOrder`), non l'ordine di completamento
      delle goroutine. *Prova:* `materializer_fanout_test.go` (barriera: 6 chiamate
      SIMULTANEE o il test fallisce; cap in-flight ≤ limite; rollback 1 = picco 1;
      ordine report deterministico con latenze inverse; `-race` verde) +
      `multilingual_fanout_test.go` (env override end-to-end via loader canonico).
- [x] **A3. Drive uploader chunked** (COMPLETA 2026-10-03, vedi §0.3: chunk size
      configurabile via `PIPELINEGEN_DRIVE_UPLOAD_CHUNK_MB`, default 256MB; il path
      resumable è invariato, i file ≤ 1 chunk tengono il percorso storico). Il chunk
      PARALLELO su una sessione non è supportato dal protocollo Drive (PUT sequenziali
      sull'offset): voce chiusa come raggiungibile. *Guadagno:* un master ~500MB
      passa da ~32 PUT a 2; max 372s di coda PUT sparisce.
- [~] **A4. Scheduler anti-contesa GPU** (PARZIALE: launch order longest-first nel
      fan-out di localizzazione; l'ordinamento nel per-item path resta aperto).

### B. Modello (Ollama) — 11h + 3,9h fallite

- [x] **B1. Demone keep-alive permanente** — gemma4 caricato 24/7 (mlock), warm una
      tantum al boot. *Dove:* `internal/platform/ollama/client` (keep_alive/warm).
      *Guadagno:* −38s a freddo/job; 129 warm/sett spariscono. → IMPLEMENTATO: env
      `PIPELINEGEN_OLLAMA_KEEP_ALIVE` + warm multi-modello (`PIPELINEGEN_WARM_MODELS`).
- [x] **B2. Constrained decoding (JSON mode)** (COMPLETA 2026-10-03, vedi §0.3:
      `format: "json"` top-level su tutti i caller JSON-emitting; il path script è
      prosa per contratto). *Guadagno:* elimina la quota dei 166 generate falliti
      dovuta a JSON malformato (83s medi).
- [~] **B3. Prefix caching + batch segmenti** (CODICE COMPLETA 2026-10-03, §0.3:
      layout shared-prefix env-gated con split byte-identico e proprietà KV pinnata
      da test; **gain attivo solo dopo** `OLLAMA_NUM_PARALLEL=1` + restart Ollama —
      misurato: la deployment attuale non riusa MAI il prefisso, nemmeno per
      richieste identiche). Il batching per-cue traduzioni esiste già (12/chunk);
      N-scene-per-chiamata escluso per contratto editoriale. *Guadagno:* 3–10×
      inferenza sul prompt eval, post-config-operator.
- [ ] **B4. Speculative decoding** (e2b draft → e4b verify). NON raggiungibile via
      API Ollama 0.30.4 (nessun draft-model/verify parameter): richiede migrazione
      inference server (llama.cpp `--model-draft` o vLLM). *Guadagno:* 2–3× (bloccato
      sul cambio server).

### C. Voce — 17,8h/settimana (voce singola più grossa di calcolo)

- [ ] **C1. TTS locale GPU** (Kokoro/Piper/XTTS server): batch di tutte le scene,
      zero rete, zero rate-limit. *Guadagno:* 13s → <0,3s/scena (40–100×);
      17,8h → ~20min; spariscono i 155 falliti di rete.
- [ ] **C2. Word-timing interno** — allineamento CTC/whisperX sul wav prodotto,
      stessi word-timestamps di edge-tts (servono al kinetic typography).
      *Costo:* ~50ms/scena.
- [ ] **C3. Voice cache per lingua+testo-hash** — rerun senza ri-sintetizzare.

### D. Traduzione — 7,9h

- [ ] **D1. MT locale batch** (NLLB-600M / MADLAD-3B su GPU): 10 lingue in un batch,
      deterministico. *Guadagno:* 6,9s → <0,3s/lingua (20–50×); 7,9h → ~15min.

### E. NLP / scene analysis — 4,9h + 1,9h artlist

- [ ] **E1. VisualNER in-process** (FFI link, no stdio spawn).
      *Guadagno:* 3,7s → <0,5s per scena.
- [~] **E2. Frasi pesanti v2 (embedding)** — PARTE NLP IN PRODUZIONE 2026-10-05:
      `rust/pipelinegen-muscles::phrase_impact` (TextRank/coseno; embedding E5 precomputati
      oppure modalità lessicale deterministica) produce `summary` estrattivo,
      `bullet_points` e `heavy_sentences`. L'adapter
      `internal/platform/media/rustexec.PhraseImpactAnalyzer` li espone al runner
      (`runner_phase_script.go`) e `durable_mapper.go` li proietta su
      `GenerateResult`/`GenerationResult` (`summary`/`bullet_points`/`heavy_sentences`);
      un worker rotto degrada senza far fallire il video. Wiring:
      `wirePhraseImpactAnalyzer` + `external.rust_phrase_impact_path`
      (default `bin/phrase_impact`, installato da `make build-muscles`).
      Benchmark per titolo: `go test ./scripts/bench -run '^$' -bench BenchmarkPhraseImpactTitles`.
      Telemetria: il worker riporta il proprio stage breakdown e il runner lo pubblica
      in `script_phrase_impact_stage_seconds` (label `stage`: split|embedding|similarity|
      ranking|summary|bullet|total) — lo stage `embedding` è l'unico non-CPU locale, ed è
      da lì che si misura se conviene la GPU/ONNX.
- [~] **E2b. Peso → motion preset (RenderingGen)** — FATTO 2026-10-05:
      `PlanInput.HeavyPhrasePriority` (default produzione `0.85`, `heavyPhrasePriorityDefault`)
      divide la corsia frasi: le frasi con priorità ≥ soglia prendono un ingresso
      prominente certificato (`selectHeavyPhraseMotion`, sottoinsieme visibile del catalogo),
      le altre restano nella rotazione calma invariata. La priorità è lo `Score`
      dell'annotazione già trasportata dal planner. Default 0 ⇒ piano bit-identico
      (nessun test esistente cambia). NOTA: il peso usato è lo score dell'annotazione,
      NON (ancora) l'`impact_weight` delle `heavy_sentences` del worker Rust: collegare
      quest'ultimo per-frase resta il passo successivo se si vuole che il riassunto
      pesante piloti direttamente l'animazione.
- [ ] **E3. Cache artlist per (frase, lingua)** — resolve ripetuti da cache locale.
      *Guadagno:* 1,9h → ~0.

### F. Ingestion — ~7,7h download + coda

- [ ] **F1. Mirror locale per video-ID** — stesso YouTube mai riscaricato: 1 download
      + cut locali content-addressed (pattern già usato per i background).
      *Guadagno:* stock.youtube_download 5,5h → ~0; clip.extract −60%.
- [~] **F2. Downloader segmentato** (PARZIALE: `--concurrent-fragments` nativo via
      env; aria2c già presente per i full-source). → `PIPELINEGEN_YTDLP_CONCURRENT_FRAGMENTS`.
- [x] **F3. Probe cache** — ffprobe/storyboard per asset-hash, mai due volte.
      *Guadagno:* duration_probe 25,7s avg → 0. → IMPLEMENTATO: cache identità
      `CachedSourceDurationProbe` wired in produzione.

### G. Audio — 2,2h AAC

- [ ] **G1. AAC chunked-parallel** — NON raggiungibile in sicurezza: il concat
      copy-only di chunk AAC indipendenti aggiunge priming encoder + arrotondamento
      a frame AAC per ogni confine (~170–340ms su 8 chunk) contro la durata
      certificata e il gate A/V 40ms (vedi §0.3). `-threads:a 0` già applicato.
      *Guadagno potenziale:* 15,5s → ~3s, bloccato sulla QA sample-accurate.
- [ ] **G2. Mix Rust + encode in un pass** — bassa priorità (già 22× realtime).

### H. Publishing — ~10h

- [ ] **H1. Objectstore locale per gli intermedi** — clip/audio/timing al L3 interno
      (pattern RenderingGen), Drive solo delivery master.
      *Guadagno:* ~60% delle 7.700 chiamate Drive sparisce.
- [ ] **H2. Docs publisher append-mode** — append sequenziali invece di render+publish.
      *Guadagno:* 7,7s → ~2s.

### I. Fallimenti — 10,7h di worker.execution fallite

- [x] **I1. Gate preflight deterministico pre-enqueue** (COMPLETA la quota
      raggiungibile, §0.3: gate unico `enforceFinalJobPreflight` in coda a
      `BuildFinalJobPayloads` — locator media per scena (classe `images selected = 0`)
      + durate valide; budget audio ±40ms, durata minima, codec/shape audio e SSOT
      overlay frame-grid esistevano già come gate inline dalla certificazione Milton.
      Font coverage resta RenderingGen-side, fuori dal perimetro 77). *Guadagno:*
      taglia la quota prevedibile dei 210 falliti (184s medi a retry).
- [ ] **I2. Retry per-checkpoint di stage** — rieseguire solo lo step fallito.
      *Guadagno:* −50% costo residuo falliti.

### K. Sprechi / tagli (verificati a codice, 2026-10-03)

- [x] **K1. `docs.enabled=false` nei payload** — VERIFICATO SICURO per il final job:
  il phase document è già gateato (`runDocumentPhase`: `documentSkipped = ... ||
  !docsEnabled` → zero render HTML, zero publish Google Docs, zero child
  `script.docs_publish`); il parent completa normalmente senza il child. La catena
  overlay→Drive→payload worker 51 è INDIPENDENTE da Docs: gli overlay sono
  pubblicati dal pool `overlayPublicationDrainer` in `complete()` (fuori dal ramo
  docs) e `finalJobOverlayAssets` legge `result.OverlayRender` — nessun riferimento
  ai documenti; `buildRemoteJobPayload`/`BuildFinalJobPayloads` non toccano docs.
  Nessun gate di certificazione richiede il documento (i gate certificati sono
  audio + overlay + preflight). *Costo attuale:* 17,1s inline sul final job Milton
  (2,6% del wall 5m48s), 520 publish × 7,7s = 1,1h/sett, più un giro
  parent→waiting_children→child→aggregator per nulla. *Azione:* zero codice —
  `"docs": {"enabled": false}` nei prossimi payload (i 9 video brasiliani). I
  documenti restano disponibili on-demand rilanciando la singola run con docs
  abilitato (il re-entry è idempotente per run terminal). STATO 2026-10-03:
  **il DEFAULT del codice È GIÀ `false` (opt-in)** — `builder.go` copia
  `item.Docs.Enabled` verbatim dal payload (zero-value = OFF) e
  `ResolveDocsConfig` è `req.Docs.Enabled || req.DocsEnabled`: un payload SENZA
  blocco `docs` non publica documenti, e le `output.languages` restano per la
  traduzione (non sono un trigger docs). VERIFICATO sui file: i payload pt-BR
  10-01/10-02 (isabelle v7-v11, maps_only) GIÀ omettono il blocco → girano
  senza Docs; solo i payload più vecchi (milton 09-29, mike tyson) lo
  portavano esplicito. CONTRATTO PINNATO: `builder_docs_default_test.go`
  (omesso→off, esplicito false→off, esplicito true→opt-in on-demand);
  pin fixture esistente: manifest mike_tyson_1000w ("publishes no Docs
  artifacts"). *Azione residua:* continuare a OMITTERE il blocco `docs` nei
  prossimi payload — nessun codice da cambiare, nessun env da impostare.
  AZIONE COLLAUDI EN (10-03): `docs.enabled` true→false nei payload collaudo EN
  non-docs-dipendenti (celebrity ×2, dolly 13clips/preview/prefetch,
  five_boxers, matt_damon_20clips, chronon-two-stop-map) — 8 file, fixture-test
  intatti, verify-agent PASS. RESTANO con docs (asseriti): matt_damon_5_clips
  (pin `Docs.Enabled` nel manifest test), elon_property (documents asserted),
  elon_overlay_docs_entities + matt_damon_docs_true + dolly_docs_audio_en
  (docs-dedicati), verify_1clip_10lang (pin docs+lingue),
  person_overlay_drive_e2e.sh (e2e del percorso Docs: fallisce senza link
  documento). I payload mappe paris/france/veneto/map-lod NON sono nel repo
  (inviati inline): per i prossimi, omettere il blocco docs.
- [x] **K2. Drain del pool publish voiceover reso INCONDIZIONATO (IMPLEMENTATO 2026-10-03)** —
  CORREZIONE dell'ipotesi iniziale: i link Drive dei voiceover NON hanno il
  documento Google come unico consumatore — sono consumati anche dalla
  cross-run voiceover cache (metadata `timing_json_link` ecc.) e dal commit nel
  registry (voiceoverLifecycle Publisher + AssetIndex). Tagliare gli upload
  avrebbe rotto C3 e il registry: gli upload RESTANO per costruzione. Il difetto
  reale era di CORRETTEZZA: `voiceoverPublishDrainer.Wait()` stava DENTRO il
  ramo `!documentSkipped` di `runDocumentPhase`, quindi con docs off la run
  completava con upload ancora in flight (orfani oltre il proprio ciclo di
  vita, link non idratati). *Fix:* drain sollevato PRIMA del gate
  `documentSkipped` (incondizionato, ultimo phase prima di `complete()`), stage
  `publish_pool_drain` mantenuto. *Test:* `runner_document_drain_test.go` (2
  test: drain anche con docs off; la run NON può completare con un upload in
  flight) — RED sul codice vecchio ("run reached COMPLETED while a voiceover
  publish was still in flight"), GREEN dopo il fix; suite package +
  verify-agent PASS. *Nota sull'ipotesi di risparmio:* le ~7.700 chiamate
  Drive/sett dei voiceover NON sono recuperabili da questo item (servono a
  cache/registry); il guadagno è correttezza (zero upload orfani/ri-lavorazione)
  e la quota Drive si taglia via K1.
- [x] **K3. post_writer_finalize 3× Drive publish — CHIUSO: nulla da tagliare.**
  Verificato a codice: `post_writer_finalize` = `finalizeJob` → spine
  `CompleteWithArtifacts` (ownership `worker_spine`): artifact prepare/hash →
  publish Drive → completion TX unica. I 3 publish sono gli artifact STAGIATI
  DEL JOB (manifest `__artifact_manifest`: script/scenes/final audio…),
  indipendenti da Docs; con K1 non sparisce nulla lì dentro. Nessun publish
  docs-dipendente nel spine.
- [x] **K4. H2 (Docs append-mode) CHIUSA come moot** — con K1 come default
  operativo sui payload (`docs.enabled=false`) il publisher Docs non è più sul
  percorso dei video payload; append-mode (7,7s→2s) non va implementata. Resta
  valida solo per run on-demand con docs abilitato, dove il costo è accettato.

### J. Observability (affinché i numeri restino veri)

- [~] **J1. Fuori da SQLite il path caldo** (PARZIALE: sampler persist deadline
      12s > busy_timeout 5s — fine dei persist falliti; la migrazione DB resta aperta).
- [x] **J2. Popolare `active_ms`** — chiude l'instrumentation_gap dello snapshot.
      → IMPLEMENTATO: proiezione SQLite wall−blocked (JSON kernel invariato).

---

## 5. Ordine di esecuzione (ore recuperate a settimana)

1. **A1 + A2** — ~100h di attesa eliminate, zero rischi sul rendering (A1 COMPLETA §0.1;
      A2 COMPLETA §0.2)
2. **C1 + C2** — −17,8h e meno fallimenti di rete
3. **D1** — −7,9h
4. **B1 + B2** — −4/5h + fine retry JSON
5. **I1** — −3/4h di retry
6. **E2** — qualità editoriale a costo ~zero
7. F1, H1, G1, A3 — i restanti 15–20h

## 6. Regola di verifica

Ogni tool si accetta solo con before/after sullo **stesso snapshot**
(`ops/benchmarks/job-timing-*.json`): la metrica target è la riga dell'operazione
interessata (es. `queue_wait_ms` per A1, `synthesize.duration_ms` per C1,
`translate.duration_ms` per D1). Nessun "sembra più veloce".
