# TICKET: Cammini di paradigma A (clip alpha posizionata) e B (HUD structured buffer)

- **Data:** 2026-10-28
- **Moduli interessati:** `refactored` (PipelineGen, contratto manifest), `RenderingGen` (producer manifest/lowering), `Chronon3d` (unità di render, compositing), `BrainRust` (`transform_assemble`, consumer wire)
- **Stato:** `MIXED — A1 LAND (wire placement + validazione fail-closed, accettazione locale); A0 CHIUSO (misura negativa: 1,44× vs 8×, variante A2 clip-statiche non procede); A2/A3/A4 SPEC SOLO nella variante ink-based (criterio di riapertura §A0); B0–B3 SPEC (B0 solo misura)`
- **Siblings:** [`TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE.md`](TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE.md) (P0/P2/P3, packed timeline), [`../../../Chronon3d/docs/tickets/TICKET-100X-PERF-WAVES.md`](../../../Chronon3d/docs/tickets/TICKET-100X-PERF-WAVES.md) (B1/B2/B3 engine), [`../../../Chronon3d/docs/tickets/TICKET-PERF-REVOLUTIONARY-IDEAS.md`](../../../Chronon3d/docs/tickets/TICKET-PERF-REVOLUTIONARY-IDEAS.md) (C-1…C-13/I-1…I-24)

> **Nessun claim di 100×** da questo file. I fattori citati sono stime di
> progetto da validare con le misure di fase; ogni fase ha il proprio gate e
> si chiude solo con before/after sullo **stesso snapshot** (regola
> `ops/benchmarks/job-timing-*.json` già in uso per la Pipeline 100x).

---

## 1. Perché due paradigmi e non altre ottimizzazioni

Le leve già in ticket (B1–B3, C-1…C-13, I-1…I-24, P0/P2/P3) ottimizzano il
lavoro **dentro** l'unità attuale: un overlay è un **frame video intero
1920×1080** (`renderinggen.overlay-plan.v1`: `width:1920,height:1080` in ogni
piano). I cammini A e B cambiano l'unità stessa:

- **A** cambia l'unità *spaziale*: da "frame pieno" a "layer posizionato sul
  solo bbox". Collassa insieme fill-rate, readback (P3: 33 MB/frame), encode
  NVENC, VRAM e frammentazione cache.
- **B** cambia l'unità *computazionale*: da "grafo da interpretare ogni frame"
  a "buffer GPU compilato una volta". Collassa il costo per-frame CPU
  (P1: `loop_other` 1,3–2,3 s non attribuiti, espressioni C-10 riparse a
  ogni frame).

I due si moltiplicano: A riduce i byte, B riduce il lavoro sui byte che
restano.

## 2. Baseline misurata di riferimento (dati di repo, non stime)

| Metrica | Valore | Fonte |
|---|---|---|
| Canvas di render per item | 1920×1080 = 2,07 Mpx | schema `overlay-plan.v1` |
| Copertura tipica utile (frase) | ~800×150 = 0,12 Mpx (≈1/17 della tela) | stima geometria, da certificare in A0 |
| Costo engine per item | 218 ms (render 8,12 ms + encode 4,63 ms + ~205 ms setup/IPC/teardown) | MAX-THROUGHPUT §1.4 |
| Servizio per item (3 lane) | 1,43–1,57 s single, 1,07 s batch P4 | `gate-100-r4..r6` |
| Readback GPU→CPU | 33 MB/frame, 23 ms | REVOLUTIONARY §5b P3 |
| Teardown NVENC per job | ~259 ms fissi | MAX-THROUGHPUT §3 |
| `loop_other` (residuo non attribuito) | 1,3–2,3 s su wall 2,4–3,6 s per job 1-frame | REVOLUTIONARY §5b P1 |
| Fast-path frame duplicati | 719/719 su fixture statica | certificazione P1 |
| Pianificazione job (72 overlay) | 4m45s (TTS 2m30 + align 45s + NER) | MAX-THROUGHPUT §4 |
| Throughput certificato | mediana 95,79 item/min (r4: 101,71 su 70 item) | `docs/stato/refactored.md` |

