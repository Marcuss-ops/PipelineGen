# PipelineGen — Orchestration consolidation audit (fase 2)

**Data:** 2026-09-12
**Scope:** verifica del piano "togliere infrastruttura" (scheduler unico, un solo
execution graph, checkpoint, barriera translation→TTS, cutover Chronon daemon,
attribuzione `audio_compile`, Docs/Drive, benchmark matrix).
**Metodo:** lettura del codice sul `main` corrente + ricomputo dei numeri dai
report operativi reali in `tests/operational/results/`.
**Nessuna modifica al codice.** L'unico artefatto prodotto è questo documento.

Corpus di evidenza usato per ogni numero in questo report:

```text
tests/operational/results/person-overlay-drive/full-*.json         11 run
tests/operational/results/jordan-entity-overlay-drive/full-*.json  15 run
----------------------------------------------------------------------------
26 run complete con timing per stage, operation e fanout
```

Tutti i totali aggregati sono ricomputati con `jq`/`python3` sul corpus; le
citazioni di codice sono `file:line` risolte sul working tree.

---

## 0. Verdetto sintetico

La tua lettura è corretta nell'impianto e sbagliata in un punto, e quel punto
cambia la priorità. Sintesi:

| # | Claim dell'analisi | Verdetto | Evidenza |
|---|---|---|---|
| P0-1 | Scheduler/pool multipli senza authority globale | **CONFERMATO** | 6 authorities indipendenti, §1 |
| P0-2 | Due orchestrazioni (streaming + phase) | **CONFERMATO** | `scene_ready_coordinator.go` + `runner_phase_voiceover.go`, §2 |
| P0-3 | Checkpoint intero per unità costa 10–20 s | **RESPINTO dai dati** | 2 093 ms totali su 26 run, §3 |
| P0-4 | TTS target aspetta tutte le traduzioni della scena | **CONFERMATO** | `scene_ready_coordinator.go:90,141`, §4 |
| P0-5 | Chronon CLI spawn per render | **PARZIALMENTE CONFERMATO** | RenderingGen è già `mode: ipc`; resta un secondo path CLI, §5 |
| P1-6 | `audio_compile` include roba non-audio | **CONFERMATO e quantificato** | 1 763 331 ms di gap vs 1 936 778 ms di render, §6 |
| P1-7 | Docs/Drive prima di COMPLETE | **CONFERMATO** | `runner_execution_pipeline.go`, §7 |
| P1-8 | Fallback TTS spawn-per-call warn-only | **CONFERMATO** | `processor.go:264,343`, §8 |
| P2-9 | Profiling Chronon non granulare | **CONFERMATO, ma non è il collo** | §9 |
| — | Ollama warm, cache TTS/script/image, audio single-pass, DirectYUV | **CONFERMATI già buoni** | §10 |
| — | Benchmark matrix 1/3/10 scene × 1/3/10 lingue | **MANCANTE** | §11 |

**La conclusione che cambia il piano:** i "76 s di `audio_compile`" non sono
76 s di audio. Sono in media **~91% attesa del render overlay**: il render
(1 936 778 ms su 21 chiamate, l'operazione più costosa dell'intero sistema)
viene contabilizzato sullo stage `process` con **`wall_ms = 0`**, e il tempo
speso ad aspettarlo finisce dentro `audio_compile`. Qualsiasi ottimizzazione
audio parta da quei 76 s ottimizza la cosa sbagliata.

**La buona notizia:** il costo di checkpoint che sospettavi come P0 non esiste
(2 093 ms su 26 run). Hai un problema di **attribuzione + orchestrazione**, non
di persistenza.

---

## 1. P0-1 — Scheduler stacking: confermato, con la catena esatta

Non sono pool "duplicati": sono sei authority indipendenti che si annidano.
Ogni livello è bounded, il sistema no.

