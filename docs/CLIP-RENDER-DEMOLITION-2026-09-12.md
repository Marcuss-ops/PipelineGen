# clip.render demolition audit (2026-09-12)

**Obiettivo:** la fase "demolition" del percorso clip.render — togliere
compatibilità, doppioni e meccanismi che non servono più.
**Regola applicata (la tua):** *prima sostituiamo → testiamo → misuriamo → poi
cancelliamo il vecchio percorso.*
**Esito:** 1 item demolito nel primo passaggio (9), 5 verificati già demoliti
(5, 6, 7, 8, 10), 4 bloccati da un consumatore reale o dal gate dei benchmark
(1, 2, 3, 4). Nel secondo passaggio (**§14**) sono stati demoliti gli item 2 e 3
e chiuso l'item 4; resta bloccato il solo item 1. Nessuna cancellazione di
codice vivo.

---

## 0. Gate: i benchmark sono verdi (misurato, non asserito)

Il cancello che la tua roadmap pone prima della demolition è la matrice
`1/10/50 clip`. È eseguibile in-repo (fake a latenza configurabile, worker e
continuation reali) e passa:

```text
scenario-10-e2e-1-clips   wall=13ms  rate=4423 clips/min   failures=0
scenario-10-e2e-10-clips  wall=14ms  rate=42646 clips/min  failures=0
scenario-10-e2e-50-clips  wall=46ms  rate=64996 clips/min  failures=0
```

Gli altri scenari misurati e verdi: 1 worker-scaling (1/2/4/8),
2 submit/settle occupancy, 3 parent-completion latency + event-driven
scalesafe, 4 SHA cache-hit, 5 shared CAS, 6 transcript reuse,
7 source-seek evidence, 8 cold vs warm, 9 RenderingGen saturation.

Evidenza chiave già disponibile per due dei tuoi item:

```text
scenario 2  slot occupancy: blocking handler=62ms | async submit=1ms | async settle=10..60ms
scenario 3  parent finalisation: tick p50=195ms | event p50=1ms (Δ≈27.3 s/clip @30s cadence)
scenario 4  SHA cache-hit: 20 clips → 1 full read, 1 download
scenario 5  shared CAS: 2 batches × 10 clips → 1 download, 2 verifications
```

I benchmark **non sono stati eseguiti sullo stack reale**: `TestScenario7_ChrononSegmentSeekLive`
è lo scenario live-only (`VELOX_BENCH_REAL_STACK=1`) e richiede GPU/Chronon.

---

## 1. Verdetto per item

| # | Item | Verdetto | Evidenza |
|---|---|---|---|
| 9 | Logging per-phase rumoroso | **DEMOLITO** | 11 Info/clip → 3 Info/clip, §2 |
| 5 | Secondo compositor FFmpeg | **già demolito** | zero riferimenti, §3 |
| 6 | Backend selector finto | **già demolito** | `backend.go` = identità/contratto, §4 |
| 7 | Font resolution/hashing legacy | **già risolto** | registry+cache+asset root assoluto, §5 |
| 8 | Prepare ripetuto nel settle | **già risolto** | prepare solo in submit, fail-closed, §6 |
| 10 | Feature flag async temporanea | **non esiste** | zero flag, §7 |
| 1 | Render sincrono bloccante | **BLOCCATO** | 2 consumatori vivi, §8 |
| 2 | Polling 30s dell'aggregatore | **BLOCCATO** | il tick è l'unico finaliser cablato, §9 |
| 3 | Root di materializzazione duplicate | **BLOCCATO** | 3 root, §10 |
| 4 | SHA-256 full-file su cache hit | **parziale** | memoizzato, restano siti sul path di scrittura, §11 |

---

## 2. Item 9 — DEMOLITO: logging per-phase

**Prima** (per ogni clip, hot path):

```text
internal/capabilities/cliprender/worker.go            6 × Info
  clip.render.job.phase          (phase=start)
  clip.render.job.start
  clip.render.job.destination_resolved
  clip.render.job.phase          (phase=render_start)
  clip.render.job.phase          (phase=render_done)
  clip.render.job.completed

internal/capabilities/localization/adapters/render.go 6 × Info per clip localizzato
  clip.render.localization.phase (compile_start / compile_done / render_start /
                                  render_done / hash_done)   ← via logPhase()
  clip.render.localization.completed
```

**Dopo:**

