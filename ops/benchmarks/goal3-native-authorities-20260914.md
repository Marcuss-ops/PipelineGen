# Goal 3 — GPU-native lane: due authority corrette e misurate (2026-09-14)

Lane: `chronon3d_cli` Vulkan + NVENC native, `--gpu-hot-path-mode require_gpu_native`
(stessi flag del worker di produzione). GPU: RTX A4000. Binario ricostruito da questo
working tree (`ninja -j10`, ccache, 287/287 target, 0 errori).

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

**Verifica (immagine-only, dopo il rebuild):** tutti gli 8 punti di sonda (4 angoli,
bordi, centro) mostrano il plate, `native vs reference MAE = 0`.

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
  `transfer = 0`: quei byte sono già nel dominio del target e una seconda OETF è proprio
  la regressione che il commento storico documentava.
- La cache PTX NVRTC è keyed sull'hash del sorgente del kernel, quindi il fix invalida
  automaticamente l'artefatto compilato (nessuna cache stantia).

**Verifica (dopo il rebuild), native vs reference:**

| caso | native | reference | MAE |
|---|---|---|---|
| immagine plate 800×800 (cover) | 27/87/165 | 33/89/161 | **0** |
| immagine mid-grey 128 | **128/128/128** | 128/128/128 | **0** |
| layer colore 0.502 | **128/128/128** | 128/128/128 | **0** |
| video background (plate video) | 217/139/91 | 202/124/94 | **0** |

La clipboard DoD "gray 128" è rispettata: il native era 54, ora è 128 come il reference.

## 3. Bloccante residuo (NON introdotto da questo pass): composizione multi-layer native

Con **più di un layer** il frame nativo non riproduce la composizione:

| caso | native | reference | MAE |
|---|---|---|---|
| image bg + fg video (70%) + watermark | campo **piatto** (bordo 245/245/245) | plate + box fg + watermark | **58–64 per canale** |
| video bg + fg video + watermark | **solo il plate video** (`MAE(02, video-only) = 0`) | plate + fg + watermark | **60–69** |
| video source + watermark | 15/48/8 | — | **15/48/8** |

Il diag mostra che i composite nativi **girano** (2 nodi per frame, `result=1 affine=0`,
1920×1080, `cpu=0`), quindi il difetto non è nel compositing ma nella consegna al
boundary: la surface convertita a NV12 non è quella che la catena di composite ha
prodotto (gli handle risultano **riusati da pool**, es. `result_handle=2` su frame
diversi, e l'output coincide con il solo layer di base). Area del repository coinvolta:
handoff terminale / regioni dirty (`scene_native_output.*`, `frame_delta_compiler.*`,
`dirty_safety_policy.*`, `tile_execution_policy.cpp`, `composite_surface_to_nv12(dirty_regions)`).

**Evidenza che è pre-esistente:** lo stesso bordo bianco a `(24,24)` era presente anche nel
binario costruito **prima** di questo pass (misure del turno precedente), quindi non è un
effetto delle due correzioni sopra.

## 4. Stato DoD Goal 3 (E2E, GPU reale)

| criterio | stato |
|---|---|
| image background: placement | **PASS** (canvas completo, MAE 0 immagine-only) |
| image background: colore | **PASS** (plate e gray128 corretti) |
| video background: color/loop | **PASS** (self-consistency frame 24 vs 110; MAE 0 video-only) |
| watermark presente | **PASS** strutturale (plan statico pinnato, nessuna animazione) |
| determinismo (stesso plan, 2 processi) | **PASS** (frame identici) |
| non-canvas video plate | **PASS** fail-closed (residency violation, nessun artefatto) |
| structural/decodifica/A-V sync (5 clip) | **PASS** |
| foreground placement / watermark pixel in composizione | **FAIL** → bloccante §3 |
| parità cross-backend delle clip multi-layer | **FAIL** → bloccante §3 |

## 5. Riproduzione

```bash
cd RenderingGen/renderinggen
GOFLAGS=-p=1 GOAL3_RENDER_OUT_DIR=<dir> \
  go test -count=1 -v -run TestGoal3_FinalClipBackgroundWatermark ./internal/overlay/
```

I test sono opt-in (skippano senza `CHRONON_BIN`/binario); le clip e i plan generati
finiscono nella directory indicata, con il reference software accanto per il confronto.
