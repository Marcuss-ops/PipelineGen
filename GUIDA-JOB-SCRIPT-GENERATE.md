# Guida A–Z di un job `script.generate` — fasi, tempi misurati, dove velocizzare

Stato dati: **22 ottobre 2026**. Tutti i tempi citati sono misurati da
`run_observability` / `run_stage_observations` / `run_operation_observations`
(`data/observability/api_requests.db.sqlite`) e dai risultati conservati in
`ops/jobs/`. Le fonti sono citate per ogni tabella; nessun numero di questo
documento è stimato se non è esplicitamente marcato come inferenza.

---

## 1. Cos'è un job `script.generate`

Un job `script.generate` trasforma un **payload editoriale** (un JSON
`*.generate.json`: testo sorgente, lingua, stile, vincoli di overlay, asset
stock, impostazioni audio/render) in:

1. un **copione narrato** segmentato per scene (~20 segmenti, ~2.900 parole per
   un video da 20 minuti),
2. la **voce** (TTS per scena),
3. un **piano e i render degli overlay** (frasi, immagini/schede entità, date,
   mappe),
4. l'**audio finale** mixato (voce + BGM con ducking),
5. opzionalmente il **video finale** renderizzato su worker remoto
   (`final_job=true`) e i **documenti** pubblicati.

### Componenti del sistema coinvolti

| Componente | Ruolo |
|---|---|
| `cmd/server` | accetta l'HTTP submit, mette il job in coda |
| `data/jobs/jobs.db.sqlite` | broker job: `jobs`, `job_steps`, `job_registry_metrics`, `job_events` |
| `cmd/worker` | prende in lease il job ed esegue il handler |
| `internal/capabilities/scripts/jobs/generation_handler.go` | handler `script.generate`, orchestrazione |
| `internal/capabilities/scripts/runner_*.go` | il Runner: le fasi di business (vedi §2) |
| `data/media/media.db.sqlite` | registro performance durevole (`performance_runs/steps`) |
| `data/observability/api_requests.db.sqlite` | telemetria run: `run_observability`, `run_stage_observations`, `run_operation_observations` |
| RenderingGen (worker remoto, es. `51.91.11.36`) | render finale video (`remote_final_job`) e lane GPU overlay (`chronon3d.service` via IPC) |
| Google Drive | delivery: VO, audio master, artefatti, cartelle per job |
| Ollama (`gemma4:e4b`) | generazione testo scene |

### Vita di un job (submit → completamento)

```text
POST submit (*.generate.json)
  -> jobs.db: job QUEUED            (correlation_id + idempotency key)
  -> worker lease                    (eventi: job_running, leased)
  -> attempt/run                     (GenerationRun, attempt_N)
       esegue le fasi del §2 con checkpoint dopo ogni fase
  -> CORE_READY                      (milestone: risultato canonico durevole)
  -> remote_final_job (se final_job=true): payload certificato al worker remoto
  -> document / complete_finalize
  -> JobFinalizer: artefatti + manifest  (eventi: job_completed)
  -> post_writer_finalize (dopo il Runner): pubblicazione Drive residua
```

**Retry e resume** (`model_run.go`): `MaxRetries = 3` per run, backoff
esponenziale `RetryDelay` 5 s → 10 s → 20 s … con tetto 120 s.
`ResumeFrom` riparte dallo stage fallito; se il run è a `CORE_READY` riparte
direttamente da `PUBLISHING_DOCUMENTS` senza rigenerare voce e overlay. I
fallimenti deterministici non tipizzati **non** vengono più ritentati
automaticamente (correzione registrata in
`ops/jobs/garlasco_batch_10_20261019_sequential_v7/CHANGELOG.md`).

### Struttura del payload (esempio reale: `02_procedimento.generate.v15.json`)

```text
version, preset, correlation_id, force_refresh
items[1]:
  id, project, title, language, tone, style, model (gemma4:e4b)
  source { type: text, topic, source_text }
  max_phrase_overlays / max_image_overlays / max_map_overlays
  overlay_style (font playfair_display_italic, colori, ombre)
  animation_counts (maps, images, phrases, entities, dates…)
  output { save_to_db, extract_entities, generate_metadata,
           generate_timeline, voiceover_enabled, languages,
           drive_folder_id, drive_subfolder_name,
           render { enabled, subtitles, render_concurrency, require_gpu },
           stock_enabled, stock_bindings[20] }
  media_plan { mode: hybrid, provider_policy, cache, extraction,
               materialization, planner, intro { 3 clip } }
  audio { mode: COMBINED_TIMELINE, timing }
  overlay_background, overlay_group_limits, overlay_categories
  image_motions / entity_image_motions / phrase_motion_family
  map_type, map_camera_animation, max_overlays_per_scene
  script_params { target_words, min_words, segment_words, segments[20] }
  media_mode (es. stock_only)
final_job (true = renderizza anche il video finale remoto)
```

Nota: `animation_counts` e `max_phrase_overlays` sono **finestre di selezione e
tetti**, non quote garantite; il budget frasi reale si misura da
`phrase_overlay_budget` nel risultato (vedi §3.2).

---

## 2. Le fasi canoniche

Ordine garantito: `RunnerPhaseSequence()` in
`internal/capabilities/scripts/runner_execution_pipeline.go`, pinnato da
`runner_phase_sequence_test.go`. I nomi fase sono di proprietà di
`internal/kernel/observability/registry.go` (`AllStages`).

