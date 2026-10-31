# TICKET — Overlay render per-scena (entiame già in overlap: design correzione)

Data: 2026-10-29 (agg. 2026-10-31) · Stato: **PARTIAL** — split per scena
implementato e **default-on** per i job non-final (`d94109fab`), con epoch cache
stabile e tre run live 30-10 a mediana **77,32 item/min** (65–70 item, senza
mappe; gate ≥100 ancora FAIL). Restano il wire/consumer multi-artefatto per i
final job (§11) e la certificazione del mix 100 item con mappe.
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

- [x] Misura wall fuori da `COMPILING_AUDIO`: ESEGUITA live oggi (run
  `split-per-scene-20261029-r1`, job `job_1793272410632223240_b91456a9`,
  payload `overlay_throughput_golden_90_20261028.v1-nomaps.generate.json`): la
  finestra overlay passa da 657,3 s (flag OFF, baseline
  `gate-90-nomaps-20261028-r1`) a **101,3 s (−85%, ×6,5)**; throughput
  6,3 → **39,7 overlay/min**; 15 batch per-scena, 67/67 item COMPLETED,
  zero fallback split, zero `worker_count` mismatch. Evidenza:
  `ops/jobs/overlay_throughput_golden_100_20261022/results/split-per-scene-20261029-r1/summary.json`
  e sezione "Split render per-scena" del README del golden. Caveat: baseline
  e2b vs run e4b (mix leggero diverso); confronto per-classe confermato
  (50 text_phrase in entrambe). Il gate ≥100 overlay/min (contratto mediana
  di 3 run comparabili) NON è a soglia: 39,7 < 100 — il residuo è attribuito a
  `queue_wait_ms` (~20 s/batch) e alla fase `generate` (46,7% del critical
  path), che appartengono al contratto di  `TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE.md` (#57), non a questa scheda.
- [x] Split default-on e cache epoch stabile (30-10): `d94109fab` rende
  `OVERLAY_RENDER_PER_SCENE` ON di default (kill switch esplicito) e usa il
  commit/digest come epoch; verifica live `/health` con
  `render_cache.epoch=d94109fab`. Le run `closeout-20261030-r{1,2,3}` danno
  54,44 / 80,04 / 77,32 item/min → mediana **77,32**, gate ≥100 FAIL; il
  packed cross-scene è stato provato e **non promosso** (43,73/min).
- [ ] Contratto worker decode-replace multi-artefatto per FinalJob (i final
  job NON splittano oggi: il wire single-manifest resta la SSOT finché il
  contratto `manifest schema_version + decode per batch` non atterra — vedi
  `OVERLAY-MANIFEST-CONTRACT.md` §4).
- [ ] Golden `overlay_throughput_golden_100_*` su host con la queue live: le
  tre run `closeout-20261030-r{1,2,3}` sono eseguite (65–70 item, mediana
  77,32/min), ma il mix 100 item con mappe resta NOT RUN.
- [x] Scheda inserita nel version control (`16e6b39ca`; verificata con
  `git ls-files` il 2026-10-31).

Collegamento al gate ≥100 overlay/min: `TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE.md`
(#57). La baseline comparabile più recente è §9.10/§11: mediana 77,32 item/min
senza mappe (30-10); questo split da solo non chiude il gate né prova che ogni
collo seriale sia risolto.

## 11. Contratto multi-artefatto e partizione sicura — passi residui

**Stato: SPEC / NOT RUN.** Coerenza con il ticket principale §10.3–10.4.
L'implementazione corrente assegna un item che attraversa scene alla scena
iniziale e fa fallback al piano unico su errore di split; entrambi i fatti
vanno considerati esplicitamente nell'analisi e nei test. Il final job R14
(30-10) ha chiuso l'handoff HTTPS e assemblato 11 frammenti, ma resta sul
percorso singolo: il contratto multi-artefatto non è cambiato.

1. Inventariare i casi non indipendenti: overlap fra scene e track,
   transizioni, layer che attraversano boundary, video background sincronizzato,
   filtri con storia temporale, caption, mappe e chain. Stabilire quali
   finestre debbano rimanere nello stesso batch; non dividere solo usando
   `SceneID` quando cambia i pixel.
   **PARZIALE (2026-10-29):** il caso overlap è ora coperto.
   `groupManifestCandidatesIntoMicroBatches` (`render_queue_split.go`) parte dai
   gruppi di scena e li raffina in componenti connesse di finestre letterali
   sovrapposte: item sovrapposti restano sempre nella stessa composizione,
   componenti disgiunte diventano batch separati in ordine di piano. Test:
   `TestMicroBatchesNeverSplitOverlappingItems`. Restano aperti i casi non
   temporali (video background sincronizzato, filtri con storia temporale,
   transizioni) che richiedono verifica pixel-level.
2. Modellare una revisione versionata del manifest con lista di artefatti
   verificati e riferimento entry→artefatto, `in_us/out_us` di piano,
   `artifact_in_us/out_us` packed, griglia fps e versioni; ogni entry deve
   avere un solo owner. Preservare retry e chiavi di composizione; specificare
   cosa accade per una sovrapposizione fra artefatti diversi.
3. In `final_job_overlay_manifest.go` e nel producer usare solo fatti
   certificati; mantenere final-job sul percorso corrente fino al cutover.
   In BrainRust, `transform_manifest.rs` già implementa replace con un singolo
   artefatto e ricodifica; estenderlo solo dopo aver definito ordine e
   rappresentazione degli overlap. Il closeout P5 è locale, non prova il
   deploy del consumer remoto. Un semplice replace full-frame di batch diversi
   non preserva necessariamente z-order o sfondo.
4. Testare input Go→wire→Rust: 1/N batch, scene-crossing, finestre sovrapposte,
   griglie razionali, limiti, clip phrase-only, mappe/chain, missing/tampered
   asset, ordine deterministico, retry/cancellazione e crash-resume. Confrontare
   output decodificato, audio e durata contro il percorso singolo; usare
   tolleranze dichiarate quando l'encoder non garantisce bit identity.
5. Misurare gli stessi workload e lane prima/dopo, inclusi numero batch,
   servizio, attese, fallback e wall. Promuovere il final-job solo dopo
   consumer effettivo aggiornato e golden completo; deploy/restart sono
   operazioni separate da autorizzare. Se la partizione non è equivalente,
   tenerla sul percorso unico e riportare il blocco, non aggirare la verifica.

## 12. Prima modifica locale: lavoro dedicato sovrapposto allo split

Nel worktree, `EnqueueChrononPlanSplit` avvia le composizioni per scena e i
candidati sul percorso dedicato (mappe/chain) in parallelo, anziché attendere
la fine di tutte le scene prima di sottomettere i candidati dedicati. Rimangono
invariati admission plan-level, limiti dei pool e authority GPU di RenderingGen,
merge in ordine piano e fallback esplicito al percorso unico sul boundary.
Se un ramo fallisce l'altro riceve cancellazione e non viene restituito un
risultato parziale. La modifica interessa solo piani non-final con split attivo
che abbiano anche item dedicati; non accelera automaticamente piani senza di
essi. Test mirati con race detector verificano sovrapposizione, cancellazione
e ordine; **nessun benchmark live before/after, nessun throughput dichiarato**.
Il ticket principale §10 resta aperto.