```text
sceneReadyCoordinator.submit()                    go func() per scena, non bounded
  └─ process(scene)
       ├─ concurrent.Map(transWork, translationConcurrency)   default 4
       │     └─ (JOIN: aspetta TUTTE le traduzioni della scena)
       └─ concurrent.Map(langs, ttsConcurrency)                default 4
             └─ processor.requestSlots                        cap 4
             └─ go enqueueLocalizedRender()                   goroutine per (scene, lang)
                   └─ localization.Service.Localize()
                        ├─ localRenderGate  (per-call, default 4)
                        ├─ Service.renderGate (globale, default 2)
                        └─ Service.uploadGate  (default 4)
```

Evidenza:

| Livello | File:line | Bounded da |
|---|---|---|
| scena | `internal/capabilities/scripts/scene_ready_coordinator.go:50` | nessuno (`go func()` per scena) |
| translation | `internal/capabilities/scripts/scene_ready_coordinator.go:90` | `translationConcurrency` (default 4) |
| TTS | `internal/capabilities/scripts/scene_ready_coordinator.go:141` | `ttsConcurrency` (default 4) |
| TTS provider | `internal/platform/audio/processor.go:273` | `requestSlots` cap `DefaultTTSRequestConcurrency = 4` (`processor.go:110`) |
| render per-call | `internal/capabilities/localization/service.go:161` | `DefaultRenderConcurrency = 4` (`request.go:29`) |
| render globale | `internal/capabilities/localization/service.go:183` | `defaultGlobalRenderConcurrency = 2` (`service.go:103`) |
| upload | `internal/capabilities/localization/service.go:90` | `defaultUploadConcurrency = 4` (`service.go:104`) |
| overlay GPU (worker in-process) | `internal/platform/overlays/gpu_gate.go:34` | `flock(LOCK_EX)` **esclusivo globale** |
| RenderingGen | `RenderingGen/renderinggen/config.yaml:7` | `gpu_lanes: 2` |

Tre conseguenze misurate:

1. **Il numero 4 compare tre volte per lo stesso lavoro TTS**
   (`voiceover.max_concurrent_tts: 4` in `config.yaml:276`,
   `DefaultTTSConcurrency = 4` in `backpressure.go:39`,
   `DefaultTTSRequestConcurrency = 4` in `processor.go:110`). Non è ridondanza
   innocua: sono tre semafori che il sistema non sa di avere, quindi non può
   pianificare su un budget unico.

2. **La concorrenza effettiva è ~2, non 4.** Il fanout `scene_analysis` su
   `person-overlay-drive/full-...-20260912T120454Z-6367.json` riporta
   `calls=4, wall_ms=24341, work_ms=46328` → `work/wall = 1.9`.

3. **`GPUGate` blocca per costruzione il render per-scena su quel path**
   (`gpu_gate.go:34`). Un
   `flock` esclusivo significa concorrenza 1, indipendentemente da ogni altro
   pool a monte. Se un domani alzi `renderGate`, il flock resta il pavimento.

Config non allineata: `SetTranslationConcurrency` (`runner_deps.go:470`) **non
ha nessun chiamante di produzione** — `grep -rn SetTranslationConcurrency
--include=*.go | grep -v _test` restituisce solo la definizione. La
`translationConcurrency` è quindi fissata a 4 e non è esponibile da config,
mentre `cfg.Scripts` non contiene nemmeno le chiavi lette dal wiring (§12).

---

## 2. P0-2 — Doppia orchestrazione: confermato

Esistono due motori che implementano le stesse semantiche.

**Motore A — streaming (SceneTextReady).** `scene_ready_coordinator.go` fa, per
ogni scena emessa dal generatore: translation → TTS → fan-out render.
Attivato in `runner_phase_script.go:252`.

**Motore B — a fasi.** `runner_execution_pipeline.go` esegue:
`normalize → mediaPreflight → beginVidRush → generate → translate →
sceneTextReady → audioCompile → persist → documents → complete`, dove
`sceneTextReady` chiama `runner_phase_voiceover.go` che **reimplementa** la
stessa griglia scene×lingua: `buildVoiceoverWork` + `concurrent.Map` TTS
(`runner_phase_voiceover.go:164`) + fan-out render + apply `applyMu` +
checkpoint per unità.