| # | Fase (misurata) | Cosa fa, al millimetro | Operazioni misurate (`run_operation_observations`) |
|---:|---|---|---|
| 1 | `normalize` | normalizzazione e validazione del payload | — (ms) |
| 2 | `MEDIA_PREFLIGHT` | preflight media/asset in ingresso | — (ms) |
| 3 | `begin_vidrush` | wiring del coordinatore VidRush per il fan-out | — (≈0; outlier fino a 317 s) |
| 4 | `generate` | generazione testo scene via LLM: `script.prepare/normalize/validate/plan/engine/postprocess`, una chiamata per scena | `ollama.generate` (avg 13,6 s/call), `ollama.warm` (33,8 s cold) |
| 5 | `emit_scene_commits` | commit durevoli delle scene generate | — (ms) |
| 6 | `scene_analysis` | NLP + risoluzione immagini: estrazione entità/frasi pesanti (worker Rust `phrase_impact`), resolve immagini candidate | `artlist.resolve` (avg 7,0 s), `nlp.extract` (avg 0,6 s) |
| 7 | `translation` | traduzione per lingue aggiuntive, fan-out concorrente (default 10) | `translator.translate` |
| 8 | `voiceover` | TTS per scena (edge-tts) + publish di ogni item VO su Drive | `tts.synthesize` (avg 8,9 s/call), `finalize.drive_publish` (avg 1,06 s) |
| 9 | `prepare_join` | join tra il branch materializzazione immagini/overlay e l'audio; include `stock_prefetch_join` (budget 10 s) | — |
| 10 | `overlay.prepare` | materializzazione del piano overlay | — (ms) |
| 11 | `audio_compile` | piano audio, mix Rust, encode AAC, probe/hash | `rust.audio_render` (avg 23,5 s), `audio.aac_encode` (avg 15,9 s) |
| 12 | `overlay_render` | lane GPU degli overlay: submit → attesa coda → render Chronon → encode → upload objectstore | `render_queue.submit` (0,03 s), `render_queue.wait_completion` (avg 11,4 s), `renderinggen.render` (avg 9,1 s), `encode` (0,33 s), `objectstore_upload` (0,03 s) |
| 13 | `audio_finalize` / `audio_publish` | hash e upload Drive dell'audio master | `drive.upload` (avg 8,5 s) |
| 14 | `persistence` (+ `persistence.sqlite`) | persistenza del risultato canonico | `vidrush.acquire/verify/finalize` |
| 15 | `remote_final_job` | solo `final_job=true`: payload certificato al worker remoto (RenderingGen), attesa del render finale | esterno (115–302 s nei Garlasco; max osservato 5.172 s) |
| 16 | `document` (+ `publish_pool_drain`) | pubblicazione documenti (child `script.docs_publish`, oppure inline) | `google_docs.publish` |
| 17 | `complete_finalize` | chiusura del run | — (ms) |
| 18 | `post_writer_finalize` | DOPO il Runner (worker): finalizzazione artefatti e Drive residui | `finalize.drive_publish` (avg 6,2 s), `artifact_prepare/hash`, `completion_tx` |

Sotto-passi misurati dentro `voiceover`: `tts`, `publish`, `finalize`,
`checkpoint` — compaiono come righe `run_stage_observations` separate ma sono
annidate nella fase `voiceover`: **non sommarle alle fasi principali**, doppiano
il wall.

Milestone (non fasi): `CORE_READY`. Fuori dal Runner: la lane cover/thumbnail
(non è una fase di questo pipeline).

---

## 3. I tempi reali — batch Garlasco 10 video (19 ott 2026)

Fonte: `run_observability` per i job elencati in
`ops/jobs/garlasco_batch_10_20261019_sequential_v7/manifest.json`; payload da
~2.900 parole, 20 segmenti, 3 clip intro, `final_job=true`, consegna
sequenziale. Wall = tempo del run, **coda esclusa** (nei Garlasco la coda è
stata ≤1 s grazie alla lane interattiva; eccezione: il secondo tentativo di 01
ha atteso 1.713 s in coda prima di fallire).

| Job | Wall tentativo riuscito | generate | scene_analysis | voiceover | audio_compile | overlay_render | Altri tentativi |
|---|---:|---:|---:|---:|---:|---:|---|
| 01_cronologia | 306,7 s | 0 s* | 151,2 s | 0 s* | 65,0 s | 114,6 s | 2 falliti: **1.384 s** (generate 1.254 s) + 219 s |
| 02_procedimento | 372,1 s | 134,9 s | 5,8 s | 44,9 s | 97,5 s | 148,1 s | — |
| 03_stasi_sentenza | 488,6 s | 246,1 s | 7,2 s | 114,3 s | 95,0 s | 80,7 s | — |
| 04_nuova_indagine | 644,1 s | 118,9 s | 4,8 s | 43,1 s | 90,6 s | **437,3 s** | — |
| 05_le_prove | 309,2 s | 118,4 s | 4,9 s | 68,9 s | 84,7 s | 73,4 s | — |
| 06_pedali_ris | 413,3 s | 135,0 s | 7,0 s | 70,3 s | 76,0 s | 163,4 s | — |
| 07_presunzione | 306,1 s | 116,8 s | 4,7 s | 61,0 s | 91,0 s | 75,1 s | — |
| 08_fonti_media | 696,8 s | 137,6 s | 4,8 s | 61,9 s | 90,5 s | **454,2 s** | — |
| 09_chiara_poggi | 587,5 s | 117,2 s | 4,8 s | 60,6 s | 96,0 s | **369,2 s** | — |
| 10_cosa_sappiamo | 313,9 s | 114,0 s | 5,0 s | 59,7 s | 89,8 s | 94,4 s | — |