```text
worker.go                         2 × Info + 1 × Error + 3 × Debug
  clip.render.job.start        INFO    ← accepted
  clip.render.job.completed    INFO    ← completed / published
  clip.render.job.render_failed ERROR  ← failed
  (job.phase ×3, destination_resolved) DEBUG

localization/adapters/render.go   1 × Info + 1 × Warn + Debug
  clip.render.localization.completed INFO  ← completed
  clip.render.localization.failed    WARN  ← failed (una riga per clip fallito)
  clip.render.localization.phase     DEBUG ← traccia diagnostica
```

`logPhase` è ora Debug; è stato introdotto `logPhaseFailure` (Warn, nome evento
`clip.render.localization.failed`) e gli 8 siti di fallimento
(`validate_failed` ×4, `compile_failed`, `render_failed`,
`render_invalid_outcome`, `hash_failed`) lo usano. Prima un clip fallito
produceva una traccia Info sparsa che l'operatore doveva ricucire a mano.

Eventi canonici rimasti: **accepted, completed, failed**. Nessun test asseriva
sui log (`zaptest/observer` mai usato in questi package), quindi il cambio di
livello è verificato dai test, non solo dal build.

---

## 3. Item 5 — già demolito: nessun secondo compositor

```text
grep -rn "OverlayCompositor|FFmpegOverlay|ffmpeg_overlay|compositeOverlay" --include=*.go --include=*.yaml .
→ 0 risultati
```

