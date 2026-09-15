# Goal 3 — GPU-native lane: authority di coordinate/colore + composizione multi-layer (2026-09-14)

Lane: `chronon3d_cli` Vulkan + NVENC native, `--gpu-hot-path-mode require_gpu_native`
(gli stessi flag del worker di produzione). GPU: RTX A4000. Binario ricostruito da questo
working tree (`ninja -j16` sotto watchdog di memoria, ccache, 304/304 target, 0 errori).

Strumento: `RenderingGen/renderinggen/internal/overlay/goal3_background_watermark_e2e_test.go`
(clip reali + sonde di pixel + **parità cross-backend** native↔reference).

---

## 1. Authority #1 — CoordinateSpace: canvas/center-relative → surface-local

**Sintomo (misurato):** un'immagine di background full-canvas atterrava nel **quarto in
alto a sinistra** (`crop=960:542:0:0`), il resto nero. Il diag del native image path:

```
[native_image_diag]     matrix3=[0.0,0.0,1.0,1.0] anchor=[960.0,540.0]
[native_image_bounds]   dst=[-960.0,-540.0 -> 960.0,540.0]  tx=-960.0 ty=-540.0
```

**Causa:** `Transform::to_mat4()` è `T(position)·R·S·T(-anchor)`, quindi l'anchor è già
dentro `state.matrix`. Il native image path la sottraeva **una seconda volta**, spostando
ogni immagine di `-anchor` (= mezza canvas) e facendo poi clippare il risultato al
quadrante. Il renderer CPU (`image_renderer_draw_detail.hpp`) consuma la stessa matrice e
non sottrae nessun anchor: è quello il comportamento di riferimento.

**Fix:** `src/render_graph/nodes/source_node_native_image.cpp` — `tx/ty` derivano dalla
matrice + il solo offset di placement, senza aritmetica locale di anchor.

**Verifica (immagine-only):** tutti gli 8 punti di sonda mostrano il plate,
`native vs reference MAE = 0`.

## 2. Authority #2 — ColorBoundary: linear-light → transfer del target

**Sintomo (misurato, tre contenuti indipendenti):**

| contenuto nel plan | valore sorgente | native (prima) | reference (software) |
|---|---|---|---|
| immagine (plate) | rgb 30/90/168 | rgb **1/23/96** | rgb 33/89/161 |
| immagine mid-grey | rgb 128/128/128 | rgb **54/54/54** | rgb 128/128/128 |
| layer colore | 0.502 | **54/54/54** | 128/128/128 |

54 = `linear(0.502)·255` e 1/23/96 = i valori lineari esatti del plate: il kernel
FullGraph `RGBA→NV12` scriveva **luce lineare** dentro la matrice YUV, assumendo che la
surface fosse già nel dominio del target.

**Causa:** la surface grafica canonica è RGBA32F **linear**; quel kernel era l'unico
boundary in cui la transfer non veniva applicata (il commento "the surface already carries
the encoder's target transfer domain" era vero solo per il path u8, dove i byte arrivano
già encoded).

**Fix:** l'encode transfer al boundary, **scelto dal target**:

- `cuda_nv12_kernels.hpp`: `encode_transfer_channel(linear, transfer)` (specchio di
  `chronon3d::Color::to_srgb()`) + parametro `transfer` sul kernel `rgba_surface_to_nv12_2x2`.
- `cuda_nv12_compositor_paths.cpp` / `cuda_nv12_compositor.hpp`: `TransferEncode`
  {`TransferNone`=0, `TransferSrgb`=1}, default `TransferSrgb` per il target NVENC SDR
  (che dichiara `AVCOL_TRC_BT709`, coerente col kernel). Il kernel **u8** resta a
  `transfer = 0`: quei byte sono già nel dominio del target.
- La cache PTX NVRTC è keyed sull'hash del sorgente del kernel, quindi il fix invalida
  automaticamente l'artefatto compilato (nessuna cache stantia).

**Verifica (dopo il rebuild), native vs reference:**

| caso | native | reference | MAE |
|---|---|---|---|
| immagine plate 800×800 (cover) | 27/87/165 | 33/89/161 | **0** |
| immagine mid-grey 128 | **128/128/128** | 128/128/128 | **0** |
| layer colore 0.502 | **128/128/128** | 128/128/128 | **0** |
| video background (plate video) | 217/139/91 | 202/124/94 | **0** |

## 3. Authority #3 — NV12 region handoff (hardening; NON era la causa del frame piatto)