\* 01 è ripreso dal checkpoint: il primo tentativo ha bruciato 20,9 minuti in
`generate` prima di fallire; il tentativo riuscito ha saltato generate/voiceover
(rigenerati nei tentativi 2–3) e pagato `scene_analysis` 151 s +
`prepare_join` 152,7 s.

Media wall del tentativo riuscito: **444 s** (mediana 393 s). Tempo reale
dalla submit al completamento per 02: 6 m 13 s (18:46:24 → 18:52:37 UTC, da
`job_events` nel `results/*.job.full.json`).

### 3.1 La coda di `overlay_render` è il collo di bottiglia della batch

Decomposizione per job (somma delle `run_operation_observations` di fase
`overlay_render` rispetto al wall della fase):

| Job | Wall ovl | render Chronon | attesa coda GPU | upload+altro | spiegato | **non spiegato** |
|---|---:|---:|---:|---:|---:|---:|
| 01 | 116,0 s | 78,5 s | 113,8 s* | 6,3 s | ≈tutto | — |
| 02 | 148,1 s | 59,9 s | 63,3 s | 4,3 s | 127 s | ~21 s |
| 03 | 80,7 s | 49,0 s | 53,6 s | 5,5 s | ≈tutto | — |
| 04 | 437,3 s | 54,7 s | 57,6 s | 3,9 s | 116 s | **~321 s** |
| 05 | 73,4 s | 33,7 s | 35,3 s | 2,2 s | ≈tutto | — |
| 06 | 163,4 s | 43,5 s | 45,7 s | 3,1 s | 92 s | ~71 s |
| 07 | 75,1 s | 41,1 s | 43,1 s | 2,9 s | ≈tutto | — |
| 08 | 454,2 s | 41,0 s | 43,2 s | 2,5 s | 87 s | **~367 s** |
| 09 | 369,2 s | 60,8 s | 63,6 s | 3,7 s | 128 s | **~241 s** |
| 10 | 94,4 s | 56,9 s | 59,8 s | 3,7 s | ≈tutto | — |

\* render e attesa coda si sovrappongono (coda misurata per item), non vanno
sommati.

Il residuo (~320 s su 04, ~370 s su 08, ~240 s su 09) **non** è render GPU né
upload misurato. **Verifica eseguita il 22/10 con la metrica `overlay_upload`
(build `8423ea114`): la pubblicazione Drive seriale NON è la causa dominante.**
La timeline per-item mostra che il residuo è concentrato in uno-due item
pesanti: 08 → item 12 blocca 383,5 s (373,3 s non misurate) e item 13 altri
40,2 s, mentre gli item 1–11 chiudono in 2,0–5,5 s con ~0 s non misurati
ciascuno; 05 (13 item, wall 73,4 s) ha il solo residuo di 37,9 s sull'item 13.
Una pubblicazione seriale per-item produrrebbe N piccoli gap, non un blocco
unico da 373 s. La controprova v1 (percorso seriale pre-fix, 39 item) limita
l'overhead non-render a ~2,6 s/item, publish Drive incluso. La causa dominante
è il lavoro per-item **non proiettato nella telemetria** delle voci pesanti
(mappe: 42–90 s/item con prefetch tile/prepare/encode visibili solo nei log del
worker). Fonte: `docs/operations/TICKET-OVERLAY-THROUGHPUT-100-MIN.md`.

### 3.2 Quello che la batch Garlasco insegna sui retry

Dal `manifest.json` e dal `CHANGELOG.md` (costi reali di rilavorazione):

- `01_cronologia`: 2 tentativi falliti prima del riuscito — **~27 minuti
  bruciati**, di cui 20,9 min in `generate` (fallimento deterministico
  ripetuto: scena clip-only senza clip certificata → `PUBLISHING_DOCUMENTS`).
- `final_video_attempts`: ulteriori failure deterministici — overlay
  entity-card sovrapposto a date-card in modalità replace (fix: scheduler di
  frame con finestra non sovrapposta), mappe duplicate e crash del renderer
  mappe dinamiche (fix operativo: `max_overlays_per_scene=1` / mappe
  disabilitate, a scapito del conteggio overlay).
- introfix r5: la lane di render localizzata brucia sempre le sottotitoli →
  intro con sottotitoli disabilitati falliva su transcript senza cue (fix in
  `runner_render_units.go`: scene a media fisso accodate solo con
  `Render.Subtitles` attivo).
- Le quote editoriali: 13–36 overlay materializzati contro il target 40;
  `phrase_overlay_shortfall` 9–11 su nove job su dieci.

**Regola:** ogni retry deterministico costa più dell'intero percorso felice.
La prima leva di velocizzazione è non fare lavoro che fallirà.

---

## 4. I tempi reali — introfix Garlasco (20 ott 2026, `final_job=true` con intro)