## 3. Cammino A — clip alpha posizionata (unità di rendering = layer)

### A0 — Census bbox sul corpus — **CHIUSO: MISURA NEGATIVA (2026-10-28)**

- **Metodo (harness `a0_bbox_census_test.go` in
  `RenderingGen/renderinggen/internal/overlay/`, env-gated):** geometria del
  piano **compilato** (`CompileSemantic`): rettangolo per-frame (sample su
  ogni frame della finestra, keyframe lineari, motion = delta dal resting
  pose), unione sulla finestra, bleed effetti + margine overshoot 12% + pad
  AA 4px, clamped alla tela. È un **bound superiore conservativo** del rect
  necessario. Corpus: `garlasco_batch_10_20261019_sequential_v7` + run golden
  `overlay_throughput_golden_100_20261022/results` — 9 piani compilati
  (13 scartati: 1 piano storico con `map_animation` ritirata, 12 con item map
  camera che richiedono asset LOD non materializzabili offline), 747 item
  unici (dedup per `render_key`). Report:
  `refactored/ops/benchmarks/a0_bbox_census_20261028.json`.
- **Risultato (fattore = px tela / px unione bbox):**

  | Classe | n | Mediana | p90 | Min |
  |---|---:|---:|---:|---:|
  | `text_phrase` | 448 | **1,44×** | 1,48× | 1,00× |
  | `number` | 69 | 1,46× | 2,51× | 1,40× |
  | `entity_card` | 35 | 1,48× | 1,51× | 1,35× |
  | `image` | 34 | 1,18× | 3,04× | 1,00× |
  | `entity_image` | 30 | 1,07× | 1,24× | 1,00× |
  | layer di piano (watermark ecc.) | 131 | 12,49× | 32,91× | 1,00× |
  | **TOTALE** | **747** | **1,44×** | 11,9× | 1,00× |

- **Gate (≥8×): NON RAGGIUNTO.** Il cammino A **si chiude qui come misura
  negativa** sulla variante A2 specificata (clip statiche per item dalla
  geometria di piano).
- **Causa strutturale (non rumore):** il box testo è **full-width di
  progetto** — `BoxWidth` defaulta a `src.Width` (`text_spec.go`), e sul
  corpus ogni `text_phrase` ha base 1920 px di larghezza (mediana altezza
  280 px); in più i motion di entrata/uscita (slide, reveal) allungano
  l'unione finestra fino a coprire il travel (es. `entity_image` base
  567×756 → unione 1920×1080). La stima 17× era una stima **ink** (glifi
  effettivi, ~800×150): la geometria di piano non può esprimerla perché il
  wrap box contiene l'ink.
- **Ipotesi di salvataggio (NON misurata, nessun claim):** clipping lato
  engine sui **draw/glyph bounds reali** per frame (damage-rect / retained
  mode, convergente con I-1 e con il cammino B): il fattore ink mediano è
  incognito e va misurato con render-probe (diff frame renderizzati, stesso
  protocollo di `mixedDiffBounds`) su item con asset materializzati. Questo
  corpus non consente la misura (asset nel object store, non su disco).
  Criterio di riapertura: ink-factor mediano ≥8× sulla classe `text_phrase`
  con il render-probe.

### A1 — Wire: `placement` nel manifest — **LAND (2026-10-28)**

- **Cosa:** estensione backward-compatible di
  `pipelinegen.overlay-manifest.v1`: campo opzionale `placement` su
  `OverlayManifestEntry` (`x_px`,`y_px`,`w_px`,`h_px`, px sulla canvas del
  piano). Semantica identica agli `artifact_*` già presenti: **assente =
  full-canvas** (percorso attuale, nessuna migrazione), **presente = il
  contenuto della voce si proietta in quel rect**. I quattro valori sono
  set sempre insieme (metà coppia = manifest malformato, fail-closed).
