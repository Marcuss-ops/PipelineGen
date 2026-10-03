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

---

## 1. Come funziona oggi (catena)

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
- [ ] **A3. Drive uploader parallelo chunked** — resumable, 4–8 chunk concorrenti,
      connessione persistente. *Guadagno:* 4–6s avg → <1,5s; max 372s sparisce.
- [~] **A4. Scheduler anti-contesa GPU** (PARZIALE: launch order longest-first nel
      fan-out di localizzazione; l'ordinamento nel per-item path resta aperto).

### B. Modello (Ollama) — 11h + 3,9h fallite

- [x] **B1. Demone keep-alive permanente** — gemma4 caricato 24/7 (mlock), warm una
      tantum al boot. *Dove:* `internal/platform/ollama/client` (keep_alive/warm).
      *Guadagno:* −38s a freddo/job; 129 warm/sett spariscono. → IMPLEMENTATO: env
      `PIPELINEGEN_OLLAMA_KEEP_ALIVE` + warm multi-modello (`PIPELINEGEN_WARM_MODELS`).
- [ ] **B2. Constrained decoding (GBNF/JSON schema)** — decoder non può produrre
      JSON malformato → niente retry. *Guadagno:* elimina quota dei 166 generate
      falliti (83s medi).
- [ ] **B3. Prefix caching + batch segmenti** — prompt editoriale condiviso (~90%
      del prompt) in KV-cache; N segmenti per chiamata o continuous batching.
      *Guadagno:* 3–10× inferenza.
- [ ] **B4. Speculative decoding** (e2b draft → e4b verify). *Guadagno:* 2–3×.

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
- [ ] **E2. Frasi pesanti v2 (embedding)** — bge-small ONNX + TextRank coseno →
      `impact_weight` per frase; arricchisce/affianca il lessicono deterministico di
      `internal/capabilities/scripts/phrases`. Costo ~10–30ms. **Qualità, non velocità:**
      peso → preset motion nel catalogo RenderingGen (frasi pesanti = motion animato,
      leggere = statico).
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

- [ ] **G1. AAC chunked-parallel** — 8 chunk in parallelo + concat copy-only
      (il contratto `copy_eligible` già esiste). *Guadagno:* 15,5s → ~3s (5×).
- [ ] **G2. Mix Rust + encode in un pass** — bassa priorità (già 22× realtime).

### H. Publishing — ~10h

- [ ] **H1. Objectstore locale per gli intermedi** — clip/audio/timing al L3 interno
      (pattern RenderingGen), Drive solo delivery master.
      *Guadagno:* ~60% delle 7.700 chiamate Drive sparisce.
- [ ] **H2. Docs publisher append-mode** — append sequenziali invece di render+publish.
      *Guadagno:* 7,7s → ~2s.

### I. Fallimenti — 10,7h di worker.execution fallite

- [ ] **I1. Gate preflight deterministico pre-enqueue** — font coverage,
      frame-alignment finestre clip, budget audio vs scene (quanto risolto a mano per
      Milton → automatico), probe codec prima del submit. *Guadagno:* taglia la quota
      prevedibile dei 210 falliti (184s medi a retry).
- [ ] **I2. Retry per-checkpoint di stage** — rieseguire solo lo step fallito.
      *Guadagno:* −50% costo residuo falliti.

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