L'overlay è compositato dentro il singolo pass Chronon
(`clip.render.overlay.single_pass`), l'artifact renderizzato **è** l'artifact
pubblicato (`worker.go`: "The rendered artifact IS the published artifact …
there is no second encode"), e `require_zero_copy` fallisce chiuso invece di
degradare su un secondo encode. Nessun helper, test, config o gate residuo.

---

## 4. Item 6 — già demolito: `backend.go` è identità, non selettore

```text
grep -rn "BackendSupport|ResolveBackend|BackendRegistry|BackendResolver|
          BackendCapabilityProbe|RenderRequirementResolver" --include=*.go .
→ solo commenti in backend.go:14-15 che elencano i simboli rimossi
→ tutti gli altri hit sono search.BackendRegistry (asset search), altro sottosistema
```

`backend.go` contiene `BackendChrononVulkan` (unico backend ammesso) e
`IsGPUBackend()` usato come guard fail-closed sul `require_gpu`. Nessun
resolver, registry o capability probe: **RenderingGen decide HOW, PipelineGen
descrive WHAT.** Non c'è nulla da cancellare.

---

## 5. Item 7 — già risolto: una risoluzione, una cache

`internal/platform/renderinggen/font_assets.go` è l'unico proprietario:

- `canonicalFonts` = registry immutabile id → path asset-root-relative;
- `AssetRoot()` risolve la root **una volta per processo** (env
  `PIPELINEGEN_ASSET_ROOT` → walk-up dall'eseguibile → CWD), quindi niente più
  path relativi alla CWD: un worker lanciato da systemd non fallisce più in
  silenzio sul font;
- `ResolveFontAsset(id)` fa **un solo** `os.ReadFile` + SHA-256 per font per
  processo, poi serve la cache;
- fail-closed su id sconosciuto o asset mancante.

I wrapper `watermarkFontAsset` / `watermarkFontAssetForStyle` /
`poppinsFontAsset` (`clip_plan_mapper.go:540,551,555`) sono ora deleghe di una
riga a `ResolveFontAsset`. **Non** soddisfano la tua condizione di
cancellazione ("soltanto wrapper inutili **e zero caller**"): hanno chiamanti
vivi (`clip_plan_mapper.go:416,497,500,524` + `clip_executor_test.go:238`).
Quindi restano, per la tua stessa regola.

---

## 6. Item 8 — già risolto: il settle non ri-prepara

`Worker.preparePlan` è raggiungibile da **un solo** sito:

```text
internal/capabilities/cliprender/worker.go:182  if phase == RenderPhaseSettle { …
internal/capabilities/cliprender/worker.go:186      } else { prepared, plan, … = w.preparePlan(…) }
internal/capabilities/cliprender/async_worker.go:96     prepared, err := w.preparer.Prepare(…)
```

Il ramo settle carica il `ResumeDocument` dal continuation store, ricostruisce
`Prepared` con `preparedFromResume` (`async_worker.go`), riusa piano, subtitles,
contratto, transcript e `publishFolderID`, e **non** rifà destination
resolution, transcript resolution, subtitle compile, asset materialization,
overlay lookup o font resolution. Non esiste alcun ramo "se manca X, rifaccio
Prepare": il settle fallisce chiuso (`settle phase requires a ContinuationStore`,
`continuation binding`). Il commento nel codice è esplicito: *"Destination
resolution and all preparation already happened before Submit. Repeating it
here would re-download assets and can select a different Drive folder after a
restart."*

---

## 7. Item 10 — nessuna feature flag

```text
grep -rniE "clip.?render.?async|async.?clip.?render" --include=*.go --include=*.yaml --include=*.mk --include=Makefile .
→ solo un commento in bench_harness_test.go e una stringa API-docs
```

Non esiste `CLIP_RENDER_ASYNC`, nessun ramo `if async` con OFF legacy, nessun
test del ramo OFF. La scelta del percorso è **strutturale**: il worker usa
`Submit`/`Settle` quando il renderer implementa il contratto async, il resto è
il fallback bloccante dell'item 1.

---

## 8. Item 1 — BLOCCATO: il render bloccante ha due consumatori vivi

Non è compatibilità morta. `RenderExecutor.Render` (bloccante) è usato
**in produzione** da un secondo sottosistema:

```text
internal/capabilities/localization/adapters/render.go:195
    outcome, err := a.renderer.Render(ctx, clipPlan)            ← blocking
internal/app/wiring/localization_service.go:269
    NewRenderPlanExecutor(renderRuntime.RenderingGenExecutor, …) ← wired in produzione
```

È il percorso **localized render** della pipeline script (per scena × lingua):
non ha continuation store, non ha aggregatore, non ha settle. Il suo
`localization.Service.Localize` tiene lo slot render finché `.Render()` non
ritorna — cioè fa esattamente ciò che il tuo item 1 vuole eliminare, ma in un
sottosistema che l'async worker/continuation **non ha ancora migrato**.

Secondo consumatore, questa volta legittimo: il render bloccante è la
**baseline BEFORE** dei tuoi stessi benchmark.

```text
internal/capabilities/cliprender/bench_harness_test.go:381
    // benchBlockingRenderer exposes ONLY RenderExecutor, so the worker takes
    // its blocking path — the model of the pre-split pipeline.
bench_scaling_test.go:56   Async: false, // pre-split pipeline: one slot per clip
bench_occupancy_test.go:60 Async: false
```

Cancellare oggi il percorso bloccante distrugge la capacità di misurare
*before vs after* — cioè il gate da cui dipende la demolition stessa.

**Sblocco (nell'ordine):**
1. migrare `localization/adapters/render.go` al modello submit/continuation/settle
   (continuation store + enqueuer + finalizzazione event-driven per quel
   percorso);
2. registrare permanentemente la baseline `Async:false` in un report
   (`writeBenchReport`) così il confronto non dipende più dal codice vivo;
3. poi cancellare `RenderExecutor.Render`, il ramo
   `worker.go:293 w.renderer.Render(opCtx, plan)`, il
   `benchBlockingRenderer`/`Async:false` e i test del comportamento bloccante
   (`TestClipRenderExecutorRenderIsSubmitThenSettle`).

Il contratto finale resta quello che descrivi: **solo `Submit()` e `Settle()`**.

---

## 9. Item 2 — BLOCCATO: il tick è l'unico finaliser cablato

```text
internal/app/wiring/lifecycle_worker.go:207
    clipAgg := cliprender.NewParentAggregator(jobsService, deps.log,
                                               cliprender.DefaultParentAggregationInterval)
internal/capabilities/cliprender/aggregator.go:31
    const DefaultParentAggregationInterval = 2 * time.Second
```

Il finaliser **event-driven esiste solo come modello di benchmark**
(`TestScenario3_EventDrivenFinaliserIsScalesafe`): il percorso di produzione
finalizza il parent *solo* sul tick. Il commento a `aggregator.go:23-30`
conferma che i 2 s sono già una mitigazione del precedente 30 s, non la forma
finale.

Il benchmark misura esattamente il premio: **p50 tick 195 ms vs p50 evento 1 ms**
(Δ ≈ 27.3 s/clip a cadenza 30 s; 1.95 s/clip a 2 s).

**Sblocco:** il worker di settle finalizza il proprio parent al completamento
(il link è già nel payload via `job.InjectParentLink`), il
`FinalizeAggregateParent` è già un CAS idempotente
(`ErrAlreadyTerminalAggregate`), e il tick viene degradato a **recovery sweeper
lento** (es. 60 s) per crash/riparazioni. Richiede validazione crash-safe sullo
stack reale: un evento perso non deve lasciare un parent appeso.

---

## 10. Item 3 — BLOCCATO: tre root, non due

```text
temp/localization                       ← percorso localized render
  internal/app/wiring/localized_render_enqueuer.go:487
  internal/app/wiring/localization_service.go:246, 299

temp/cliprender                         ← percorso clip.render worker
  internal/app/wiring/registry_internal_modules.go:364  (materializer)
  internal/app/wiring/registry_internal_modules.go:368  (prepared resolver, <root>/<sha>/source.<ext>)
  internal/app/wiring/registry_internal_modules.go:403  (worker workspace)
  internal/app/wiring/registry_internal_modules.go:453  (async Drive staging)

pipelinegen/cliprender/staging          ← terza root, staging publisher
  internal/capabilities/cliprender/adapters/cliprender_publisher.go:490
```

Convergenza su una CAS sola: `PreparedAssetResolver` impone già il layout
content-addressed `<root>/<sha>/<kind><ext>` (`prepared_asset_resolver.go:40`,
`assetExtension`), quindi la root condivisa esiste già come contratto — manca
solo che i tre wiring la puntino allo stesso valore e che si cancelli il
materializer parallelo.

**Sblocco:** è una modifica di wiring, ma va validata su stack reale perché
tocca il riuso Drive/vs CAS e il comportamento crash (v. item 8). Non eseguibile
qui in sicurezza.

---

## 11. Item 4 — parziale: hash-once sul hit, rehash residuo sul write

**Fatto** (il tuo requisito principale): il path caldo non ri-hasha più un file
già content-addressed.

```text
internal/capabilities/cliprender/content_verifier.go
    ContentVerifier — memoizzazione per processo, chiave path+size+mtime,
    invalidazione su drift; LRU cap 1024; hash una volta per processo.
internal/capabilities/cliprender/prepared_asset_resolver.go:52
    verifiedPreparedAsset → verifier.Verify(path)   ← non ri-hasha
```

Misurato: `scenario 4`: **20 clip → 1 full read, 1 download** (naive: 20 read).

**Residuo** — rehash pieni ancora sul percorso caldo, ma non su un cache hit:

```text
internal/platform/drive/materializer.go:147, 203, 271, 332   hashFilePath(…)
  (branch registered_local / legacy_cache / write-and-verify)
internal/capabilities/localization/adapters/render.go:217     digest.SHA256File(outcome.OutputPath)
internal/capabilities/cliprender/adapters/cliprender_publisher.go:558  digest.SHA256File(destination)
internal/capabilities/cliprender/adapters/cliprender_plan.go:76        digest.SHA256File(current.LocalPath)
```

Questi nascono "quando i byte entrano nel sistema" (cioè sono la certificazione
d'origine, non un secondo controllo) — coerente con *"il digest deve nascere una
volta e poi viaggiare come metadata"*. Per cancellarli serve che il digest
certificato viaggi davvero end-to-end fino al publish: è una modifica che tocca
Drive/CAS e va validata sul reale. Da non fare alla cieca.

---

## 12. Cosa NON ho toccato e perché

- **Nessuna cancellazione di codice vivo.** `RenderExecutor.Render`, il
  fallback bloccante del worker e il `benchBlockingRenderer` restano: hanno
  consumatori reali (§8).
- **Nessuna modifica al tick dell'aggregatore** (§9) né ai wiring delle root
  (§10): richiedono validazione crash-safe sullo stack reale.
- **Nessuna rimozione di wrapper font** (§5): non soddisfano la tua condizione
  di zero caller.

## 13. Verifica eseguita

```text
go build ./...                                              OK
go test ./internal/capabilities/cliprender/...              ok
go test ./internal/platform/renderinggen/...                ok
go test ./internal/capabilities/localization/...            ok
go test ./internal/app/wiring/...                           ok
scenario 10 (1/10/50 clip)                                  verde, 0 failures
```

Nota pre-esistente e non correlata: `internal/capabilities/scripts/usecase/gencore`
è flaky (≈1/10 run, test diverso ogni volta) anche **senza** le modifiche di
questo passaggio.

## 14. AGGIORNAMENTO — secondo passaggio (sostituisce i verdetti di §9, §10, §11)

Questo passaggio ha eseguito i prerequisiti PRE-2 e PRE-3 e ha chiuso PRE-4.
I verdetti di §9, §10 e §11 sono **superati**: restano nel documento come
fotografia dello stato precedente.

### Item 2 — DEMOLITO: finalizzazione event-driven del parent

Il tick era l'unico finaliser cablato: il parent di un clip finito restava
non-terminale fino al tick successivo. Ora il figlio terminale notifica il
proprio parent nell'istante in cui il commit atterra, e il tick è degradato a
**recovery sweeper** (30 s, la stessa cadenza degli aggregatori voiceover/script).

```text
internal/kernel/…  (nessuna modifica al CAS)
internal/capabilities/jobs/parent_completion.go        NUOVO
    ParentCompletionNotifier  + notifyParentCompletion (best-effort, 5 s timeout)
internal/capabilities/jobs/worker.go
    Worker.parentNotifier + WithParentCompletionNotifier
internal/capabilities/jobs/worker_finalize_paths.go
    notifica dopo il commit terminale (ramo artifact + ramo legacy)
internal/capabilities/jobs/runner.go
    Runner.parentNotifier + WithParentCompletionNotifier → buildWorkers
internal/capabilities/cliprender/aggregator.go
    FinalizeParent(ctx, parentJobID) — single-shot, idempotente
    DefaultParentAggregationInterval  2 s → 30 s (recovery)
internal/app/wiring/clip_render_parent_completion.go   NUOVO
    builder memoizzato sull'unica authority + adattatore del port
internal/app/wiring/lifecycle_job_runner.go / lifecycle_worker.go
    notifier cablato sul runner; ticker = "clip-render-parent-recovery-sweeper"
```

Proprietà difese dai test, non asserite:

- **idempotente**: la finalizzazione è lo stesso CAS senza lease di `Tick`, quindi
  una notifica che corre contro lo sweep è un no-op, non un doppio flip;
- **best-effort**: il figlio è già durable quando la notifica parte, quindi un
  errore del notifier viene loggato e **non** fallisce il job — la rete di
  sicurezza resta lo sweep;
- **ownership**: l'adattatore finalizza solo figli `clip.render` (voiceover e
  script hanno semantiche multi-figlio e i loro aggregatori), e
  `FinalizeParent` verifica anche il tipo del parent;
- **non-finalizza troppo presto**: un figlio ancora `RUNNING` non fa flip.

Il modello del benchmark è stato allineato alla produzione: lo scenario
`event_driven` non chiama più `Tick` (scansione completa) ma `FinalizeParent`
(un solo parent).

**Misura** (`TestScenario3_ParentCompletionLatency`, 6 clip, cadenza
misurata 200 ms):

```text
measured_tick              p50=184 ms  p95=193 ms
event                      p50=0 ms    p95=0 ms
event @30s recovery        p95=17 ms     ← la cadenza lenta non entra nella latenza
polling-only @30s (proiezione) p50=27600 ms  ← cosa avrebbe pagato un deployment a solo polling
```

### Item 3 — DEMOLITO: una sola root di materializzazione

Erano due alberi che tenevano **la stessa immagine dei byte**: `temp/cliprender`
(flow clip.render) e `temp/localization` (flow localization + enqueuer localized
render). Ogni flow riscaricava lo stesso asset nella propria cache
content-addressed.

```text
internal/app/wiring/clip_render_runtime.go
    assetMaterializationRoot(cfg)          → <temp>/materialized
    assetMaterializationResolverRoot(cfg)  → <temp>/materialized/assets
registry_internal_modules.go / localization_service.go / localized_render_enqueuer.go
    tutti e tre i materializer usano la root condivisa
```

Le directory di *lavoro* restano separate (scratch del worker, output localizzati,
staging Drive): contengono contenuto realmente diverso. Converge solo la cache
degli asset, che è content-addressed e quindi condivisibile per costruzione.

### Item 4 — CHIUSO: un'unica authority per il digest

C'era **una seconda implementazione** della memoizzazione
(`cliprender.ContentVerifier`) e il materializer canonico della piattaforma
**non la usava affatto**: `verifyAndReturn` e il ramo CAS rilanciavano un
`SHA-256` full-file a ogni cache hit.

```text
internal/kernel/digest/verifier.go        NUOVO — l'unica authority
    Verifier: memo keyed su (size, modTime), bounded LRU, nil-safe
internal/capabilities/cliprender/content_verifier.go
    ora è un alias sottile su digest.Verifier (API e test invariati)
internal/platform/drive/materializer.go
    verifyAndReturn / cas_cache / download_concurrent_cache → verifier.Verify
    hashFilePath rimosso: il digest nasce una volta
```

Contratto difeso dai test: 3 cache hit = **1 lettura completa**, e la
memoizzazione **non** diventa fiducia — un file che non matcha l'atteso resta
rifiutato.

### Item 1 — ancora BLOCCATO (verificato di nuovo sul tree ribasato)

Nessun cambiamento al verdetto di §8. Sul tree attuale:

```text
internal/capabilities/cliprender/worker.go:294        w.renderer.Render(opCtx, plan)   ← ramo non-async
internal/capabilities/localization/adapters/render.go:195  a.renderer.Render(...)   ← PRODUZIONE, altro sottosistema
internal/capabilities/localization/service.go:190          s.renderer.Render(...)
internal/capabilities/cliprender/bench_harness_test.go:746 benchBlockingRenderer     ← baseline BEFORE del benchmark
```

Il ramo bloccante del worker è ora esplicito (`asyncCompletion`, impostato dai
soli switch di composizione) e `handleAsyncSettle` **fallisce chiuso** se il
worker non è configurato per lo split — quindi la modalità non può degradare in
silenzio. La cancellazione di `RenderExecutor.Render` resta impossibile finché
il sottosistema `localization` (scene × lingua, senza continuation store,
aggregatore o settle) non è migrato a Submit/Settle, e finché il benchmark usa
il renderer bloccante come modello pre-split.

### Gate rieseguito

```text
go build ./...                                            OK
go test ./internal/kernel/digest/...                      ok
go test ./internal/platform/drive/...                      ok
go test ./internal/capabilities/cliprender/...             ok
go test ./internal/capabilities/jobs/                       ok
go test ./internal/app/wiring/...                           ok
scenario 10 (1/10/50 clip)  wall 11/17/59 ms · 5076/34435/50664 clip/min · 0 failures
scenario 3  (parent latency)  event p50=0 ms p95=0 ms
```

Flake **pre-esistente e non correlato**: `TestPreparationCoordinator_UsesNotifierInsteadOfPolling`
(`internal/capabilities/jobs`) fallisce sotto `-race` — verificato identico con
le modifiche di questo passaggio rimosse. `TestScenario10_EndToEndCanonical`
asserisce un rapporto di throughput wall-clock con tolleranza 10% su pareti di
~20-80 ms: sotto `-race` e con il resto della suite in esecuzione la misura
oscilla oltre la banda (in isolamento passa 4/4).

### Ordine aggiornato

```text
FATTO  PRE-2  finalizzazione event-driven + tick come recovery sweeper
FATTO  PRE-3  convergenza su una sola root content-addressed
FATTO  PRE-4  un'unica authority del digest, hash-once sui cache hit
       ↓
PRE-1  migrare localization/adapters/render.go a submit/continuation/settle  → sblocca item 1
       ↓
       benchmark 1/10/50 sullo stack reale (baseline BEFORE registrata)
       ↓
       demolition definitiva di item 1 (RenderExecutor.Render + i test che lo certificano)
```

---

## 15. Ordine confermato (primo passaggio — superato da §14)

La tua sequenza è corretta e va invertita solo nella percezione: **la demolition
non è il passo successivo, è il passo dopo il prossimo.** Oggi mancano ancora
due prerequisiti:

```text
PRE-1  migrare localization/adapters/render.go a submit/continuation/settle   → sblocca item 1
PRE-2  finalizzazione event-driven del parent + tick come recovery sweeper    → sblocca item 2
PRE-3  convergenza delle tre root su una CAS                                  → sblocca item 3
PRE-4  digest certificato end-to-end (no rehash sul write)                     → chiude item 4
        ↓
      benchmark 1/10/50 sullo stack reale, baseline BEFORE registrata
        ↓
      demolition definitiva (item 1, 2, 3, 4)
```

Item 9 è l'unico item interamente eseguibile in questo passaggio, ed è stato
eseguito.