- **Identità (invariante dal contratto §2):**
  - `RetryKey` **non cambia**: placement è metadata di posizione, non
    contenuto (come `track`/`in_us`/`out_us`).
  - `Digest()` **lo copre**: il digest serializza le entry canoniche per
    intero; due composizioni con rect diversi sono composizioni distinte
    (pixel diversi ⇒ artefatto diverso), coerente con
    `OVERLAY-MANIFEST-CONTRACT.md` §2 ("item_id + content_hash + placement").
- **Validazione fail-closed:** producer Go (`manifest.go`) e consumer Rust
  (`pipelinegen-muscles/src/protocol.rs`) rifiutano `w_px≤0`, `h_px≤0`,
  `x_px<0`, `y_px<0` e coppie parziali. Il vincolo `x+w ≤ canvas_w` resta
  producer-side (il manifest non porta le dimensioni canvas; il producer
  conosce il piano e fallisce prima di emettere).
- **Gate di accettazione (chiuso):** test unitari verdi su entrambi i lati
  (Go `internal/capabilities/overlays/model`, Rust `pipelinegen-muscles`),
  nessun cambio di `RetryKey` per le entry esistenti, serializzazione
  deterministica invariata, wire doc aggiornata.

### A2 — Producer: packing spaziale (mosaico bbox) + artefatto con alpha

- **Cosa:** il packed timeline (P0/MAX-THROUGHPUT §3.4) compatta il **tempo**;
  A2 compatta lo **spazio**: le entry vengono renderizzate sulle sole loro
  bbox (motion bounds + padding effetti) e mosaicate su una canvas di
  artefatto ridotta, con `placement` che porta il rect di destinazione sul
  piano. Il compositing sul background avviene **una volta per video** a
  valle (A3), non più sostituendo frame pieni.
- **Formato artefatto (decisione da misura, opzioni in ordine di preferenza):**
  1. **RGB + matte su seconda traccia NVENC** (due stream hardware compatti,
     compositing `alphamerge` lato worker): usa l'encoder esistente, nessuna
     dipendenza nuova; ~2× encode ma su ~1/17 dei px.
  2. RGBA premoltiplicato non codificato nel CAS locale (H1) + compressione
     generica: zero loss, storage maggiore.
  3. **Escluso:** codec con alpha nativo via NVENC — l'A4000 (NVENC 7ª gen)
     non codifica alpha (no AV1); VP9-alpha/libvpx software viola i budget.
- **Gate:** parità pixel sul corpus golden (decode YUV, confronto
  frame-per-frame del composito finale vs percorso attuale — stesso protocollo
  `TestSegmentedSessionMatchesIndependentSingleItemRenders`), before/after
  sulle righe `overlay_render`/`encode` di `ops/benchmarks/job-timing-*.json`
  stesso payload, e roofline verificata (px codificati/item = bbox reale).
- **Rischio noto:** glow/blur/stroke eccedono la bbox testo; il padding
  effetti è parte del gate A0 (mai bbox "tight" senza bleed).

### A3 — Worker: compositing a rect in `transform_assemble`

- **Cosa:** il final worker (BrainRust) proietta ogni voce con `placement`
  come **alpha-blend sulla regione** del frame master (background in
  stream-copy), invece della sostituzione frame-pieno. Le voci senza
  placement restano sul percorso di sostituzione attuale (compatibilità).
- **Gate:** parità visiva e audio vs percorso sostituzione (fixture Garlasco),
  regole fail-closed §5 del contratto invariate (voce oltre base certificata,
  artefatto mancante, righe che condividono byte), retry idempotente per voce.

### A4 — CAS per-entry per `content_hash` (riuso cross-run)

- **Cosa:** le clip bbox sono content-addressed dal `content_hash` della voce
  (già "one content one overlay"): entry identiche tra run/retry non
  rirenderizzano. È la leva steady-state: la produzione seriale ripetuta
  (stesso canale, stessi template) tende a zero lavoro fresco.
