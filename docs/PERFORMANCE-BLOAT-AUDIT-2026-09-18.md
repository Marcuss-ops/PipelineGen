# Performance & Bloat Audit — 2026-09-18

> Audit consolidato su tre assi: (1) velocizzazione del percorso script-generation,
> (2) analisi RenderingGen + overlay end-to-end, (3) inventario del bloat da
> eliminare/migrare secondo la Zero Legacy Policy
> (`docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md`).
>
> Tutti i riferimenti `file:linea` sono verificati sul tree al 2026-09-18.

---

## 1. Executive summary

Il percorso script → overlay → render è architetturalmente sano (DAG parallelo,
fail-closed, checkpoint durevoli) ma porta **tre colli seriali misurabili** e
**una catena legacy completa rimasta nel tree** senza chiamanti in produzione.

| Asse | Finding principale | Impatto stimato |
|---|---|---|
| Velocità | Render overlay per-lingua SERIALE (`runner_phase_overlay_render.go:61`) | 9 lingue = 9 attese GPU+upload in serie |
| Velocità | `runTranslatedNLP` dopo il join TTS (`runner_execution.go`, `sceneTextReady`) | fino a ~8.7 min di NLP (probe 7 lingue, 2026-09-17) fuori dal parallelismo |
| Velocità | `gpu_lanes=2` worker vs attesa lane misurata al 74% (`gpu_lane_wait.go`) | il collo vero è lato worker, non lato producer |
| Bloat | Catena phrase extractor legacy (4 interfacce + adapter Ollama 160 LOC + campo compat) | zero chiamanti production, solo superficie morta |
| Bloat | 6 hotspot con deadline ratchet 2026-09-30 (12 giorni) | debito strutturale non ridotto dal ratchet |

---

## 2. Stato attuale: il DAG dello script generation

Percorso reale in `internal/capabilities/scripts/runner_execution_pipeline.go`:

```
start → normalize → mediaPreflight → beginVidRush
  → generate      (Gemma per segmento; gate Ollama cap=3 = OLLAMA_NUM_PARALLEL)
  → translate     (pool 4; checkpoint per ogni scena×lingua)
  → sceneTextReady fan-out:
       ├── branch semantica: VidRush join (VisualNER su source) + overlay.prepare
       ├── branch assets:    DocsPrepare skeleton + audio prefetch
       └── TTS (pool 4) nel goroutine principale
  → runTranslatedNLP   (VisualNER per OGNI scena×lingua tradotta; frasi deterministiche)
  → audioCompile → overlayRender (BLOCCANTE, la wait più lunga del run)
  → audioFinalize → audioPublish → persist → documents → complete
```

Concettenote chiave:

- Le frasi importanti sono **deterministiche e locali** (`important_phrase_selection.go`)
  da cutover 2026-09-18: zero chiamate NLP per le frasi; VisualNER resta per entità/nomi.
- Per le traduzioni, la selezione frasi gira sul testo localizzato (`translated_nlp.go:121`)
  quindi le annotazioni sono ancorate alla lingua e ai timing della voce corrispondente.
- Il fan-out SceneTextReady è protetto da test di regressione espliciti
  (`runner_critical_path_regression_test.go`): una slow/blocked overlay.prepare enqueue o
  DocsPrepare NON DEVE mai ritardare TTS o NLP. Qualsiasi refactoring qui deve tenerli verdi.

---

## 3. Problemi — PipelineGen script runner

### P1 — Render overlay per-lingua seriale  [ALTO]

`runner_phase_overlay_render.go:61` itera `overlayPlansToRender` (source lang prima,
poi target in ordine) chiamando `EnqueueChrononPlan` **in sequenza**: ogni chiamata
sottomette il job e aspetta lo stato terminale + pubblica su Drive + registra analytics.

Con N lingue si pagano N × (attesa GPU + upload Drive ~1.9s/artifact + analytics) in serie.
Ogni lingua interna ha già un pool per-item parallelo (`defaultSeparateItemRenderWorkers=4`,
`render_queue_items.go`), ma le lingue non si sovrappongono mai.