| Job | Wall | generate | voiceover | audio_compile | overlay_render | remote_final_job |
|---|---:|---:|---:|---:|---:|---:|
| 01 (r6) | 1.536,5 s | **1.240,9 s** | 1,8 s | 71,6 s | 137,7 s | 115,7 s |
| 02 (r7) | 1.191,4 s | 128,9 s | 68,6 s | 110,9 s | **690,9 s** | 250,9 s |
| 03 (r8) | 677,9 s | 189,1 s | 82,4 s | 71,7 s | 47,9 s* | 301,9 s |
| 04 (r8) | 558,7 s | 111,0 s | 55,8 s | 83,3 s | 129,8 s | 217,6 s |

\* 03 con `max_overlays_per_scene=1` (solo 9 overlay) per superare i conflitti
di finestra; 04 con mappe disabilitate. Overlay finali: 30 / 27 / 9 / 29.

Osservazioni:
- `remote_final_job` (render finale remoto) è **attesa esterna**: 115–302 s,
  media 221 s. Concorre con poco locale; si riduce solo con più worker remoti
  o render più veloci.
- 02: `overlay_render` 690,9 s per 27 overlay = 2,3 overlay/min — il caso peggiore.
- 01: `generate` 1.240,9 s con voiceover 1,8 s = rigenerazione testo con
  cache audio: il modello LLM è la variabile dominante quando non è caldo o
  quando il piano viene rifatto.

---

## 5. Il quadro aggregato (1.840 run `script.generate`, fino al 22 ott)

Fonte: `python3 ops/jobs/timing_snapshot.py` + query su `run_observability`.
Finestra osservabilità: 2026-10-14 → 2026-10-22.

**Coda vs lavoro (solo `script.generate`):** queue **87,7 h** vs wall **58,2 h**
(blocked 16,8 h; operazioni accumulate 88,7 h). Su tutti i tipi: queue 103,3 h
vs wall 75,4 h — **il 57,8% del tempo cronologico è attesa in coda**, non
calcolo. Worst case di head-of-line: `script.generate` in coda **7.858 s**
(2 h 11). `script.generate_item` attende 6,94× il proprio wall.

**Costo dei fallimenti:** 23,6 h di esecuzione terminata male, di cui
**15,0 h su 472 job `script.generate` falliti** + 6,9 h `youtube_clip.extract`.

### 5.1 Wall per fase (tutti i run `script.generate`; voci principali)

| Fase | n run | media | p50 | p90 | max | totale |
|---|---:|---:|---:|---:|---:|---:|
| overlay_render | 813 | 86,9 s | 12,9 s | 236,9 s | 4.407,6 s | **19,6 h** |
| generate | 1.048 | 36,7 s | 2,6 s | 97,0 s | 2.446,4 s | 10,7 h |
| scene_analysis | 797 | 39,0 s | 11,1 s | 120,2 s | 1.213,9 s | 8,6 h |
| prepare_join | 969 | 26,5 s | 0,3 s | 95,5 s | 303,9 s | 7,1 h |
| remote_final_job | 146 | 172,4 s | 7,8 s | 254,2 s | 5.172,3 s | 7,0 h |
| audio_compile | 897 | 22,5 s | 11,6 s | 67,4 s | 633,2 s | 5,6 h |
| voiceover | 1.014 | 16,7 s | 2,6 s | 50,0 s | 338,1 s | 4,7 h |
| audio_publish | 547 | 6,8 s | 5,3 s | 14,6 s | 147,3 s | 1,0 h |
| post_writer_finalize | 1.046 | 3,2 s | 0,0 s | 8,1 s | 64,8 s | 0,9 h |

(voci annidate dentro `voiceover`: `publish` 10,9 h totali, `tts` 4,2 h — da
leggere come decomposizione, non come fase aggiuntiva.)

### 5.2 Lavoro per operazione (accumulato, tutte le fasi)

| Operazione | n | Totale | Media | Note |
|---|---:|---:|---:|---|
| `scene_analysis/artlist.resolve` | 8.789 | **17,2 h** | 7,0 s | risoluzione immagini/asset |
| `voiceover/tts.synthesize` | 6.255 | **15,4 h** | 8,9 s | rete edge-tts |
| `overlay_render/render_queue.wait_completion` | 4.789 | **15,1 h** | 11,4 s | attesa lane GPU |
| `overlay_render/renderinggen.render` | 4.789 | **12,1 h** | 9,1 s | render Chronon vero |
| `generate/ollama.generate` | 2.342 | 8,9 h | 13,6 s | di cui 4,2 h di coda interna |
| `audio_compile/rust.audio_render` | 680 | 4,4 h | 23,5 s | mix finale |
| `publish/finalize.drive_publish` | 12.133 | 3,6 h | 1,1 s | VO verso Drive |
| `persist/vidrush.finalize` | 11.699 | 3,2 h | 1,0 s | |
| `audio_compile/aac_encode` | 509 | 2,3 h | 15,9 s | |
| `post_writer_finalize/drive_publish` | 1.221 | 2,1 h | 6,2 s | |
| `acquire/vidrush.acquire` + `verify` | 27.617 | 3,0 h | ~0,4 s | |
| `audio_publish/drive.upload` | 436 | 1,0 h | 8,5 s | master audio |

Lettura: **il tempo utile è ~35% del wall dentro la lane render** (render loop
vero), il resto è attesa coda GPU, prepare/startup e pubblicazione.

---

## 6. I costi nascosti, voce per voce

Le voci che nelle tabelle precedenti restano aggregate o senza voce propria,
misurate su tutti i 156.989 record di `run_operation_observations` e sui
`audio_metrics` dei risultati Garlasco.