Il codice dichiara esplicitamente che la seconda esecuzione è un no-op grazie
all'idempotenza (`runner_phase_script.go:299`: *"The normal
translation/TTS stages become idempotent no-ops for these scenes"*), e
`buildVoiceoverWork` salta le scene già voicizzate. Quindi oggi **non fai il
lavoro due volte**, ma:

- tieni due insiemi di branching/conditional-skip;
- due definizioni di "unità di lavoro" (scena vs `(scena, lingua)` flatten);
- due punti in cui si decide quando far partire il render;
- due superfici di test (`runner_translation_pool_test.go`,
  `runner_tts_pool_test.go`, `runner_scene_text_ready_fanout_test.go`,
  `runner_vidrush_concurrency_test.go` …) che pinnano garanzie su **entrambi**.

Convergenza target: un solo `SceneReadyEvent → task graph`, e il phase runner
ridotto a `submit graph → join → certify → finalize`.

---

## 3. P0-3 — Checkpoint: ipotesi respinta dai dati

Hai ipotizzato 200 ms × 50–100 checkpoint = 10–20 s buttati. Il dato dice
altro.

Il codice fa esattamente ciò che descrivi:

- `runner_phase_voiceover.go:162` `var applyMu sync.Mutex`
- `:219` `applyMu.Lock()`
- `:251` `r.checkpoint(ctx, runID, result)` → `SavePartialResult` dell'intero
  `GenerateResult` + `persistSemanticBundleSidecar` con `MarshalIndent`,
  temp file e rename (`runner_lifecycle.go:24-60`)
- `:252` `applyMu.Unlock()`

Ma il costo misurato dello stage `checkpoint` è:

```text
somma dello stage "checkpoint" su 26 run : 2 093 ms
massimo su singolo run                    :   187 ms
mediana                                   :    72.5 ms
```

Distribuzione (ms): 27, 31, 33, 37, 43, 49, 51, 53, 60, 63, 64, 72, 72, 73,
78, 87, 91, 93, 93, 97, 105, 127, 129, 136, 142, 187.

Nel critical path il checkpoint appare con `duration_ms = 1`. **Il checkpoint
non è un P0 e non spiega nessuno dei ~59 s "mancanti".** Il pattern (snapshot
intero per unità + `applyMu`) resta tecnicamente migliorabile — delta/unit
checkpoint con snapshot globale ai confini di fase — ma è un lavoro di igiene,
non un guadagno a due cifre.

Nota metodologica che conta: `RecordStage("checkpoint", ...)` misura solo
`SavePartialResult` + sidecar, **non l'attesa su `applyMu`**. Se un giorno il
checkpoint diventasse lento per contesa, questo stage non lo mostrerebbe: la
barriera nascosta va cercata in un `checkpoint_wait_ms` separato, che oggi non
esiste.

---

## 4. P0-4 — Barriera translation→TTS per scena: confermato

`scene_ready_coordinator.go`:

```go
:90   translated, err := concurrent.Map(c.ctx, transWork, translationConcurrency, ...)
...
:141  tts, err := concurrent.Map(c.ctx, langs, ttsConcurrency, ...)
```

`transWork` esclude la lingua sorgente (già presente), quindi la traduzione non
riguarda il source — eppure **il `concurrent.Map` TTS non parte finché tutte le
traduzioni della scena non sono risolte**. La lingua sorgente, che non ha
nessuna dipendenza, viene messa in coda dietro le traduzioni della propria
scena.

Barriera reale, misurata, su `person-overlay-drive/full-...-20260912T120454Z`:

```text
scene_analysis   wall 24 341 ms   work 46 328 ms
voiceover (TTS)  wall  5 440 ms   work  9 829 ms   ← parte dopo il join traduzioni
```

Il DAG che vuoi è per `(scene, language)` autonomo; oggi è per scena. Il
guadagno atteso è su `tts_first_started_ms` e sul GPU idle, non sul tempo del
singolo componente. **Questa è una delle modifiche a più alto rapporto
valore/rischio del piano**, perché non richiede nessun cambiamento di
concorrenza: richiede di togliere un join.

---

## 5. P0-5 — Chronon: CLI vs daemon, stato reale (claim da correggere)