**Fix**: semaforo bounded allineato a `worker.gpu_lanes` (2, vedi R1) attorno al loop.
Il semaforo lato PipelineGen NON è un bound GPU — il worker resta l'unica autorità
(`worker.gpu_lanes`, `config.go:120-124`) — serve solo a evitare code inutili.

### P2 — NLP traduzioni fuori dal percorso critico  [ALTO]

In `runner_execution.go` `sceneTextReady()` chiama `runTranslatedNLP` DOPO il join del
fan-out, cioè dopo che TTS è terminato. I dati del probe Mike Tyson confermano il costo:

| Run | Lingue | `translated_nlp_wall_ms` |
|---|---|---|
| 2026-09-16 | — | 575.193 (~9.6 min) |
| 2026-09-17 | PL DE ES PT BR FR ID | 519.901 (~8.7 min) |
| 2026-09-17 | IT RU TR | 227.703 (~3.8 min) |
| 2026-09-17 | RU | 91.680 |

**Fix**: muovere l'estrazione per-(scena, lingua) dentro il coordinator
(`scene_ready_coordinator.go`): ogni traduzione appena pronta schedula la sua NER,
sovrapposta al TTS restante, invece di attendere la barriera.

### P3 — Checkpoint per singolo item  [MEDIO]

`runner_phase_translation.go:97` e `runner_phase_voiceover.go:290` eseguono
`r.checkpoint(...)` (→ `repo.SavePartialResult` SQLite) a OGNI scena×lingua completata.
Un run 5 scene × 9 lingue = 45 scritture di result completo per la sola traduzione.

**Fix**: debounce (flush ogni N item o a boundary di stage). Il resume reale è già
garantito dal checkpoint post-fan-out (`runner_execution.go` `parallelFanOut`) — il
per-item serve solo a limitare la perdita a crash; un debounce di ~500ms non peggiora
materialmente la finestra di perdita.

### P4 — Doppia compilazione SceneIR in Enrich  [MEDIO]

`vidrush_semantic_chain.go` `SceneIRSegmentEnricher.Enrich` compila la SceneIR
(prima passata), estrae entità/frasi, poi RICOMPILA con `EntityResult` per il profilo.
La seconda compilazione esiste solo per far riemergere le superfici editoriali nel
profilo canonico. In più, sulle entità si susseguono 4 passaggi (`validateVisualEntities`
→ `deduplicateVisualEntities` → `limitTranslatedVisualEntities` → `normalizeVisualPersonName`)
che possono fondersi in una singola passata.

**Fix**: single-pass Enrich (costruire `entityResult` prima dell'unica compilazione;
fondere i 4 passaggi entità in uno).

### P5 — VisualNER ridondante sulle traduzioni  [MEDIO]

`translated_nlp.go`: `matchLocalizedSourceEntities` (`translated_nlp_identity.go:44`)
cerca già i nomi source nel testo tradotto e ne copia l'identità. Quando TUTTE le entità
source sono trovate verbatim nella traduzione, la chiamata NER per quella lingua non
aggiunge typed spans nuovi.

**Fix**: skip condizionale della chiamata `NERPort.Extract` per la lingua quando i
source matches coprono il limite `entityLimit`. Con 5 scene × 9 lingue le chiamate NER
tradotte scendono tipicamente da 45 a ~0-10. NB: verificare contro il probe live
(`VELOX_E2E_LIVE=1 go test ./internal/capabilities/scripts/ -run TestLiveMikeTyson500WordMultilingualNLPNoRendering`)
che `AllSpecialNamesGrounded` resti vero per ogni lingua.

### P6 — Streaming scene-text: verificare l'eligibility di default  [VERIFICA]

Il per-scene streaming esiste già (`scene_ready_coordinator.go`; eligibility in
`runner_phase_script.go`, `hasExplicitSceneMarkers`): le scene SourceClips senza marker
`SCENE N:` possono accendere traduzione+TTS per-scena invece che alla barriera.
Verificare che i run production lo usino effettivamente (log/piano) prima di toccare altro.

### P7 — Pool traduzione provider-aware  [BASSO]

`MaxTranslationConcurrency=4` è certificato per Ollama (slot condivisi con Gemma,
`OLLAMA_NUM_PARALLEL=3` server). Quando la traduzione gira via Argos server (già cablato
in `build_bundles_texttracks.go` come primary con fallback Ollama), il cap Ollama non è
il collo: il pool può salire senza rubare slot a Gemma. Intervento di config, non di codice.

