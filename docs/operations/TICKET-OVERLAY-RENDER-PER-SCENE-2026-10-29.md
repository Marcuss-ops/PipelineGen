# TICKET — Overlay render per-scena (entiame già in overlap: design correzione)

Data: 2026-10-29 · Stato: **DESIGN CORRETTO** (soprintende la versione appena
scritta che parlava di pre-warm cache-hit: trovato contrattualmente impossibile)
Priorità: alta — è l'ultimo collo seriale dopo il grosso TTS a frase lo stesso
giorno (TICKET-TTS-CRITICAL-PATH-SENTENCE-CHUNK-2026-10-29).

## Obiettivo

Sovrapponibilità: la coordinator vuole lanciare il render overlay della scena K
im appena (1) TTS della scena K è finalato e (2) gli offset `TimelineStartUS`
delle scene 0..K-1 sono noti. Con N scene, il 92% del wall del render overlay
scappa fuori dal piede `COMPILING_AUDIO` (oggi parte solo dopo che TUTTE le
scene hanno finito TTS + translation + timing projection).

## Diagnosi live (job `tts-chunk-verify-v2`, ~143 parole, 1 scena)

| Tratto | Istante UTC | wall |
|---|---|---|
| TTS scena 0 fine | 10:27:32.44 | **3,6 s** (vs 24 s baseline) |
| join GENERATING_SCENE_TEXT | 10:27:40.56 | +8,1 s (render clip localizzato + join semantico) |
| overlay plan compiled | 10:27:40.72 | 0,14 s |
| run completed | 10:27:49.99 | **+9,2 s bordati all'attesa del render overlay** |

Per N scene > 1, ogni scena K con K < N−1 aspetta in coda di join prima di
pagare il proprio ~9 s di render GPU — **che l'architettura rende invece
sovrapponibile ai TTS delle scene successive** (they cost ~3,6 s ciascuno).

## Cosa esto GIÀ in overlap (non rifare)

1. **Estrazione/prepare**: `runVidRushJoinAndPrepare` (branch di fondo
   `SceneTextReady`, runner_overlay_prepare.go) — già in parallelo col TTS.
2. **Mappe/cali unici**: `PreMaterializeDynamicMapVideo` è già FUORI dal
   boundary di submission (render_queue_items_batch.go:77–89): una prepare
   stessa mappa in volo è JOINED mai doppia. Video mappa di contenuto identico
   serve multiple lingue,una sola volta.
3. **Render localizzati** (clip-video): già per-scena sparati dalla
   coordinator al TTS-join.

## Il collo reale, certificato dal meccanismo

La manifest-timeline produce **UNO solo artefatto certificato per timeline
(inter-o-runa)**:
- chiave di cache = `manifest.Digest()` + plan context digest + epoch
  (`overlayManifestCompositionCacheKey`) — **la per-item `RenderKey` cache
  non è MAI colpita dai Vorlage della composizione**: un pre-warm per-item
  cache non sorta alcun effetto nella contenizione.
- il final-job hand-off (`finalJobManifestWire`) e il decode-replace
  semantico del worker assumono **un solo artefatto** per timeline:
  "every entry reads from one artifact".

**Conseguenza**: rendere la scena K come composizione separata
(candidata finché TTS-ready) significa emettere N artefatti timeline per
lingua. I consumer di valle non sono preparati: il decode-replace del
worker salva finché un solo artefatto per ogni finestra; richiede un
**contratto worker versione versione (manifest schema_version + decode
per batch)**, non un rimpegno client.

## Design (cast minimo vero)

### Gand (A) — Wire multi-artefatto per scena

1. `buildManifestEntryWorks` ad ogni scena K produce un suddigest con
   solo le vocidella scena K (`manifest.scene_digest = digest delle
   ENTRY la cui scena è K`) — digest propria-verità, content-addressed,
   non plan filtered.
2. `enqueueOverlayManifestTimeline(ctx, plan, candidates, sceneWindow)`:
   experts per lavoro batch-scena = **una composizione per scena**,
   plan t=0 anchoring invariato perché ogni entry porta in_us/out_us
   plan-assoluti (già così ogni volta), packed composition rinuncia
   agli span vuoti dentro la scena.
3. La reference per lingua al join unifica N batch results, una per
   scena e una per Scenario:
   ```go
   reference.Batches = append(reference.Batches, OverlayBatchRenderReference{
       BatchID: sceneManifest.Digest(), SceneID: sceneID,
       StartUS: sceneStartUS, EndUS: sceneEndUS,
       JobID: ..., Status: ..., Artifact: sceneArtifact,
   })
   ```
   La `Batches` già esiste, e `overlayArtifactForItem` di editing_timeline
   implementa tu per-item lookup per items → nessun consumer cambia forma,
   solo il manifest → multi-batch validation.
4. **Contratto worker nuovo** (renderinggen + worker decode): il wire
   `mode=replace` con entry che citano `artifact_sha256` per batch, il
   worker decode-replace legge le finestre dall'artefatto batch-scena
   invece che dall'united timeline. SSOT gate: un item come max un
   artefatto — resta.

### Gand (B) — Advance fallback se (A) è rifiutato dal contract worker

Divelled "big-bang": manterieni la composizione UNICA ma la sua
compresa (colonna "#") un solo `EnqueueChrononPlan` per lingua *non*
arriva semplicemente ancorata prima che TUTTI i TTS siano in cache:
la finestra di lavoro del join del COMPILING_AUDIO si accorcia, la
composizione GETTATa una sola volta quando l'ultima scena è finalata.
Rifiuto: solo deferral (incassare e than riusposta) non è un miglioramento
vera del piede.