### 6.1 Upload e pubblicazione su Drive

| Operazione | n | Totale | Media | Dati trasferiti |
|---|---:|---:|---:|---:|
| `publish/finalize/drive_publish` | 12.721 | **4,12 h** | 1,17 s | 9,1 GB |
| `post_writer_finalize/finalize/drive_publish` | 1.221 | **2,09 h** | 6,15 s | 2,5 GB |
| `audio_publish/drive/upload` | 436 | 1,03 h | 8,51 s | (non misurati) |
| `publish/drive/upload` | 588 | 0,57 h | 3,47 s | 1,8 GB |
| `publish/finalize/artifact_prepare` | 12.733 | 0,04 h | 0,01 s | 9,2 GB letti |
| `publish/finalize/artifact_hash` | 12.730 | 0,03 h | 0,01 s | 9,2 GB hashed |

- **Totale upload/publish Drive: ≈7,8 h su ~15.000 chiamate**, più ~25.000
  chiamate di prepare/hash a costo quasi nullo (ma leggono/hashano ~9 GB).
- Per job Garlasco: upload audio master 11,3–18,8 s (`audio_metrics.upload_ms`),
  `post_writer_finalize` 14,1–25,0 s, ~40 publish VO da 1,06 s ciascuno dentro
  `voiceover`.
- Chunked upload già applicato (A3): un master ~500 MB passa da ~32 PUT a 2.
- La pubblicazione Drive **seriale** dopo ogni render nel percorso `final_job`
  è stata rimossa (commit `5538ed8d6`, attivo dal build `8423ea114`) e misurata
  con `overlay_upload`: prime misure 2,3 ms/item sul percorso async (enqueue
  outbox), quota per-item comunque piccola (~2–5 s/item). La verifica ha
  scagionato la pubblicazione come causa del residuo di `overlay_render` (§3.1:
  concentrato negli item pesanti). Gap residuo: l'upload reale nel worker
  outbox (`overlay_drive_outbox.go`) resta non misurato.
- Leva: H1 (objectstore locale per gli intermedi) taglia ~60% delle chiamate.

### 6.2 Ricerca immagini (browser + searching)

La catena certificata (`internal/capabilities/imagesearch`): FRASE → estrazione
semantica/entità → canonicalizzazione (`person:chiara-poggi`) → query di
ricerca → risultati → validazione rilevanza → immagine scelta. Il decisore è
deterministico (knowledge base curata, nessun modello esterno).

| Voce | Misura |
|---|---|
| `scene_analysis/artlist.resolve` | 8.830 chiamate, **17,27 h** — la singola voce più grossa della pipeline; media 7,04 s, max 283 s |
| Ricerca Artlist | **browser-backed e serializzata**: `acquireSearchSlot` (`internal/platform/artlist/scraper/scraper.go`) è un semaforo a 1 slot — l'anti-bot di Artlist restituisce challenge se più query aprono il browser insieme. Sorgente `browser_api` |
| `searcher_cache` | deduplica le richieste identiche (una sola ricerca browser per query), ma `cache_hit`/`cache_status` **non risultano mai popolati** per artlist: il riuso reale oggi non è misurabile (gap di strumentazione) |
| Internet images (DDG catalog) | `ProcessorInternetImages`, misurato in `internet_image_search_ms` nel risultato; nei Garlasco è `BYPASSED` (`media_mode=stock_only`): **questo costo è fuori dalla finestra Garlasco** |

Attenzione al parallelismo: il collo non è la CPU ma il gate serializzato del
browser. Aggiungere worker non accelera questa voce; serve cache per
(frase, lingua) — leva E3 — o un searcher non browser per le query ripetute.

### 6.3 Estrazione entità e analisi semantica (Rust)

| Voce | Misura |
|---|---|
| `scene_analysis/nlp.extract` | 8.961 chiamate, 1,56 h; media **0,63 s** (max 305,6 s = anomalia) — estrattore conservativo CPU + knowledge base deterministica (PERSON/ORG/PRODUCT/LOCATION/GPE… con disambiguazione), nessun modello esterno |
| Rust `phrase_impact` | BrainRust release worker (`bin/phrase_impact`, installed by `make rust-fetch`): TextRank/coseno + embedding E5 precomputati o modalità lessicale deterministica; produce `summary` estrattivo, `bullet_points`, `heavy_sentences`. Metriche per stadio in `script_phrase_impact_stage_seconds` (split\|embedding\|similarity\|ranking\|summary\|bullet\|total). Un worker rotto **degrada senza far fallire il video** |
| `audio_compile/rust.audio_render` | 683 chiamate, 4,51 h; media 23,8 s (max 610,9 s) — il mix Rust dell'intera timeline audio |

Le schede entità renderizzate rientrano in `overlay_render` (non hanno voce
propria): il loro costo di immagine è dentro `artlist.resolve` + il render
dell'overlay.

### 6.4 TTS — il costo voce completo

| Voce | Misura |
|---|---|
| `voiceover/tts.synthesize` (edge-tts) | 6.255 chiamate, **15,43 h**; media 8,88 s/call, max 300 s (`provider` = lingua, es. `it`) |
| `voiceover/publish` (VO → Drive) | ~2 item/scena da 1,06 s |
| `tts_publish_drain` | ≈0 (drenaggio già overlappato) |