---

## 4. Problemi — RenderingGen + overlay

### R1 — `gpu_lanes=2` con lane wait misurata al 74%  [ALTO]

Il worker ha pipeline a 3 stadi (prep pool → GPU lanes → post pool, `processor/pipeline.go`).
Il dato chiave è in `processor/gpu_lane_wait.go`: su un batch reale 5-clip, un clip
riportava `total_ms=36.694` contro un wall Chronon di 8.201 — **~27s (74%) era attesa
di GPU lane**, invisibile nelle fasi.

Config: `worker.gpu_lanes` default **2** (`config/config.go:327`, range valido 1-8).
Esiste già una seconda unità worker (`infra/native/renderinggen-b.yaml`) e il daemon pool
supporta `LanesPerDaemon` round-robin (`chronon/daemon_pool.go`).

**Fix**: dimensionare `gpu_lanes` con la metrica `gpu_lane_wait` (se resta alta → 4 lane
o secondo worker), MA prima validare con `vramprobe` (nel repo) che la GPU regga
2+ render concorrenti NVENC.

### R2 — Drive publish dentro la wait bloccante  [ALTO]

`render_queue.go` `enqueueChrononPlan`: `e.publisher.PublishOverlay` gira PRIMA del
return, dentro il tempo bloccante del render. Upload misurato ~1.9s/artifact (e2e stock
2026-09-18) × 10+ item per lingua = 20s+ di upload serializzati col render.

**Fix**: raccogliere i riferimenti e pubblicare dopo (leg parallela o post-collect),
fuori dal percorso che blocca la lingua successiva. L'artifact resta certificato dal
receipt gate del worker; la publish è un side-effect applicativo.

### R3 — overlay.prepare per piani solo-testuali  [MEDIO]

`capabilities/overlays/prepare.go` + `runVidRushJoinAndPrepare` (`runner.go:76`):
il job `overlay-prepare.v1` parte sempre dopo l'estrazione entità. Per piani senza item
immagine/entity (solo IMPORTANT_PHRASE/WORD text) il prefetch non ha asset da materializzare
e la risoluzione template viene rifatta comunque dal compile del render.

**Fix**: skip condizionale quando gli intents non contengono superfici con asset_refs.
Costo evitato: un job queue + lease + workspace per run.

### R4 — Poll interval e tail latency  [VERIFICA]

La wait è event-driven con poll di sicurezza (`defaultQueuePollInterval`, tunabile via
`SetPollInterval`, già testato in `TestQueueRenderEnqueuerUsesEventDrivenWait`).
Verificare in produzione che `External.RenderingGenPollIntervalMS` sia impostato
aggressivo (500ms-1s) per tutte le code; il poll serve solo come rete di sicurezza.

### Cosa NON toccare in RenderingGen (già ottimale)

- Pipeline a stadi prep/GPU/post con overlap (prep N+1 durante GPU N).
- Materialize content-addressed L1/L2/L3, resolver streaming senza doppio `os.Stat`,
  hard-link workspace; font Poppins e background condivisi tra child plan = L2 hit.
- Compile semantico `overlay/semantic_compile.go`: decode strict, single pass, fail-closed
  su template sconosciuti.
- Progress/stall watchdog (`[video] N/M frames`, log campionato 10s per il mutex condiviso).
- Receipt gate nativo in `FinalizeJob` (backend/frames verificati; fallisce chiuso).

### Dato e2e live (2026-09-18, stock pipeline)

`tests/operational/results/pipeline-live/full-stock-run-pipeline-e2e-20260918T133118Z-5105.json`:
wall 29.4s, `stock.stage_sources` (download YouTube) 15.7s = **53.5%** del wall,
`unattributed 24.25%`. Segnale: (a) il collo della stock pipeline è il download,
(b) l'attribuzione osservabilità lì ha margine di miglioramento.

---

## 5. Inventario bloat (classificato)

### A. ELIMINARE SUBITO (morto confermato, zero chiamanti production)