## Codice per la path A

1. `overlay_manifest_timeline.go` (298 linee): aggiungere `sceneWindow`
   argomento a `enqueueOverlayManifestTimeline` e `partitionOverlayRenderWork`
   (scene filter); il digest dentro il caller resta per-scena.
2. `runner_phase_overlay_render.go`: la coordinator canna e la fase
   `renderOverlayPlans` scrive nel Outcome già presente.
3. `render_queue_batches_test.go`: aggiungere pin per "un batch-scena
   senza occupazione ring con la stessa ordinazione di oggi".
4. `editing_timeline.trace_contract_test`: N batch per scena K > 0; il
   per-item artifact lookup non cambia forma.
5. `TICKET-OVERLAY-RENDER-PER-SCENE-2026-10-29`: verifiche others con
   il nuovo contratto.

## Bind golden & check che chiriamo

- golden 100-scene (`ops/jobs/overlay_throughput_golden_100_*`): la
  **stesura del multi-batch** non tocca la forma per scena→identità;
  la [distinction] della wall 100-scene scende proporzionalmente.
- `final_job_overlay_ssot_gate_test.go`: portare dietro al multi batch
  digest > 1 per lingua, or (a) fail prior to change, replaces with the
  per-batch duplicate check (fest docked: no same artifact describes 2
  overlays al df) — golden 2026-10-24.
- `final_job_overlay_manifest_test.go`: portare `admittedFrames` in
  forma per batch (`sceneWindow`).
- pin del worker decode-replace: "una finestra UNA fonte" con N batch.
- `scene_trace_contract_test.go`: N batches per scena K > 0 in ordine
  scena-first, e la coordinator fire per ma scena in ordine.

## Impatto stimato (N=8, ~80 s wall oggi)

| Tratto | Oggi | Dopo (Gand A) |
|---|---|---|
| Render overlay finito per scena K | fine run | TTS-join + 9 s localizzato |
| Piede post-tts (run_END - TTS_end) | ~T_render(N) | ~T_render(1) + max (9 s, TTS prozione residue) |
| Wall totale | 80 s | ~45–55 s (~35% risparmiato) |

## Passo che NON è per-scena (riforma locale della CO) — refuso eliminato

Il ticket precedente pensava *pre-warm cache* per le voci: il digest della
chiave composizione è `manifest.Digest()`, l'intero timeline — un sub-piano
scena K produce N digests diversi e cache-miss sempre. Lo slate è
soprintende e non implementabile come design. La path (A) resta l'unica
con 35% wall reale.

## Stato implementazione (2026-10-29)

**Gand A: IMPLEMENTATA e suite verdi.** Evidenza local-tree (worktree PipelineGen,
`go test ./internal/capabilities/scripts/ -count=1` → PASS 13.838 s):

- `render_queue_split.go` (nuovo): `groupManifestCandidatesByScene` (ordine
  scena-first da `groupFirstIndex`), `planSpansMultipleScenes` (conta solo il
  lavoro manifest: mappe/chain restano sul percorso dedicato),
  `EnqueueChrononPlanSplit` (una composizione per scena via `concurrent.Map`
  con `OVERLAY_RENDER_PER_SCENE_WORKERS`, default 2),
  `admitManifestCaptionWindows` a livello PIANO prima di ogni GPU (refuse-set
  identico al path singolo), `assembleSplitManifestReference` (N batch in
  ordine piano, righe per-item con l'artefatto della scena proprietaria,
  top-level = prima scena, `Manifest` deliberatamente nil).
- `runner_phase_overlay_render.go`: `renderOverlayPlanAtBoundary` è l'unica
  scelta della forma di submission (gate `OVERLAY_RENDER_PER_SCENE`, mai per
  FinalJob, mai per piani single-scene, capability-check
  `OverlayPlanSplitEnqueuer`); fallback fail-closed al path singolo su errore.
- `runner_overlay_render_barrier.go`: barrier a livello LINGUA
  (`OVERLAY_RENDER_LANG_BARRIER`, default off) che lancia il rendering delle
  piante dalla P1 branch mentre il master audio compile è ancora in volo; il
  join OnPlanReady applica i risultati in ordine piano deterministico.
- `overlayArtifactForItem` (model_result.go): per-item lookup via righe
  `Items` + `Batches` — nessun consumer cambia forma.
- Pin di test (`render_queue_split_test.go`): piano multi-scene splitta,
  single-scene non splitta, ordine scena-first, fail-closed senza client,
  fallback su errore split, `FinalJob` mai split, flag OFF = path singolo,
  `TestAssembleSplitManifestReference` (N batch, righe con artefatto della
  propria scena, dedicated merged, metrics multi-batch).

**Residui aperti (NOT RUN / bloccati host):**

- [ ] Misura wall fuori da `COMPILING_AUDIO` before/after su job 100-scene
  reale (>1 lingua, workload comparabile al tratto `tts-chunk-verify-v2`) —
  richiede worker RenderingGen live; la stima ~35% resta stimata, non misura.
- [ ] Contratto worker decode-replace multi-artefatto per FinalJob (i final
  job NON splittano oggi: il wire single-manifest resta la SSOT finché il
  contratto `manifest schema_version + decode per batch` non atterra — vedi
  `OVERLAY-MANIFEST-CONTRACT.md` §4).
- [ ] Golden `overlay_throughput_golden_100_*` su host con la queue live.
- [ ] Inserire la scheda nel version control (file attualmente untracked).

Collegamento al gate ≥100 overlay/min: `TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE.md`
(#57) — questo item è il collo seriale finale di quel gate.