La lane native passava al boundary il **rect dirty CPU** (`sw_renderer->last_dirty_rect()`,
una *predizione* del renderer software) come insieme di regioni da convertire, mentre la
destinazione è un frame NV12 **pooled**: le regioni non convertite restano i byte del
buffer riciclato. Il WIP aveva anche una `dirty_regions` in `FullGraphFramePackage`.

**Fix (landed):** `include/chronon3d/media/video/nv12_region_policy.hpp` è l'unica authority
di "quali launch fa il boundary": una conversione parziale è ammessa solo se il chiamante
**prova** la continuità della destinazione. `pipe_export_stages.cpp` non promuove più la
predizione CPU in `out.dirty_regions`; il counter `nv12_partial_handoff_frames` rende
visibile qualunque futuro producer che dichiari continuità senza averla.

**Onestà del risultato:** questa modifica **non** ha cambiato il frame multi-layer
(conversione full/frame confermata da 120 `rgba_to_nv12_frames`), quindi l'ipotesi "regioni"
è **refutata come causa**; resta un hardening di correttezza con test UNIT
(`tests/video/test_nv12_region_policy.cpp`: 10 casi, 59 assertion, verdi).

## 4. Causa del blocco multi-layer: isola fusa non sound (risolta)

**Sintomo:** le clip con **≥3 layer** (background + foreground + watermark) finivano con un
frame che conteneva **un solo layer** (il plate), pur con ogni nodo "eseguito con successo"
e `execution_path=full_graph_native`, 0 downgrade.

**Catena di evidenza (tutta strumentata in questo pass):**

```
[batch-member-skip] node_id=5 name='text' batch_root=3 batch_members=2 consumers=1
[skip-diag]         node_id=5 name='text' reason=static_baked
[skip-policy]       node 5 ('text') skipped as static_baked published no surface
[input-unresolved]  level=3 consumer_id=6 input_index=1 producer_id=5 producer_level=0
[temp-lifetime]     after_level=0 live=[1,2,]        ← il temp del nodo 5 non esiste mai
[composite-inputs]  node6: inputs=2 in0=1 in1=0 → Composite con top null = PASS-THROUGH
```

**Causa (codice):** il pass di fusione (`frame_graph_builder_program_detail.hpp`) accettava
un'isola `{text(5), Transform(3)}` con `root_node = member_nodes.back() = 3`. In esecuzione
`execute_single_node` salta ogni membro non-root (`lowered_into_batch && root_node != id`) e
pubblica `state.shared_transparent` — che è allocato **solo** nel path a tile, quindi sulla
lane native è **null**. Il consumatore *fuori* dal batch (il Composite finale, che legge il
nodo 5) riceve così un input null: un `Composite` con `top` nullo degenera in pass-through
e il layer sparisce. Nessun nodo segnala un errore.

**Perché il caso a 2 layer era corretto:** in quel grafo la fusione non avveniva affatto
(0 occorrenze di `batch-member-skip`), quindi il text veniva eseguito standalone.

**Fix (landed):** `batch_is_sound()` in `frame_graph_builder_program_detail.hpp`: un'isola è
fusa **solo se ogni consumatore di ogni membro non-root vive dentro l'isola**; altrimenti i
nodi restano standalone ed eseguono normalmente. Il guard è applicato a tutti i 3 punti di
commit del batch (2 in `collect_layer_batches`, 1 in `flush_tail_fused_batch`).

**Verifica:** `01_image_background_watermark` (bg + fg + watermark) ora contiene **tutti e tre
i layer** in un frame reale: plate agli angoli, pattern del foreground al centro, banda del
watermark accesa; la riga `[input-unresolved]` e lo skip sono scomparsi (0/run).
Parità native↔reference sul caso immagine: MAE (58.8, 63.8, 60.1) → **(16.8, 45.1, 42.6)**.

## 5. Difetti residui, ora **visibili** perché la composizione funziona

| # | difetto | misura | dove vive |
|---|---|---|---|
| R1 | il plate multi-layer non copre l'ultima colonna/riga (banda nera a destra/basso: area coperta = 7/8 × 7/8) | sonda (1896,1056) = rgb(0,0,0) invece del plate | placement/crop del background image nel grafo multi-layer (la stessa famiglia di §1) |
| R2 | i pixel del **video decodificato** differiscono dal reference (canale G ~45) | `source_watermark` MAE (15.4, 48.2, 8.4) | conversione YUV→RGB/range del path native vs software |
| R3 | il contenuto del foreground **non è deterministico tra processi** | 01 vs 01c: 17–54% di pixel diversi nella scatola del fg | decode/replay del VideoNode nel grafo multi-layer |

R2/R3 non erano osservabili prima perché il foreground **non compariva affatto** nel frame
(era la conseguenza di §4); la determinismo del plan/watermark (frame statici) resta PASS.