Per job Garlasco (20 scene, ~21 min di audio), da `audio_metrics` nei risultati:

| Job | tts_ms (accumulato) | aac_encode_ms | upload_ms | total_ms (wall) | audio (s) | RTF |
|---|---:|---:|---:|---:|---:|---:|
| 01_cronologia | 203,6 s | 57,1 s | 15,3 s | 64,8 s | 1.187 | 0,055 |
| 02_procedimento | 165,5 s | 70,6 s | 16,3 s | 89,1 s | 1.219 | 0,073 |
| 03_stasi_sentenza | **429,6 s** | 81,8 s | 11,3 s | 89,6 s | 1.313 | 0,068 |
| 04_nuova_indagine | 159,7 s | 77,0 s | 18,8 s | 83,2 s | 1.315 | 0,063 |
| 05_le_prove | 255,4 s | 68,0 s | 18,7 s | 80,7 s | 1.307 | 0,062 |
| 06_pedali_ris | 266,1 s | 55,9 s | 13,2 s | 64,7 s | 1.316 | 0,049 |
| 07_presunzione | 228,9 s | 81,2 s | 14,9 s | 87,1 s | 1.292 | 0,067 |
| 08_fonti_media | 233,2 s | 75,4 s | 15,4 s | 83,1 s | 1.300 | 0,064 |
| 09_chiara_poggi | 227,2 s | 83,8 s | 18,3 s | 91,9 s | 1.266 | 0,073 |
| 10_cosa_sappiamo | 219,6 s | 70,1 s | 14,4 s | 85,3 s | 1.264 | 0,068 |

- `tts_ms` è **accumulato** sulle 20 chiamate (concorrenza interna),
  `total_ms` è il wall dell'intera pipeline audio: ~65–96 s per 20–22 minuti di
  audio = **13–20× realtime** (RTF 0,049–0,075).
- Il wall della fase `voiceover` osservata nei run è 43–114 s: coerente.
- Prova che la cache funziona quando scatta: `02_procedimento.phrasefix.recheck`
  ha risintetizzato 1 scena su 20 (tts_ms 8,4 s contro ~200 s).
- Leve: C1 TTS locale GPU (13 s → <0,3 s/scena, 40–100×), C2 word-timing
  interno (~50 ms/scena), C3 voice cache per (lingua, hash testo).

### 6.5 Gli altri costi nascosti

| Voce | Misura | Nota |
|---|---|---|
| Bookkeeping VidRush | `acquire` 1,72 h + `verify` 1,28 h + `finalize` 3,24 h = **6,24 h** su ~39.300 chiamate | persistenza durevole per asset: ~0,4–1 s/chiamata, non eliminabile |
| Coda interna di Ollama | **4,17 h** delle 8,86 h di `ollama.generate` sono `queue_wait_ms` (2.057 chiamate con attesa) | quasi metà del tempo LLM è attesa del modello, non inferenza; il prefix caching (B3) la riduce |
| Cold start modello | `ollama.warm` 25 chiamate, media 33,8 s (max 79,8 s) | già eliminato dal keep-alive (B1) |
| Checkpoint | 10.662 righe, 0,07 h totali, max 74,2 s | quasi gratis; il picco è contesa SQLite |
| Contesa SQLite / resource sampler | il sampler risorse fallisce `context deadline exceeded` ogni ~14 s | rumore di log + sintomo del single-writer SQLite (TODO §2, J1) |
| Docs publish | `google_docs.publish` 145 chiamate, 0,26 h; media 6,5 s (max 185,3 s) | leve H2 (append-mode) |
| Idratazione sorgente | `sqlite.hydrate` 900 chiamate 0,18 h; `qdrant.search` 30 chiamate 2,1 s | trascurabile |
| Traduzione | **0** nei Garlasco (single-language); con più lingue `translator.translate` ~7,9 h/settimana, media 6,9 s | fan-out 10 già attivo (A2); leva D1 (MT locale) |
| AAC master | `aac_encode` 55,9–87,5 s per ~21 min di audio (media op 16,3 s) | G1 chunked bloccato dalla QA sample-accurate |
| Residuo `overlay_render` | fino a ~370 s/job concentrati in 1–2 item (§3.1) | fasi per-item non strumentate di mappe/immagini — **NON** la pubblicazione Drive (verificato con `overlay_upload`, 22/10) |
| Lavoro sprecato dai fallimenti | 23,6 h, di cui 15 h su `script.generate` (§5) | leve I1/I2 |
| Attesa in coda | 87,7 h vs 58,2 h di lavoro (§5) | leve A1 estese |

---

## 7. Cosa insegna il confronto Garlasco ↔ aggregato

1. **`overlay_render` è la voce n.1** (19,6 h) e nei job con item pesanti mostra
   un residuo concentrato in 1–2 item (§3.1). La verifica `overlay_upload` del
   22/10 ha scagionato la pubblicazione Drive seriale (quota ~2–5 s/item, già
   rimossa e misurata) e puntato alle **fasi per-item non strumentate delle
   mappe/immagini** (42–90 s/item): è lì che va misurato e ottimizzato, prima
   di aggiungere parallelismo GPU.
2. **`generate` (LLM) è la voce n.2** e la più imprevedibile: p50 2,6 s (cache)
   ma p90 97 s e max 40 min. Il keep-alive del modello (B1) elimina il cold
   start da 38 s; il prefix caching (B3) è pronto ma serve
   `OLLAMA_NUM_PARALLEL=1` + restart Ollama per attivarlo.