| # | Cosa | Dove | Evidenza |
|---|---|---|---|
| A1 | Interfacce legacy phrase extractor: `ImportantPhraseExtractor`, `BatchImportantPhraseExtractor`, `SceneNLPExtractor`, `BatchSceneNLPExtractor` + valore `SceneNLPExtraction` | `vidrush_semantic_ports.go:53-88` | Il commento stesso: "legacy NLP phrase surface... no longer calls it" |
| A2 | Campo compat `VidRushPipeline.PhraseExtractor` | `vidrush_pipeline.go:75` | "retained temporarily for source compatibility" |
| A3 | Adapter `OllamaImportantPhraseExtractor` (~160 LOC, 4 metodi) | `internal/platform/ollama/adapters/important_phrase_extractor.go` | Zero costruzioni in `internal/app/wiring/` e `cmd/` (verificato con grep) |
| A4 | Campi compat `VidRushPipeline.Enricher` / `ProviderResolver` + ramo `SemanticAndFanoutResolver` | `vidrush_pipeline.go:44-60`, `runner.go:305-345` | In produzione `Enricher: nil` (`script_generation_runtime.go:326`); la catena nuova è costruita da `NERPort` |
| A5 | Fallback compat gate NLP (`nlpGate == nil → generationGate`) | `runner.go:390-397` | "compatibility fallback for older composition roots" — vietato dalla Zero Legacy Policy |
| A6 | Stale comment che cita `vidrush_semantic_chain_grounding.go` (file inesistente) | `vidrush_semantic_chain.go:14` | "stale comments describing retired behavior" = legacy secondo la policy |
| A7 | `serialMode` nel Runner di produzione | `runner.go:170-175`, `runner_deps.go:54`, `runner_execution.go:247-260,387` | Solo benchmark "before/after"; benchmark conclusi. La policy vieta i feature flag che sopravvivono alla migrazione. Se serve ancora → hook test-only |

### B. MIGRARE (hotspot con deadline ratchet — da `architecture/package_hotspots.json`)

Deadline **2026-09-30 (12 giorni)** per 6 hotspot; ratchet = non-crescita, la riduzione va fatta a mano:

| Hotspot | Files | Target split | Nota |
|---|---|---|---|
| `internal/capabilities/scripts` | 89 | `runner/ audio/ overlay/ vidrush/` | `overlay_background/overlay_entity_cards/overlay_publication` già indicati come candidati subpackage |
| `internal/capabilities/scripts/adapters` | 56 | `processor/ postprocessor/ vidrush/` | deve diventare re-export only |
| `internal/app/wiring` | 150 | `chronon/ voiceover/ vidrush/ assets/` | split iniziato (181→150); resta Artlist/stock nel root |
| `internal/capabilities/images` | 75 | `generation/ search/ validation/` | ceiling ri-allineato 2026-09-13 |
| `internal/capabilities/jobs` | 77 | `queue/ scheduling/ finalize/` | scheduling già estratto |
| `internal/capabilities/assets/providers/stock/stockpipeline` | 63 | `stock/ ingest/finalize/publish/cleanup/reconcile` | re-merged flat, baseline 63 |

Deadline **2026-12-31**: `kernel/asset` (39), `kernel/asset/detail` (34), `platform/delivery` (12), `platform/embeddings` (5).

### C. RIDURRE (bloat da esecuzione)

- C1 = P4 (doppia compilazione SceneIR + 4 passaggi entità in uno).
- C2 = P3 (checkpoint per-item → debounce).
- C3 = P5 (NER traduzioni ridondante → skip condizionale).

### D. TENERE (sembra bloat, non lo è)

| Cosa | Dove | Perché tenerlo |
|---|---|---|
| `entity_battery_corpus*.go` (0 `func Test`) | `internal/capabilities/scripts/` | Ground-truth data per la certificazione estrattori (vedi `docs/entity-extraction-battery-goal4.md`), non test morti |
| `legacy_overlay_plan.go` | `internal/capabilities/scripts/` | Nome ingannevole: è il bridge batch ATTIVO per `script.generate_item` |
| `platform/qdrant/indexing/clipindexer` | `internal/platform/qdrant/` | Compatibility seam VIVO (`SetCanonicalIndexRequester` short-circuit verso outbox PG, da AGENTS.md). Non toccare finché il cutover media non lo dichiara estinto |
| `percheck_pg_dual_write_contract` | `cmd/archcheck/scan/boundaries/` | Gate auto-dichiarato con sunset legato alle colonne TEXT legacy in contratto fase 4 — eliminare SOLO con le colonne |