## 6. Stato DoD Goal 3 (E2E, GPU reale)

| criterio | stato |
|---|---|
| image background: placement | **PASS** per il caso immagine-only (MAE 0); **FAIL** in composizione multi-layer → R1 |
| image background: colore | **PASS** (plate e gray128 corretti) |
| video background: colore/loop | **PASS** (self-consistency frame 24 vs 110; MAE 0 video-only) |
| **3 layer realmente composti** (bg + fg + watermark) | **PASS** (ex-FAIL: era il blocco §4) |
| watermark presente e locale | **PASS** (banda accesa, nessun bleed fuori dal box) |
| determinismo del plan/watermark | **PASS**; **FAIL** per il contenuto video → R3 |
| non-canvas video plate | **PASS** fail-closed (residency violation, nessun artefatto) |
| structural/decodifica/A-V sync (5 clip) | **PASS** |
| parità cross-backend delle clip multi-layer | **FAIL** (residuo di R1/R2) |

## 7. Riproduzione

```bash
# matrice E2E completa (5 clip + reference software + parità)
cd RenderingGen/renderinggen
GOFLAGS=-p=1 GOAL3_RENDER_OUT_DIR=<dir> \
  go test -count=1 -v -run TestGoal3_FinalClipBackgroundWatermark ./internal/overlay/

# diagnostica del grafo + fixture riusabili per il CLI a mano
GOAL3_DIAG=1 GOAL3_ASSETS_DIR=<dir-fissata> GOAL3_RENDER_OUT_DIR=<dir> \
  go test -count=1 -run TestGoal3_FinalClipBackgroundWatermark ./internal/overlay/

# riesecuzione a mano di un plan registrato (3 frame, ~1 s)
CHRONON3D_NATIVE_SURFACE_PROMOTION_DIAG=1 CHRONON3D_DIAG_EXEC_LOG=1 \
  chronon3d_cli render --plan <dir>/01_image_background_watermark_plan.json \
    --assets-root <dir-fissata> --backend vulkan --hardware nvenc \
    --encoder-backend native --gpu-hot-path-mode require_gpu_native \
    --gop-source <dir>/assets/semantic/goal3-foreground/source.mp4 \
    --start-frame 0 --end-frame 1 -o /tmp/probe.mp4
```

Ogni run lascia `<nome>.log` accanto agli artefatti (traccia completa del CLI), così la
diagnosi non dipende dal processo che ha prodotto il frame.

## 8. Inventario delle modifiche di questo pass

**Chronon3d (engine)**

| file | contenuto |
|---|---|
| `src/render_graph/nodes/source_node_native_image.cpp` | authority #1 (placement) |
| `src/media/video/compositor/cuda/cuda_nv12_kernels.hpp` | OETF al boundary |
| `src/media/video/compositor/cuda/cuda_nv12_compositor_{paths,}.cpp`/`.hpp` | transfer guidata dal target + policy delle regioni |
| `include/chronon3d/media/video/nv12_region_policy.hpp` | **nuovo**: authority delle regioni NV12 |
| `src/render_graph/compiler/frame_graph_builder_program_detail.hpp` | **fix §4**: `batch_is_sound()` |
| `src/render_graph/executor/{executor_levels,node_runner_single_node_detail,node_skip_policy}.*` | diagnostica bounded (graph-edge, input-unresolved, temp-lifetime, composite-inputs, batch-member-skip, skip-diag) + log d'errore su skip che non pubblica nulla |
| `src/render_graph/nodes/composite_node.cpp` | diag `[composite-inputs]` |
| `apps/chronon3d_cli/commands/video/common/{pipe_export_stages,pipe_export_queue}.hpp` | niente più dirty-rect CPU al boundary |
| `include/chronon3d/core/profiling/render_counter_macros.hpp`, `apps/.../telemetry_{capture,merge}.hpp` | counter `nv12_partial_handoff_frames` |
| `tests/video/test_nv12_region_policy.cpp` + `tests/video_tests.cmake` | **nuovo** test UNIT (10 casi / 59 assertion, verdi) |

**RenderingGen (orbit E2E)**: `internal/overlay/goal3_background_watermark_e2e_test.go`
(`GOAL3_DIAG`, `GOAL3_ASSETS_DIR`, log del CLI per clip).

**PipelineGen (refactored)**: contratto background `Kind` + mapper/staging + fan-out
localization (dal pass precedente), gate verdi (`gofmt`, `vet`, test cliprender /
renderinggen / localization / wiring / kernel; `archcheck`: 1 violazione pre-esistente
`stockpipeline` 63→65, debito registrato, non di questo pass).