3. **TTS su rete** (15,4 h) e **artlist.resolve** (17,2 h) sono i due grandi
   costi esterni ripetitivi: cache per (testo, lingua) e TTS locale GPU sono
   le leve a maggiore rendimento (40–100× stimati, vedi roadmap).
4. **La coda è più grande del calcolo** (87,7 h vs 58,2 h): con più throughput
   per job non si scala il batch se non si alza la concorrenza/lane.
5. **I fallimenti costano quanto i successi** (15 h sprecate su
   `script.generate`): gate deterministici pre-enqueue (I1, parzialmente fatto)
   e retry per-checkpoint (I2, aperto) sono ottimizzazioni di tempo quanto lo
   è rendere più veloce il render.
6. **`remote_final_job` non è comprimibile localmente**: è attesa di un worker
   esterno (media 221 s nei Garlasco).

---

## 8. Leve di velocizzazione — ordine consigliato per guadagno atteso

Riferimento completo: `TODO-pipeline-100x-velocita.md` (roadmap A–K con stato
di ogni voce) e `docs/operations/TICKET-OVERLAY-THROUGHPUT-100-MIN.md`
(obiettivo 100 overlay/min; baseline misurata 27,2/min su 3 run, vedi ticket).

| # | Leva | Stato | Guadagno atteso | Dove |
|---|---|---|---|---|
| 1 | Gate deterministici pre-enqueue (I1) + retry per-checkpoint (I2) | I1 parziale, I2 aperto | −15 h/finestra di fallimenti; −50% costo retry | `final_job_payload.go`, `runner_lifecycle.go` |
| 2 | Pool Drive fuori dal critical path + metrica `overlay_upload` | **fatto e misurato** (22/10): 2,3 ms/item async | piccolo (~2–5 s/item), già preso | `render_queue.go`, `runner_lifecycle.go` |
| 2b | Strumentare e ottimizzare le fasi per-item degli item pesanti (prefetch/prepare/encode mappe) | **strumentato** (22/10: fasi proiettate in `run_operation_observations`, mappe misurate a ~20 s/item); **ottimizzazione aperta** | fino a ~20–60 s/finestra su piani con mappe | pipeline mappe / `render_queue.go` |
| 2c | Misurare l'upload Drive nel worker outbox (handler senza `MeasureOperation`) | **fatto e misurato** (22/10): 2,4–3,0 s/item reali, asincroni fuori finestra | gap di attribuzione chiuso | `internal/app/wiring/overlay_drive_outbox.go` |
| 3 | TTS locale GPU (Kokoro/Piper/XTTS) + voice cache (C1/C3) | aperto | 13 s → <0,3 s/scena (40–100×); 15,4 h → ~20 min | `internal/capabilities/voiceover` |
| 4 | Cache artlist per (frase, lingua) (E3) | aperto | 17,2 h → ~0 sul resolve ripetuto | scene_analysis |
| 5 | Prefix caching Ollama (B3) + `OLLAMA_NUM_PARALLEL=1` | codice pronto, config da verificare | 3–10× su inferenza prompt | `internal/platform/ollama` |
| 6 | Objectstore locale per gli intermedi (H1) | aperto | −60% delle ~12k chiamate Drive | publish |
| 7 | Più lane per la coda / anti-starvation (A1 esteso) | lane interattiva attiva | toglie il head-of-line da 2+ h | `app/wiring/lifecycle_job_runner.go` |
| 7b | **2ª lane di render (2° daemon Chronon + socket spread + pool item 2)** | **fatto e misurato (22/10): 27,2 → 37,8 overlay/min (+39%, overlap 1,89×)** | il collo reale della finestra overlay è il servizio render seriale; N lane ≈ N× fino alla saturazione NVENC | `chronon3d-b.service`, `/etc/renderinggen/renderinggen.yaml`, `render_queue_items.go` |
| 8 | MT locale batch (D1), VisualNER in-process (E1), mirror ingestion (F1) | aperti | 7,9 h + 4,9 h + 7,7 h/settimana | vari |
| 9 | Speculative decoding (B4), AAC chunked (G1) | bloccati (API Ollama / QA sample-accurate) | 2–3× / 15,5 s → ~3 s | — |

**Contratto di misura** (dal ticket, da rispettare per ogni ottimizzazione):
`throughput = overlay validati / minuti di wall della finestra`; un overlay
conta solo se materializzato, renderizzato e superato ai controlli. Salvare per
ogni run: `run_id`, hash input, revisione, worker, orari UTC, conteggi
richiesti/pianificati/materializzati/validati, durate per fase, wait di coda,
retry, hash output. Non confrontare finestre diverse come equivalenti.

**Golden case** (baseline overlay-only): payload immutabile
`ops/jobs/overlay_throughput_golden_100_20261022.v2.generate.json` (SHA-256
`60fb14…d63080d`), derivato da `08_fonti_media.generate.v11.json`: 25 frasi
brevi + 25 lunghe + 30 immagini + 10 date + 10 mappe, `max_overlays_per_scene=10`,
`final_job=false`. **Baseline misurata (22/10, 3 run confrontabili v8/v9/v10,
1 lane render): mediana 27,2 overlay/min sulla finestra render (range
25,4–29,5). Dopo la seconda lane di render (2° daemon Chronon +
`overlayItemDeepWidth=2`, run v11): 37,8 overlay/min (+39%, finestra 60,4 s,
overlap render 1,89×).** Target 100/min non raggiunto: servono ~4 lane efficaci
o servizio render ~1,5 s/item (oggi ~3,0 s/item). Record e comando di
ripetizione: `ops/jobs/overlay_throughput_golden_100_20261022/results/`
(con README, incluse le condizioni di confrontabilità — le run a lane diverse
non vanno confrontate).