- **Vincolo noto (dal contratto):** il drift dei timing TTS sposta le finestre
  e il campione stile è salato da `video_id` run-derived: finché non si
  allinea (voce C3 / render_key placement-invariant), l'hit rate cross-run
  resta misurabile ma parziale. Misurare prima, promettere dopo.
- **Gate:** hit rate misurato su 3 run consecutive dello stesso payload; zero
  artefatti duplicati (ownership `ManifestOwnedCache` invariata).

## 4. Cammino B — paradigma "HUD da gioco" (unità computazionale = buffer)

### B0 — Attribuzione del residuo per-frame (misura di partenza)

- **Cosa:** il primo lotto (REVOLUTIONARY §7, telemetry P1) ha già i bucket
  `loop_prepare_frame` / `loop_graph_eval` / `loop_encode_handoff` /
  `loop_other`. B0 è la lettura del breakdown reale su fixture
  `chronon_text_full_static` e corpus golden: quanto del residuo è
  interpretazione (valutazione piano, espressioni, keyframe, allocazioni) e
  quanto è lavoro irriducibile.
- **Gate:** report con % per bucket. Se `loop_graph_eval + loop_other` < 30%
  del wall item, B si chiude come misura negativa.

### B1 — ADR + specifica del path HUD (obbligo AGENTS.md)