---

## 6. Piano operativo

### Fase 1 — Rimozione legacy (PR meccanica, basso rischio)

1. Cancellare A1-A6: interfacce + adapter + campi compat + fallback + stale comment.
   I test che contano le chiamate phrase-extractor si semplificano (senza interfaccia
   non può esserci chiamata).
2. `serialMode` (A7): estrarre in hook test-only o rimuovere; i test
   `runner_serial_mode_test.go` / benchmark si riscrivono attorno al fan-out diretto.
3. Gate: `go test ./internal/capabilities/scripts ./internal/app/wiring ./internal/platform/ollama/adapters ./internal/platform/ollama/client -count=1`
   + `make verify-agent` + `go run ./cmd/archcheck --strict`.

### Fase 2 — Velocità critico-path (PR con misure before/after)

4. **P1**: semaforo per-lingua = `gpu_lanes` (2) attorno al loop overlay render.
5. **R2**: Drive publish fuori dalla wait (post-collect).
6. **P2**: `runTranslatedNLP` per-(scena, lingua) dentro il coordinator.
7. Misure: KPI esistenti (`generate_first_scene_ready_ms`, `core_ready_ms`,
   `overlay_render` stage, `gpu_lane_wait`) + probe live Mike Tyson per la NLP.

### Fase 3 — Migrazione strutturale (entro deadline 09-30)

8. `scripts/adapters` → 3 subpackage (famiglie già coese, split meccanico) e il root
   diventa re-export/compat surface (come da nota registro).
9. `scripts` root → famiglia overlay nei subpackage target.
10. Aggiornare `package_hotspots.json` (baseline ratchet down) nella STESSA PR di ogni
    riduzione (il gate `percheck_legacy_hotspot_growth` fallisce altrimenti su stale counts;
    usare `scripts/regen_hotspots.py`).

### Fase 4 — Efficienza runtime

11. C1 single-pass Enrich.
12. C2 checkpoint debounce.
13. C3 skip NER traduzioni ridondante (validato dal probe live: `AllSpecialNamesGrounded`
    per ogni lingua).
14. **R1**: dimensionare `worker.gpu_lanes` (2→4 o worker B) dopo `vramprobe`.
15. P7 pool traduzione provider-aware (config).

---

## 7. Bound certificati — NON toccare

| Bound | Valore | Perché |
|---|---|---|
| `DefaultGenerationConcurrency` | 3 | = `OLLAMA_NUM_PARALLEL` server (A4000/e4b): slot client extra → solo coda server |
| `DefaultTTSConcurrency` | 4 | Misurato: pool 8 → TTS 32.3s→41.0s, wall 51.2s→58.2s (contenzione provider) |
| `MaxTranslationConcurrency` | 4 | Hard cap scene×lingua con apply in ordine canonico |
| `DefaultNLPConcurrency` | 4 | Certificato (`TestConcurrencyBound_NLP`) |
| `defaultSeparateItemRenderWorkers` | 4 | Pipelining pre/post per-item; il GPU bound resta il worker |
| `worker.gpu_lanes` range | 1-8 | Validazione config (`config.go:421-422`) |
| Fan-out SceneTextReady | parallelo | Coperto da `runner_critical_path_regression_test.go` — NON ri-serializzare |

---

## 8. Metriche di successo

1. `overlay_render` stage wall: −(N_lingue−1)/N_lingue × (wait GPU + upload) dopo Fase 2.4.
2. `translated_nlp` sovrapposto a TTS: il run wall non deve più contenere la NLP in serie
   (target: NLP invisibile sul critical path, verificabile dai KPI milestone).
3. `gpu_lane_wait` percentile: sotto soglia decisa post-`vramprobe`.
4. Tree: −600+ LOC legacy (Fase 1), hotspot `scripts`/`scripts/adapters`/`app/wiring`
   sotto baseline entro 09-30.
5. Zero regressioni: suite 4 package + `make verify-agent` + `archcheck --strict` verdi
   su ogni PR; probe live Mike Tyson verde prima e dopo Fase 2/4.