**Condizioni di una run confrontabile** (valevole per ogni before/after):

1. stesso payload immutabile (verificarne lo SHA-256 prima del submit) e nuova
   chiave di idempotency per ogni run;
2. ObjectStore L3 con tutti i binding immagini verificati prima del submit
   (query in `results/README.md`): uno scarto corrompe la run e non va contato;
3. stessa macchina a riposo (nessun altro job in esecuzione) e stessa build;
   annotare `git_commit` e SHA-256 del binario;
4. esito `SUCCEEDED` con tutti gli item COMPLETED, altrimenti la run non
   entra nella baseline (registrarla comunque con la causa);
5. non confrontare run con un tentativo interrotto (es. reboot): il retry
   riparte da zero e i timing API non sono affidabili.

**Comandi di misura** (le due sorgenti canoniche, non l'API aggregata):

```bash
# fasi di job e sotto-operazioni (incluse prefetch/prepare/encode mappe e
# upload Drive reali dell'outbox) — run di un job:
sqlite3 'file:data/observability/runs.db.sqlite?mode=ro' \
  "SELECT run_id, stage, wall_ms FROM run_stage_observations WHERE job_id='<JOB>' ORDER BY stage;
   SELECT run_id, stage, component, operation, total_work_ms, max_work_ms, calls
   FROM run_operation_observations WHERE job_id='<JOB>' ORDER BY stage, component;"

# finestra render e item/min della run worker:
sqlite3 'file:data/observability/runs.db.sqlite?mode=ro' \
  "SELECT (MAX(queue_completed_at)-MIN(queue_queued_at))/1e9 AS window_s, COUNT(*) AS items
   FROM render_attempt_analytics WHERE run_id='<RUN>';"
```

Le due metriche si calcolano come `items / window_s * 60` (item/min) e
`overlay validati / minuti di wall della finestra` (contratto di misura sopra).
Lo storico completo delle misure e la scomposizione «dove si perde tempo»:
`docs/operations/TICKET-OVERLAY-THROUGHPUT-100-MIN.md`.

---

## 9. Come riprodurre queste misure

```bash
# snapshot aggregato (sola lettura, scrive un JSON versionato)
python3 ops/jobs/timing_snapshot.py --out ops/benchmarks/job-timing-$(date -u +%Y%m%dT%H%M%SZ).json

# tempi per-fase di un run (SQLite read-only)
sqlite3 'file:data/observability/api_requests.db.sqlite?mode=ro' \
  "SELECT name, duration_ms FROM run_stage_observations WHERE run_id='<run_id>' ORDER BY started_at"

# decomposizione operazioni di un run
sqlite3 'file:data/observability/api_requests.db.sqlite?mode=ro' \
  "SELECT stage, component, operation, COUNT(*), SUM(duration_ms), SUM(queue_wait_ms)
   FROM run_operation_observations WHERE run_id='<run_id>'
   GROUP BY stage, component, operation ORDER BY SUM(duration_ms) DESC"
```

Tabelle chiave:
- `run_observability`: queue/active/blocked/wall per run (con `job_type`),
- `run_stage_observations`: wall per fase (incluse le voci annidate),
- `run_operation_observations`: lavoro per singola operazione + `queue_wait_ms`
  + `cache_hit`,
- `data/jobs/jobs.db.sqlite`: `job_steps`, `job_registry_metrics`
  (`queue_wait_ms`, `wall_time_ms`), `job_events` (submit → completed),
- `results/*.job.full.json`: esito completo con `audio_metrics`
  (`tts_ms`, `aac_encode_ms`, `upload_ms`, `audio_rtf`), `overlay_plan`,
  `phrase_overlay_budget`, `canonical_timeline`, `final_audio`.

Gap di strumentazione noti (da non confondere con assenza di costo):
`run_observability.active_ms` mai popolato; `outbox_delivery_ms` dichiarato ma
vuoto; 85 run con osservabilità parziale (`observability_degraded`).

---

## 10. Riferimenti

- Sequenza fasi: `internal/capabilities/scripts/runner_execution_pipeline.go`
  (`RunnerPhaseSequence`), nomi in `internal/kernel/observability/registry.go`
- Retry/resume: `internal/capabilities/scripts/model_run.go` (`stageOrder`,
  `ResumeFrom`, `RetryDelay`)
- Batch Garlasco: `ops/jobs/garlasco_batch_10_20261019_sequential_v7/`
  (`manifest.json`, `CHANGELOG.md`, `results/*.job.full.json`)
- Audit animazioni Garlasco: `ops/jobs/garlasco_animation_audit_20261021/`
- Throughput overlay: `docs/operations/TICKET-OVERLAY-THROUGHPUT-100-MIN.md`
- Roadmap velocità: `TODO-pipeline-100x-velocita.md` (anatomia al secondo di un
  job reale §2, roadmap A–K §4, ordine di esecuzione §5)
- Performance final job: `TODO-milton-finaljob-performance.md`