L'analisi dice "il client attuale dice esplicitamente CLI subprocess today,
persistent daemon / IPC later". Sul `main` corrente **non è più vero per
RenderingGen**:

```yaml
# RenderingGen/renderinggen/config.yaml:27-34
chronon:
  profile: gpu-vulkan-native
  home: /opt/chronon3d
  binary: /opt/chronon3d/bin/chronon3d_cli
  # Warm render shell: render through the persistent Chronon daemon started
  # by worker-entrypoint.sh (caches alive across jobs).
  mode: ipc
  socket_path: /var/run/chronon3d/chronon.sock
```

E `cmd/renderinggen/main.go:87-107` seleziona `chronon.NewIPCClient(...)` quando
`cfg.Chronon.Mode == "ipc"`. Quindi **il cutover al daemon è già fatto dove
conta**: la produzione RenderingGen passa dall'IPC.

Resta un **secondo path che spawna il CLI per job**, e va deciso il suo destino:

```text
refactored/internal/app/wiring/rendering_runtime.go:49
    renderer := infraoverlays.NewCommandRenderer(rendererBinary)   // default /opt/chronon3d/bin/chronon3d_cli
refactored/internal/platform/overlays/renderer.go:37
    cmd := exec.CommandContext(ctx, r.Binary, "--plan", planPath, "--output", output)
```

Questo path è l'**overlay worker interno a PipelineGen**
(`internal/app/workerruntime/run.go:210` → `BuildRenderingRuntime`, con
`GPUGate` flock a `overlay_handlers.go:175`). Il path di produzione degli
script usa invece la coda RenderingGen
(`script_generation_runtime.go:210,239` → `QueueRenderEnqueuer`).