- **Obbligo:** nuove cache/registry/pipeline persistente richiedono ADR
  (`Chronon3d/AGENTS.md` §regole: "No nuovi singleton/registry/resolver/cache
  senza ADR"). Bozza: `Chronon3d/docs/tickets/ADR-DRAFT-HUD-STRUCTURED-BUFFER.md`.
- **Cosa:** compilare il piano overlay in **un structured buffer GPU** per
  job: per item, quad dei glifi (atlas persistente), costanti keyframe
  (transform/opacity/colore, easing risolto a compile time), riferimenti
  texture (forme 9-slice, immagini). Una pipeline persistente, **draw
  instanziati**: il frame non interpreta nulla, la CPU non tocca il piano.
- **Effetto atteso (da misurare in B2, non promesso):** `loop_graph_eval` e
  il re-parse espressioni (C-10) spariscono *per costruzione*; il costo
  per-frame CPU della classe coperta → ordine di decine di µs.
- **Perimetro iniziale:** la classe dominante `text_phrase` con moto
  rigido/fade (la maggioranza dei 25 short + 25 long del mix golden). Gli
  item con animatori per-cluster (word-reveal) restano sul path attuale.

### B2 — Implementazione lane HUD per la classe coperta

- **Gate:**
  - parità pixel **bit-identica** per la classe coperta (stesso raster MTSDF,
    stesso ordine di blend): se non bit-identica, la classe resta sul path
    attuale (fail-closed, nessun dual-path silenzioso);
  - `render_loop_ms` su fixture HUD ≤ baseline/5 (target di progetto; il
    gate misurato prevale sul target);
  - zero allocazioni steady-state (gate già esistente
    `steady_state_allocation_gate`).

### B3 — Estensione + Demolition Debt

- Estendere la lane a image/entity_card (quad texture + 9-slice) **solo** con
  gli stessi gate B2. Poi ritiro del path sostituito (scheda §6).

## 5. Fisica e roofline (per non illudersi)

- 100× su ~96/min ≈ **160 item/s** ≈ 14.400 frame/s a 1080p: impossibile su
  NVENC (satura a ~2–3× realtime per codec pieno). **Plausibile solo come
  prodotto A×B×temporal-sparse**: ~5 frame unici per item (transizioni) su
  bbox ~1/17 di tela + hold replicati (fast-path già 719/719) + encoder
  persistente ≈ 800 micro-frame/s ≈ ~3 GB/s DRAM: la GPU non è il limite,
  lo diventano IPC/CPU che B elimina.
- **Amdahl end-to-end:** il job è 4m45s di pianificazione + ~42s di render.
  100× sul render da solo ⇒ ~5× sul job. Il 100× percepito richiede anche
  C1/C2/D1/E1 (TTS locale batch, align in-process, NER via FFI) — ancora
  ANALIZZATO in MAX-THROUGHPUT §4.
- **Scale-out:** una A4000 ha 1 chip NVENC e 16 GB (12 daemon = impossibile,
  MAX-THROUGHPUT §2.2). Il residuo oltre il tetto single-box è orizzontale:
  il manifest rende gli item portabili, la queue è già multi-worker.

## 6. Scheda Demolition Debt (obbligo `Chronon3d/AGENTS.md`)

| Voce | Impegno |
|---|---|
| Ingresso | A1 wire `placement`; A2 lane mosaico bbox + artefatto alpha; B1 ADR + lane HUD |
| Uscita | alla chiusura di ogni fase, il codice sostituito viene **rimosso**: A2 chiude la emissione full-canvas per la classe coperta; B2/B3 chiudono il path di interpretazione per la classe coperta (incluso il re-parse espressioni laddove la lane HUD lo rende non raggiungibile) |
| Divieto | nessun "nuovo sistema + vecchio sistema" permanente; nessun fallback silenzioso sul path vecchio (fail-closed: errore tipizzato o path esplicito) |
| Eccezioni | solo con nota motivata in questo file o nell'ADR collegato |

## 7. Criteri di accettazione raggiungibili in questo checkout

- [x] **A1:** `placement` opzionale su `OverlayManifestEntry` con validazione
  fail-closed Go + Rust; `RetryKey` invariato; `Digest()` sensibile al rect;
  test unitari verdi su entrambi i lati; wire doc aggiornata.
- [x] **A0:** census bbox eseguito (2026-10-28): **misura negativa**,
  mediana 1,44× vs soglia 8× → A2 (clip statiche per item) chiuso. Vedi §A0
  per metodo, tabella completa e criterio di riapertura ink-based.
- [ ] **B0:** breakdown `loop_other` per componente (richiede run di render →
  gate live).
- [ ] **A2/A3/A4/B1/B2/B3:** SPEC; richiedono implementazione engine/worker,
  ADR approvato e gate golden live. **NON RAGGIUNGIBILI** in una sessione
  analitica: nessun esito è dichiarato.

## 8. Verifica A1 (2026-10-28, accettazione locale)

- Go: `go build` + `go vet` su `internal/capabilities/overlays/...` e
  `internal/capabilities/scripts` OK; `go test ./internal/capabilities/overlays/model/`
  **ok** (inclusi i nuovi `RetryKeyIgnoresRectPlacement`,
  `DigestSeparatesRectPlacements`); test di contratto manifest/overlay del
  package `scripts` (`-run 'Manifest|Overlay|Packed|Placement|FinalJob|PhraseClip'`)
  **ok**; `internal/platform/sqlite/overlayrendercache` **ok**.
- Rust (`BrainRust/pipelinegen-muscles`): `cargo test` **133 unit + 3 + 4
  integration, 0 falliti** (8 test manifest: 4 packed + 4 spatial);
  `cargo fmt --check` e `cargo clippy --all-targets -- -D warnings` puliti.
- `git diff --check` pulito nelle tre aree (root, `refactored`, `BrainRust`).
- Limite dichiarato: è accettazione **locale** del wire, non certificazione
  live; il producer continua a emettere `placement: null` (full-canvas) finché
  A3 non chiude il compositing a rect (§3.2 del contratto).

## 9. Ordine di esecuzione

1. A0 + B0 (solo misure; decidono se A2/B2 procedono).
2. A1 (**fatto**) — è il prerequisito wire di tutto il cammino A.
3. A2 → A3 → A4 (il prodotto dei fattori sta qui).
4. B1 (ADR) → B2 → B3.
5. Rerun golden gate-100 dopo ogni fase (regola esistente), mai attribuzione
   di rate da proiezioni.