**Quindi il lavoro non è "fare il cutover"**: è decidere se il worker overlay
in-process sopravvive. Se sopravvive, va portato anche lui su IPC; se no, va
rimosso — perché oggi è un secondo motore di esecuzione con la sua cache, il
suo lock GPU e il suo spawn per job (esattamente la "CLI + daemon
contemporaneamente supportati" che hai elencato).

---

## 6. P1-6 — `audio_compile` etichettato male: confermato e quantificato

Questo è il risultato più importante del report.

**Come è composto lo stage.** `executionRun.audioCompile()`
(`runner_execution.go:353-359`) avvolge `runAudioCompilePhase` + `publishFinalAudio`.
Dentro il phase, il compile **sincrono** dell'overlay plan chiama:

```go
// internal/capabilities/scripts/runner_phase_audio.go:406
ref, renderErr := r.overlayRenderEnqueuer.EnqueueChrononPlan(ctx, *result.OverlayPlan)
```

e `EnqueueChrononPlan` è bloccante: `Submit` → `waitForCompletion`
(`render_queue.go:263`) → publisher Drive. Tutto dentro `audioCompileStage`.

**Come viene registrato il render.** Le fasi del worker RenderingGen vengono
proiettate con:

```go
// internal/capabilities/scripts/render_queue.go:392
Stage:     kernobs.StageProcess,     // ← non audio_compile
Component: kernobs.ComponentRenderingGen,
```

con `durationMS` passato direttamente e **nessun `wall_ms` di stage**. Infatti
nel fanout `process` compare con `wall_ms = 0`.

**Prova numerica.** Su 26 run:

| Grandezza | Valore |
|---|---|
| somma `renderinggen/render` | **1 936 778 ms** su 21 chiamate (media 92 227 ms) |
| somma `audio_compile wall − audio_compile own work` | **1 763 331 ms** |
| rapporto | **91.0%** |

Il gap di `audio_compile` traccia il render. Esempi:

| Run | wall | audio_compile wall | audio_compile own work | gap | renderinggen/render | `process` fanout | `unattributed_ms` riportato |
|---|---:|---:|---:|---:|---:|---:|---:|
| person 20260910T153111Z | 229 933 | 148 341 | 12 675 | **135 666** | 131 840 | 132 792 | **75** |
| jordan 20260911T121749Z | 268 914 | 208 690 | 43 476 | **165 214** | 175 411 | 176 312 | **248** |
| jordan 20260912T124730Z | 258 898 | 76 497 | 38 799 | **37 698** | 49 711 | 50 721 | **261** |
| person 20260912T120454Z | 75 749 | 15 542 | 10 119 | **5 423** | 5 301 | 6 098 | **42** |

Cioè: **fino a 165 s di render vengono attribuiti a `audio_compile`, mentre
`unattributed_ms` è riportato come 42–261 ms.** Il numero "audio 76 s" del
piano, su questi dati, è render + publish, non DSP.

**E il DSP vero è encode-bound.** Scomposto (aggregato su 26 run):

```text
rust/audio_render     305 749 ms   (26 chiamate, media 11 759 ms)
  └─ audio/aac_encode 287 752 ms   = 94.1% di rust/audio_render
audio/probe             7 427 ms
audio/hash                976 ms
```

`aac_encode` è **anche** dentro `rust/audio_render` per costruzione — il
commento a `runner_phase_audio.go:185` lo dichiara ("mix/aac_encode/probe/hash
remain the owner-measured subtimings inside it"). Quindi `attributed_ms`
sovra-conta di ~288 s sul corpus. Non è un doppio lavoro, è un doppio conteggio.

**Verdetto operativo:** prima di toccare l'audio, separare lo stage. La
nomenclatura che hai proposto è giusta; i subtiming (`audio_asset_resolve`,
`timeline_compile`, `clip_audio_prepare`, `audio_plan_compile`, `mix`,
`aac_encode`, `probe`, `hash`) **esistono già** in
`runner_audio_observability.go` e vengono emessi solo quando > 0 — nelle run
reali risultano quasi tutti a zero perché la versione del renderer non li
riporta. Il fix non è aggiungere misure: è (a) togliere il render-wait da
`audio_compile`, (b) marcare `aac_encode` come sub-fase e non come operazione
disgiunta.

**Dove va il tempo audio reale, in ordine di grandezza:**
`aac_encode` (94% del DSP) ≫ `probe` > `hash` > `mix`. Nessuna catena
`wav→wav→mp3→wav→mp3→AAC`: il master usa un singolo render Rust e un solo
encode (`final_audio_<hash>.m4a`, AAC 48 kHz stereo). **Il "minimum necessary"
sull'audio è già rispettato; l'encode è il costo, non lo spreco.**

---

## 7. P1-7 — Docs/Drive prima di COMPLETE: confermato

Ordine reale (`runner_execution_pipeline.go`):

```text
generate → translate → sceneTextReady → audioCompile → persist → documents → complete
```

`runDocumentPhase`:

- `runner_phase_document.go:145` apre la fase;
- `:173` fa `r.voiceoverPublishDrainer.Wait()` — attende il pool di publish
  prima di proiettare i link Drive nei documenti;
- poi `document.prepare` (render HTML, CPU, già skeletonizzato in anticipo) e
  `document.publish` (Google Docs, fan-out per lingua con cap 4).

Misurato: `google_docs/publish` = 156 539 ms su 26 run (media 6 021 ms),
`finalize/drive_publish` = **857 093 ms su 131 chiamate** (media 6 543 ms,
il secondo costo del sistema dopo il render). Il `document` stage è
tipicamente 3 645–9 180 ms per run.

Conclusione: la separazione semantica che hai indicato è corretta e non
esiste. Oggi `job complete` significa "Drive + Docs fatti". Serve esplicitare:

```text
COMPUTE_COMPLETE   audio/video certified, artifact pronti
PUBLISH_COMPLETE   Drive/Docs/metadata
JOB_COMPLETE       prodotto
```

Il render core non deve aspettare Docs. Nota però: `document.prepare` è già
skeletonizzato in anticipo (`renderDocumentSkeletons` chiamato nel branch
`parallelFanOut`), quindi **il costo Docs non è già interamente sul critical
path** — la parte da spostare è `document.publish` + il drain.

---

## 8. P1-8 — Fallback TTS legacy: confermato, warn-only

```go
// internal/platform/audio/processor.go:262-266
if err := p.ensureStarted(ctx); err != nil {
    p.mu.Unlock()
    p.log.Warn("persistent TTS worker unavailable, falling back to legacy spawn-per-call", zap.Error(err))
    return p.generateLegacy(ctx, input, safeName)
}
...
// internal/platform/audio/processor.go:343
cmd := exec.CommandContext(ctx, "python3", args...)
```

Una configurazione rotta trasforma silenziosamente il worker persistente in
`spawn Python → TTS → exit` **per ogni sintesi**, con solo un warning. Non
esiste né un contatore `tts_legacy_fallback_total` né un gate
degraded/alert/fail-closed. Per un sistema dove la latenza è parte del
contratto è sotto-specificato: serve un modo esplicito
(`tts_worker_mode = persistent | legacy`) e un alert sul fallback.

---

## 9. P2-9 — Profiling Chronon: granulare ma non riconciliato

Stato reale: il sidecar `chronon3d.frame-timing.v1` viene parsato
(`cliprender/chronon_sidecar.go`) e proiettato in operazioni canoniche
(`cliprender/chronon_metrics_adapter.go`):

```text
chronon.startup, chronon.input_open, chronon.prepare,
chronon.render_loop, chronon.encoder_drain, chronon.ffprobe, chronon.sha256
```

Le metriche DirectYUV/readback/backpressure sono contatori esatti e c'è un
acceptance test che impone `gpu_readback_bytes = 0`,
`nv12_to_rgba_frames = 0`, `rgba_to_nv12_frames = 0`,
`software_fallback_nodes = 0`.

Mancano i bucket richiesti per la chiusura `unexplained → 0`:
`scene_eval_ms`, `frame_delta_ms`, `gpu_wait_ms`, `nvenc_ms`, `mux_ms`,
`finalize_ms` — oggi `mux_finalize` è aggregato. **Ma su questi dati non è la
priorità**: il render è 1 936 778 ms *perché è il render atomico dell'intero
video sulla coda, misurato a 92 s di media*, non perché il bucket interno sia
opaco. Prima si libera l'attribuzione (§6) e si toglie la barriera (§4); solo
dopo ha senso spendere su `scene_eval_ms`.

Nota importante sul profiling: **il render NON è 47 s in queste run.** I 47 s
della run Jordan erano un caso; la media sulle 21 chiamate è 92 s, con picchi
di 200 s (`jordan 20260911T125234Z`: render 201 546 ms su wall 321 550 ms).
Questo rafforza la tua conclusione "non riscrivere il renderer ora", ma
indebolisce il numero di riferimento.

---

## 10. Claim "già buono": verificati

| Claim | Verdetto | Evidenza |
|---|---|---|
| Image search ha cache key canonica + singleflight | **CONFERMATO** | `images/provider_search.go:22` `flight singleflight.Group`, `:90` `resolverSearchCacheKey`, `images/storage_cache.go:16` `imageSearchCacheKey`, `storage_ops.go:173` `s.dedup.Do(key, ...)` |
| `force_refresh` fuori dalla cache key script | **CONFERMATO** | `kernel/script/cache_key.go:26,34` — commento esplicito: `force_refresh` → memory gate bypassed, NOT identity |
| Audio master single-pass | **CONFERMATO** | `rust/audio_render` unico render, `aac_encode` unico encode; nessuna catena di transcode |
| DirectYUV / zero-readback | **CONFERMATO** | acceptance test con `gpu_readback_bytes = 0` |
| Ollama warm/singleflight | **CONFERMATO** (fase 1) | `client_warm.go` + `WarmModel`; resta il difetto `num_ctx` 4096 vs bucket 2048 documentato nel report fase 1 |

---

## 11. Benchmark matrix: mancante

Non esiste un harness scenes × languages × cold/warm nel repo. Esistono:

- `internal/capabilities/scripts/render_concurrency_benchmark_test.go` —
  concurrency 1/2/3/4 su render, gated da `VELOX_BENCH_REAL_RENDER=true`;
- `internal/capabilities/scripts/render_concurrency_benchmark_test.go` +
  `internal/capabilities/performance/benchmark.go` (persistito);
- `RenderingGen/renderinggen/internal/processor/daemon_vs_cli_benchmark_integration_test.go`;
- `RenderingGen/renderinggen/internal/processor/daemon_stability_integration_test.go`;
- `refactored/internal/capabilities/scripts/certification_3scene_vertical_slice_test.go`
  e `certification_10clip_10scene_test.go`.

Manca esattamente la matrice richiesta:

```text
scenes:    1 / 3 / 10
languages: 1 / 3 / 10
cache:     cold / warm
renderer:  cold / daemon warm
```

e la raccolta congiunta di `wall / ollama / nlp / tts / audio / images /
render / upload + cache_hits + GPU/CPU + peak RAM/VRAM`. Finché non c'è, ogni
confronto tra questi due report resta basato su run occasionali.

**Gate già presenti ma non autorevoli:** `runner_timing_gate_test.go`,
`runner_performance_contract_test.go` e le pipeline invariants in
`runner_lifecycle.go` — vedi §12 per il problema.

---

## 12. Spazzatura e ganci non autorevoli

### 12.1 Modello di timing che non sa rappresentare la concorrenza

Su 26 run:

```text
attributed_ms > wall_ms          : 26 / 26
overlapped_ms != 0               :  0 / 26
righe di stage con duration_ms=0 : 122
```

`processor`/`persist`/`verify`/`acquire`/`process` riportano `wall_ms = 0`,
quindi il lavoro parallelo che contengono è invisibile al critical path e al
bottleneck. Il bottleneck riportato è sempre `generate/ollama.generate`, anche
in run dove il render vale 200 s su 321 s totali. Fino a che questo non è
sistemato, **nessun guadagno del DAG sarà visibile**, e il gate
`InvariantUnattributedBelowFivePercent` continuerà a passare con 0.05% mentre
il buco reale è ~135 s.

### 12.2 Invariante hardcoded

```go
// internal/capabilities/scripts/runner_lifecycle.go
kpis.InvariantTTSNeverWaitsRender = true   // "This is now structural (see P0.3)"
```

Un invariante impostato a `true` senza misurazione non è un gate, è una
costante. Nello stesso blocco `InvariantTTSNeverWaitsDrive` è derivato da due
timestamp (`TTSFirstStartedMs > 0 && AudioCompileStartedMs > 0`), cioè verifica
"esiste un timestamp", non "TTS non ha aspettato Drive".

### 12.3 Wiring morto

```go
// internal/app/wiring/script_generation_runtime.go
:172    runner.SetTTSConcurrency(cfg.Scripts.LocalizedRenderConcurrency)
...
:355    runner.SetTTSConcurrency(ttsConcurrency)                          // sovrascrive sempre
```

La prima chiamata è sempre sovrascritta dalla seconda nella stessa funzione.
`SetTranslationConcurrency` non ha chiamanti. `cfg.Scripts` non contiene
`tts_concurrency`, `nlp_concurrency`, `localized_render_concurrency`,
`localized_render_global_concurrency`, `script_generation_concurrency`,
`serial_mode` — tutte lette dal wiring e documentate in
`internal/platform/config/scripts.go`.

### 12.4 Su disco

```text
Chronon3d/build            57 GB   (gitignored, .gitignore:18)
  └─ build/chronon         54 GB
Chronon3d/.ccache         1.2 GB
refactored/.venv-whisper  2.6 GB   (gitignored, refactored/.gitignore:276)
refactored/data           955 MB
refactored/rust           558 MB
```

Correzione al report fase 1: `.venv-whisper` **è** gitignored. La spazzatura è
disco locale, non repo.

### 12.5 Corpus di evidenza distrutto

`refactored/` ha 92 entry dirty: 14 modified, 63 untracked, **15
`status-*.json` cancellati**:

```text
tests/operational/results/jordan-entity-overlay-drive/status-*.json   10
tests/operational/results/person-overlay-drive/status-*.json           5
```

Sono esattamente i file con cui si misura ogni regressione. Vanno ripristinati
o la spazzatura va cancellata in modo intenzionale, non per `git clean`
accidentale.

---

## 13. Ordine pratico corretto

Il tuo ordine era già quasi giusto. Con i dati in mano, la correzione è che
**l'attribuzione viene prima di tutto** e che il cutover Chronon è in gran
parte già fatto:

```text
0.  Fix attribuzione: render fuori da audio_compile; stage wall per
    process/persist/verify/acquire; overlapped_ms reale; aac_encode
    marcato sub-fase, non operazione disgiunta.      ← nessun guadagno, sblocca la misura
1.  Togliere la barriera translation→TTS per scena (per-(scene,language)).
2.  Convergere i due orchestratori su un solo execution graph.
3.  Authority unica di concorrenza (assorbire translation/tts/requestSlots/
    renderGate/uploadGate/gpu_lanes), e decidere il destino del worker
    overlay in-process (CommandRenderer + GPUGate flock).
4.  Separare COMPUTE_COMPLETE da PUBLISH_COMPLETE (document.publish + drain).
5.  Gate TTS legacy fallback: mode esplicito + contatore + alert.
6.  Benchmark matrix 1/3/10 × 1/3/10 × cold/warm.
7.  Solo ora: profiling Chronon granulare (scene_eval/frame_delta/gpu_wait/
    nvenc/mux/finalize) e chiusura unexplained→0.
8.  Ultimo: packet copy / GOP surgery (già esistente in Chronon3d:
    CopyGop/BitstreamCopy/SmartGopCopy, packet_copy_allowed{true}).
```

**Cosa NON rifare:** image cache/singleflight, cache script/TTS con
fingerprint semantica, `force_refresh` fuori dalla key, master audio
single-pass, DirectYUV, Ollama warm. Sono tutti già verificati qui.

---

## 14. Criteri di accettazione verificabili

| # | Criterio | Come si verifica |
|---|---|---|
| A | Il render ha uno stage con `wall_ms > 0` e non è incluso in `audio_compile` | nuovo report: `timing.stages` contiene `render` con wall ≈ `renderinggen/render` |
| B | `unattributed_ms` riflette il buco reale | su una run con render: `wall − somma(stage disgiunti) < 5%`, e oggi sarebbe ~135 s |
| C | `overlapped_ms > 0` quando due branch corrono insieme | run con NLP+TTS in parallelo |
| D | `aac_encode` non è contata due volte | `attributed_ms ≤ wall_ms × (1 + overlap)` |
| E | La lingua sorgente non aspetta le traduzioni | `tts_first_started_ms < translation_last_finished_ms` sul source |
| F | Un solo execution graph | `runVoiceoverPhase` non contiene più fan-out TTS/render |
| G | Budget di concorrenza unico | un solo componente possiede i limiti CPU/GPU/NVENC/TTS |
| H | `COMPUTE_COMPLETE` esiste come stato | artifact certificati osservabili prima di `document.publish` |
| I | Fallback TTS osservabile | `tts_legacy_fallback_total` incrementato e alertabile |
| J | Matrice benchmark eseguita | report con 1/3/10 × 1/3/10 × cold/warm |

---

## 15. Limiti di questo report

- `overlapped_ms = 0` è provato come **output** del modello di timing; la
  spiegazione strutturale (il campo `Stage` non viene serializzato nei report,
  quindi la ricostruzione degli intervalli è impossibile a posteriori) è
  ricostruita per aritmetica, non letta da un log di intervalli.
- I bucket Chronon `scene_eval_ms`/`frame_delta_ms`/`gpu_wait_ms`/`nvenc_ms`/
  `mux_ms`/`finalize_ms` non sono verificabili da questo corpus: richiedono una
  run con `--profile` e il sidecar preservato.
- Il rapporto 91% tra gap di `audio_compile` e `renderinggen/render` è una
  correlazione su 26 run con 4 esempi puntuali coerenti; non è un esperimento
  controllato con render disabilitato.
- Le run analizzate sono batch concorrenti su coda condivisa: il render medio
  di 92 s include l'attesa di accodamento, non solo il tempo di esecuzione
  Chronon.
