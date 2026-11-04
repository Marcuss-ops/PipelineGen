# RenderingGen

GPU overlay rendering worker. On the local GPU host the Queue, worker and
Chronon run natively; Docker is reserved for PostgreSQL and the object store.
The image remains available for CI and distributed production workers.

## Architecture

```
PipelineGen -> Central Queue -> RenderingGen workers (same image)
                                   -> Artifact Storage (L3)
                                      + local cache (L2 NVMe) + VRAM (L1)

Local runtime:

```
systemd: PipelineGen + Queue + RenderingGen + Chronon3d -> localhost services
Docker:  PostgreSQL:5432 + objectstore:9000
```
```

RenderingGen never knows about the provider (Runpod, local, ...). It only
receives config: queue endpoint, artifact store, Chronon backend, GPU device.

Typography/overlay ownership is split by boundary, not duplicated: PipelineGen's
`script.OverlayStyleSpec` and supported-family contract are canonical for the
request; its font asset map binds every selectable ID to a path+SHA and stages
only referenced families; the `renderinggen.overlay-plan.v1` schema is the wire
contract; RenderingGen validates and lowers that plan through one runtime font
resolver and one text-style lowering path. Chronon owns rasterization, not an
independent family-selection policy. Image captions are separate linked text
layers emitted once by RenderingGen; composition/entity styles supply geometry,
motion and fallback color, not a competing font source. Cross-boundary parity
is pinned by PipelineGen's `TestCanonicalFontAssetsParity` and RenderingGen's
schema/runtime font and caption precedence tests.

Workers **pull** jobs from the central queue (claim -> render -> complete/fail);
they are never pushed HTTP requests directly.

## Repository layout

- `renderinggen/` — Go worker (queue client, storage/cache, GPU detect, health)
- `queue/` — central pull-based job queue (claim + lease expiry); HTTP contract documented in [`queue/README.md`](queue/README.md)
- `contracts/` — JSON Schemas for the cross-service wire contracts (`renderinggen.job.v1`)
- `objectstore/` — central object storage (L3) used by the worker cache
- `infra/docker/` — infrastructure Compose and CI/production worker image
- `infra/native/` — host-native configs and environment template
- `infra/systemd/` — host-native Queue and worker units
- `.github/workflows/` — CI: build, test, publish versioned images on `main`

## Overlay contract and renderer

PipelineGen emits the semantic `renderinggen.overlay-plan.v1` contract. The
RenderingGen worker validates and compiles that plan locally into Chronon's
`chronon.render-plan.v2`, materializes its content-addressed assets, and only
then invokes Chronon3d. Concrete Chronon plans are not accepted on the production semantic job path; RenderingGen owns the only lowering chain.

A job **must declare every font its text needs**, including the coverage font for
the script: Chronon3d builds its fallback stack by scanning the directory of the
plan's primary font, while the worker materializes only the assets the job
declares, so an undeclared font simply does not exist for the shaper and the text
renders with missing glyphs (or fails at frame 0). See
[`multilingual_overlays_v1/README.md`](multilingual_overlays_v1/README.md) for the
measured evidence, the matrix of 10 translated overlays across the 10 configured
languages, and the reproduction commands.

## Organizzazione editoriale: sezioni, classi e renderer

Per scegliere un overlay senza confondere il contenuto con la tecnica, usare
questa gerarchia: **sezione editoriale → tipo semantico (`kind`) → template / preset
visivo → animazione (`motion_id`) → renderer**. Una sezione editoriale è un
raggruppamento per chi crea il video; non è un renderer, né implica un nuovo
`kind` nel contratto.

| Sezione editoriale | Modello attuale | Cosa distingue |
|---|---|---|
| Entità con testo + 1 immagine | `entity_card` con un asset e caption | Un’unica entità genera ritratto e didascalia animata; non è un composito. |
| Immagine singola | `entity_image` con un asset | Una scheda immagine indipendente, senza caption obbligatoria. |
| Immagini x2, x3, x4, x5… | `entity_image` con `image_layers` | Una voce semantica, da 2 a 5 layer/assets (limite di schema `maxItems: 5` e di compilazione) con motion indipendente per layer. Un gruppo `entity_group` segue lo stesso limite 2..5; sei membri vengono rifiutati. |
| Frasi importanti | `important_phrase` | Frasi editoriali, con stile testo separato dall’animazione. |
| Frasi importanti brevi | `important_phrase` + stile/motion breve | 26 stili `short_phrase_style` (picker `short_important_phrase`, disgiunto da `important_phrase`); nessuna famiglia `typewriter_modern_v1` nel catalogo live (26 famiglie totali). |
| Numeri, metriche e date | `number`, `metric_stat`, `timeline_date` | Picker live: 30 motion metric (`metric_v1` 20 + `metric_didone_v1` 10), 31 motion date (`date_v1` 21 + `date_didone_v1` 10); `entity_card_v1` ha altri 10 motion per schede entità. |
| Mappe | `map` | ChrononPlugin è l'unica fonte del rendering mappe. Il renderer nativo (`maps_dynamic_map_render`) pubblica il vocabolario in `ChrononPlugin/plugins/maps/maps-renderer-catalog.v1.json` (25 composizioni documentary + base flyover, camera `slow_approach`/`exponential_dive`, basemap `chronon_editorial`/`esri_sat`); RenderingGen incorpora quello stesso catalogo e rifiuta per nome ogni `map_animation` fuori da esso, incluse le vecchie famiglie Python (`region_zoom`, `pulse_marker`, `ed_*`). |
| Altri elementi | `important_word`, `quote`, entità testuali, `product`, `logo`, `shape`, `video_overlay`, ecc. | Rimangono classi supportate dal compilatore; non vanno mescolate alle sezioni principali dell’editor. |

Quindi la proposta editoriale parte da **7 sezioni principali** (le prime sette
righe) più “Altri elementi”. Lo scenario concatenato già presente, invece, ha
**6 atti runtime**: frasi, date, metriche, entità, luoghi+mappa, immagini. Date
e metriche sono separate nello scenario per provare le due famiglie, pur
appartenendo alla macro-sezione editoriale “Numeri, metriche e date”.

Nei piani nuovi, `composition_id` può identificare una delle dieci
composizioni immagine del catalogo runtime. RenderingGen verifica il numero
esatto di immagini e, nelle varianti “con testo”, richiede da una a N caption.
Il campo è opzionale: i piani esistenti senza `composition_id` restano validi.
Su un composito, la caption va dichiarata sul relativo `image_layers[]`; il
campo item-level `entity_caption` resta per l’immagine singola.

**Fotografia runtime verificata:** c’è **1 renderer** (Chronon3D); il worker
RenderingGen compila il piano semantico, non è un secondo renderer. Il registry
contiene **20 `ItemKind`**, **29 template**, **10 preset ufficiali** (2 testo +
8 immagini) e **6 comportamenti di compilazione** (testo, entità, immagine,
video, shape, mappa). Sono conteggi di livelli diversi: kind/template/preset non
sono sinonimi di “sezione”. **Conteggio verificato il 2026-10-16** con
`go run ./cmd/motion-catalog`: il runtime espone **438 motion ID**, **26 famiglie
selezionabili** e **22 use case**. È una vista compilata: include l’artefatto
ChrononTemplate incorporato, i pack C++ e i cataloghi metric/date/entity.
Generala al momento del consumo per leggere la disponibilità corrente; i
conteggi precedenti non descrivono più il registry compilato.

Il comando scrive il catalogo su stdout; `go run ./cmd/motion-catalog -output
catalog.json` lo salva su file.

Le famiglie, i target e gli use case si sovrappongono: i numeri non vanno sommati
come se fossero motion uniche. Per il picker attuale: **mappe = 14 motion
compatibili** (`map_image_v1`: 10 scelte specifiche, cioè 5 trattamenti × 2
renderer, più 4 motion immagine centrate condivise); **immagini = 156** motion
ammesse dal target runtime; **caption = 238** motion compatibili. Sono criteri
di compatibilità del runtime, non la somma degli inventari editoriali storici.
La famiglia dedicata `entity_card_v1` ha **10** motion; il pool generico delle
immagini di entità riusa invece il pool immagine.

Le composizioni selezionabili sono distinte dalle motion: immagini da 1 a 5,
immagini con testo da 1 a 5 (da una a N caption), `one_map` e `two_maps`. Le
dieci motion map obsolete restano registrate per la compatibilità del catalogo,
ma non sono selezionabili; le 10 mappe correnti specifiche sono abbinate per
renderer e hanno durata fissa di 150 frame. Il registry contiene una undicesima
`map_image_v1` non deprecata (`map_middle_east_oil_focus`) esclusa dal picker
georeferenziato dalla regola track `centeredMapMotion`, quindi il picker resta a
14 opzioni. Il producer automatico usa un sottoinsieme sicuro di 4 motion
centrate, ruotandole deterministicamente tra mappe. Il pool automatico delle
caption comprende 238 motion compatibili con il target `caption`; senza `caption_motion_id`, la scelta è
stabile per piano/video/layer-caption e differisce tra layer distinti. Per la
certificazione GPU consultare il report runtime separato: la registrazione non
implica che ogni ID sia stato certificato.

## Motion and preset catalog (owned by ChrononTemplate)

RenderingGen does not own the phrase/motion/preset vocabulary. ChrononTemplate
does, and it emits it as data:

```text
ChrononTemplate/catalog/motion_catalog.v1.json   ids, keyframes, selections
ChrononTemplate/tools/emit_catalog.cpp           validation + the C++-owned lists
                 │  chronontemplate_emit_catalog
                 ▼
renderinggen/internal/motion/catalog/chronontemplate_catalog.v1.json   ← embedded

ChrononTemplate/tools/short_phrases/emit_short_phrase_catalog.cpp
                 │  chronontemplate_emit_short_phrase_catalog
                 ▼
renderinggen/internal/motion/catalog/short_phrase_motion.v1.json       ← embedded
```

The embedded artifact is the only source the worker reads: `internal/motion`
registers every motion from it and fails closed at startup if it cannot be read
or validated, `internal/overlay` checks that its rendering preset registry and
the catalog's preset families agree (`overlay.ValidateCatalogParity`, called by
the worker before it reports ready), and `internal/overlaybatch` takes its
phrase→motion pairing, its phrase-overlay rows and its image-overlay matrix from
the catalog's selections instead of restating them in Go. Phrase typography has
one animated preset (`phrase_default`) plus `static_text_smoke`; text animation
is selected independently with `motion_id`.

The public motion family API keeps motion selection independent from preset
styles. The current picker/use-case counts are listed in
[Organizzazione editoriale](#organizzazione-editoriale-sezioni-classi-e-renderer);
the counts are target-compatible projections and can overlap. `phrase` combines
text animation groups; it does not define another set of motions. Brush recipes
selected on phrases emit native stroked-path layers beside the text. A narrower
editorial image inventory contains 53 IDs across `overlay_v3_image` (10),
`image_25d_clean_v1` (8), `editorial_image_v1` (14), and `image_premium_v1` (21).
That 53 is not the runtime image target count: additional safe families (such
as brush, paint, web, and light-leak treatments) make 156 image-target-compatible
IDs in the current runtime. The legacy `ImageOverlayMotionIDs()` is still only
the 18-ID compatibility pool. `web` has 14 catalog motions.

The separate `image_premium_v1` family adds 20 `image_recipe` motions without
expanding that legacy matrix. They lower to Chronon render-plan v3 primitives
(shape components, built-in effects and effect-parameter tracks, animated path
trim, gradient fills, path masks, 2.5D transforms, captions, and active/inactive
selection for legacy multi-layer compositions). This is still the regular Chronon3D renderer; there
is no premium-specific renderer. The compiled runtime animation catalog reports
the live groups: the editorial image inventory is 53 (18 legacy + 14 Editorial
Image V1 + 21 premium), while 156 motions currently admit the generic image
target. Use
`ImagePremiumV1MotionIDs()` for only the 20 premium ids, or
`ImageOverlayMotionIDs()` when a caller specifically needs the unchanged
legacy 18. The premium ids are `image_glow_depth_in`, `image_border_draw_in`,
`image_soft_yaw_glow`, `image_tilt_frame_in`, `image_frame_scale_reveal`,
`image_glow_pulse_settle`, `image_neon_trace`, `image_corner_bloom`,
`image_depth_float`, `image_parallax_frame`, `image_mask_wipe_border`,
`image_split_light_reveal`, `image_focus_breath`, `image_roll_depth_in`,
`image_card_flip_soft`, `image_border_expand`, `image_glow_ring_expand`,
`image_caption_frame_combo`, `image_spotlight_focus`, and `image_stack_focus`.

ChrononTemplate also owns the declarative presentation catalog: `metric_v1`
(20 presets), `date_v1` (20), and `entity_card_v1` (10). RenderingGen exposes
these catalog-derived choices through
`motion.Registry.PresentationMotionIDs(familyID)` directly; RenderingGen does
not maintain a presentation inventory wrapper. The canonical template IDs `metric_stat_card` and
`timeline_date_card` are registered as text-layer templates. Select one by
setting `motion_id` on the semantic overlay item; keep `preset_id` for the
independent text appearance. For example:

```json
{
  "kind": "metric_stat",
  "template_id": "metric_stat_card",
  "preset_id": "phrase_default",
  "motion_id": "metric_count_flip",
  "text": "$59.99",
  "start_ms": 0,
  "end_ms": 3000
}
```

Use `kind: "timeline_date"`, `template_id: "timeline_date_card"`, and a
`date_v1` motion such as `date_page_turn` for dates. Entity-card presets use
the existing entity contract (`kind`, `entity_id`, `duration_ms`) and
`template_id: "entity_card_person"`. All three families lower to ordinary
Chronon layer-animation tracks; camera-backed tracks set `enable_3d`
automatically. `TestPresentationMotionInventoryAndSemanticWiring` compiles all
50 IDs through the semantic compiler and checks the serialized plan. The
opt-in `TestEveryPresentationMotionExecutesOnTheStrictGPU` renders each through
Vulkan with `require_gpu_native` and native NVENC output; it requires a
Chronon3d build with Vulkan and CUDA interop enabled, a usable NVIDIA device,
and working NVENC. The strict mode fails closed rather than falling back to CPU;
`ffprobe` must decode each non-empty MP4 as H.264, 1280×720, exactly 48 frames.
When `RENDERINGGEN_GPU_CERT_OUTPUT_DIR` is set, the suite preserves those 50
renders and writes a manifest with per-file SHA-256/size and strict-lane metadata.
A registered Vulkan backend or a successful software preview alone is not GPU
certification.

The checked runtime snapshot in `renderinggen/motion-certification/latest/`
contains 108 phrase render logs and 20 software image logs; this is historical
certification evidence, **not** a count of all motions currently registered
(438 total). Do not infer GPU certification for newly registered IDs from
those older logs. Set
`RENDERINGGEN_GPU_CERT_FAMILY=phrase` or `image` to run the current selected
inventory; set `RENDERINGGEN_GPU_CERT_MOTION=<id>` to isolate a single motion.
`RENDERINGGEN_GPU_CERT_OUTPUT_DIR` preserves the compiled plan, prepared
package, native render log, and an incrementally updated
`motion-runtime-report.json` with each ID, backend, encoder, status, and render
duration. The default lane is strict Vulkan/NVENC. To certify the Vulkan render
path without the failing CUDA-to-Vulkan NVENC handoff, set
`RENDERINGGEN_GPU_CERT_GPU_MODE=auto`,
`RENDERINGGEN_GPU_CERT_HARDWARE=none`, and
`RENDERINGGEN_GPU_CERT_ENCODER_BACKEND=native`. To force full software
rendering, also set `RENDERINGGEN_GPU_CERT_BACKEND=software`.
In the worker's non-strict mode, a recoverable Vulkan device loss or
unsupported native draw retries the same plan in software and records
`chronon_software_fallback=1`. Explicit strict-native mode continues to fail
closed and preserves the error in the job log.

Catalog ownership and sync direction remain ChrononTemplate sources -> their
catalog emitters -> RenderingGen embedded artifacts. Run
`scripts/sync_motion_catalog.sh` after changing either canonical motion source;
its `--check` mode verifies both copies. Never edit generated embedded catalogs
by hand.

Text item `params` (or the item-level `style` override) may set `font_family`
to any of the eight closed runtime families: `poppins`, `inter`, `inter_bold`,
`dejavu_sans`, `playfair_display_italic`, `bricolage_grotesque`, `oswald`, and
`abril_fatface`. `glow_size`
is `[0,256]` pixels, `stroke_size` `[0,64]`, `font_size_px` `(0,512]`, and
`shadow_blur_px`, `shadow_opacity`, `shadow_offset_x_px`, and
`shadow_offset_y_px` are `[0,256]`, `[0,1]`, and `[-256,256]` pixels for each
offset. Zero disables glow/stroke; shadow opacity zero disables the shadow.
Every selected font asset must be declared and materialized by the job; the
worker's bundle preflight rejects a plan whose asset bundle omits any font the
compiled plan needs. A local
`style` value takes precedence over the same key in `params`, and both override
the preset defaults. For text in an image caption, the canonical item/child
`params.font_family` wins over the legacy `caption_font_family`; if absent, the
child legacy value is used, then the language-aware default. Run-level
`overlay_style` is projected into image/entity captions by PipelineGen before
the plan is sealed. Entity composition styles own layout, motion, and fallback
color, not typeface or explicit payload style.
Refresh both embedded artifacts after a ChrononTemplate motion catalog change:

```sh
scripts/sync_motion_catalog.sh --check   # fail if either embedded copy is stale
scripts/sync_motion_catalog.sh           # rebuild both from ChrononTemplate
```

The RenderingGen text-preset contract is now only `phrase_default` plus
`static_text_smoke`. The producer checkout must publish those same IDs in
`overlay_presets.text` before the sync check can pass; a stale producer list is
reported as catalog drift and must be corrected at its owner rather than hidden
by hand-editing the generated embedded artifact.

## Curated background library

`assets/backgrounds/` contains six normalized background videos supplied from
the Drive references used for visual overlay certification. They are
content-addressed in [`assets/backgrounds/manifest.json`](assets/backgrounds/manifest.json)
and are ready for the semantic `VIDEO_BACKGROUND` template. The checked-in
bytes are video-only: the source AAC tracks were removed so a background can
never replace or contaminate PipelineGen's master voiceover.

Before enqueueing a production job, upload the selected file bytes to the
artifact store under the manifest SHA-256 and reference that hash in
`asset_refs`. RenderingGen will materialize the file and Chronon will use it
as the full-canvas video source while important phrases, words, images and
entity cards remain independent layers.

## Renderer

Chronon3d is the **single source of truth** for the renderer. RenderingGen does
not vendor or compile any Chronon source — there is no `COPY chronon/` in any
RenderingGen Dockerfile. The worker image is built `FROM
chronon3d-runtime:<version>`, an image produced by the `Marcuss-ops/Chronon3d`
repository:

```
Marcuss-ops/Chronon3d ──CI──▶ chronon3d-runtime:<version>
                                    │
                              RenderingGen worker
                                    │
                           renderinggen-worker:<version>
```

Chronon3d's `docker-runtime.yml` workflow builds its own
`docker/chronon-runtime/Dockerfile` and publishes
`ghcr.io/marcuss-ops/chronon3d-runtime:<version>`, which installs the real CLI
at `/opt/chronon3d/bin/chronon3d_cli`. RenderingGen consumes that image via the
`CHRONON_RUNTIME` build arg — it never builds Chronon itself.

For a native host, RenderingGen needs the executable path, the matching Chronon
runtime libraries, FFmpeg, writable workspace/cache directories, and either a
running Chronon daemon socket (`mode: ipc`) or direct CLI execution
(`mode: cli`). The checked-in native profiles point to
`/usr/local/bin/chronon3d_cli`; the container profile points to
`/opt/chronon3d/bin/chronon3d_cli`. Every render also needs the job's
content-addressed assets available in the artifact store so the worker can
materialize them below the workspace before invoking Chronon.

The two projects are versioned independently:

- `RenderingGen` — worker orchestration (queue, storage, workspace)
- `Chronon3d` — render engine, consumed as a pinned runtime image

## Worker tuning: cache reuse and lane/daemon parity

Two knobs decide most of a worker's steady-state cost; both are documented where
they are set (`renderinggen/config.yaml`, `infra/native/*.yaml`) and pinned by
tests.

**Content-addressed asset reuse (`artifact_store.l1_max_bytes` /
`l2_max_bytes`).** L1 is the small-object RAM cache; L2 is the on-disk,
content-addressed mirror every materialization resolves through
(`LocalPath`: L2 first, L1 second, L3 last). Both are CAPS enforced by LRU
eviction — a value too small is not a correctness error, it re-fetches from L3;
a value large enough for the GPU working set (background videos + portrait
plates) is what makes a repeated asset set free on later jobs. Defaults: 512 MiB
/ 40 GiB; `0` = unbounded. The contract is pinned by
`TestPrepareReusesContentAddressedAssetAcrossJobsFromL2` (two jobs, same sha256
set → exactly one L3 fetch, second served from L2).

**Multiple daemons (`chronon.socket_paths`).** `gpu_lanes` above what the
daemon can execute concurrently does not add throughput; it only grows the
prep→GPU rendezvous wait. Two ways to reach the multi-session baseline: lower
`gpu_lanes`, or list one socket per daemon in `chronon.socket_paths` so the
lanes spread round-robin over N daemons (each socket lane capped at
`ceil(gpu_lanes/sockets)`; `socket_path` keeps the historical one-daemon shape).
Read the decision off `renderinggen_worker_lane_wait_seconds` against the
`render` phase histogram: while p50(lane wait) stays well below p50(render),
lanes are not the bottleneck; once it dominates, add daemons or lower
`gpu_lanes`. The worker logs this rule at startup (`lane/daemon parity: ...`).

## Go runtime memory policy (GOGC / GOMEMLIMIT)

The native units pin the Go GC policy in `infra/systemd/*.service`
(`Environment=GOGC=100 GOMEMLIMIT=...`), where the rationale lives next to the
values:

- **Workers** (`renderinggen-worker*.service`): `GOMEMLIMIT=4GiB` — a soft cap
  that turns a pathological job into GC pressure instead of an OOM kill on the
  GPU host. It covers the default 512 MiB L1 RAM cache
  (`artifact_store.l1_max_bytes`) plus per-job plan/staging bytes, the probe
  cache and upload buffers. Raise it together with `l1_max_bytes` (especially
  with `0` = unbounded).
- **Queue** (`renderinggen-queue.service`): `GOMEMLIMIT=1GiB` — the heap is
  request-shaped (submit bodies are bounded by `maxSubmitBytes`).

`GOGC=100` (Go's default) is kept explicit next to each cap so the two knobs
are always reviewed together. The contract is pinned by
`renderinggen/internal/architecture/conformance_runtime_env_test.go`: a unit
that drops either line fails `make test-unit`.

## Images

- `renderinggen-worker:<version>` — `FROM chronon3d-runtime:<version>` + Go binary + config

Always use versioned tags, never `latest`.

## Render progress observability

A running render is never an opaque `RUNNING/0%`: the worker parses the
frame-position lines Chronon prints (`[video] N/M frames`, both pipe-export
and direct-YUV/NVENC paths) and exposes live position through three channels:

```
Chronon stdout/stderr
      │  '[video] 485/1800 frames'
      ▼
chronon.ProgressTracker (per-job snapshot: frames_done/total, %, fps,
      │                 last_frame_at; strictly one GPU lane at a time)
      ├── GET :8080/progress          worker-local live JSON
      ├── queue POST /jobs/{id}/progress (throttled 10s, lease-owner checked;
      │                                 visible in GET /jobs/{id} on the queue)
      └── artifact ledger metrics      render_frames_done / render_frames_total
                                       / render_fps (after completion)
```

A job whose `frames_done` stays flat while `last_frame_at` ages is a stalled
render; the renderer's own stall watchdog aborts it. GPU memory growth alone
is never treated as progress evidence.

## Database

The central queue persists jobs, attempts, artifacts, workers, events and
metrics to PostgreSQL (the source of truth); binary artifacts stay in the
object store. Schema migrations live in `queue/migrations/` and are applied
automatically at startup when `DATABASE_URL` is set — the in-memory store
remains the default for local/dev without a database.

`infra/docker/docker-compose.yaml` runs only PostgreSQL and the object store.
The native Queue receives `DATABASE_URL` through the checked-in
`infra/systemd/renderinggen-queue-wrapper.sh`, which maps the operator-owned
`PIPELINEGEN_MEDIA_POSTGRES_DSN` from the PipelineGen environment; the secret
is not duplicated in the unit or repository.

## Build & run

```sh
# Build the native application binaries
go build -o /usr/local/bin/renderinggen-queue ./queue/cmd/queued
go build -o /usr/local/bin/renderinggen ./renderinggen/cmd/renderinggen
```

Install the checked-in units/configs during host provisioning, then enable:

```sh
sudo install -D -m 0644 infra/systemd/*.service /etc/systemd/system/
sudo install -D -m 0755 infra/systemd/renderinggen-queue-wrapper.sh /usr/local/libexec/renderinggen-queue-wrapper
sudo install -D -m 0644 infra/native/renderinggen-native.yaml /etc/renderinggen/renderinggen.yaml
sudo install -D -m 0644 infra/native/renderinggen-b.yaml /etc/renderinggen/renderinggen-b.yaml
sudo systemctl daemon-reload
sudo systemctl enable --now renderinggen-queue.service renderinggen-worker.service
```

Start infrastructure separately with `docker compose -f infra/docker/docker-compose.yaml up -d`.
Changes to Queue or the worker now require only a native rebuild and
`systemctl restart`, not a container image rebuild.

Images are built in two independent steps — first the renderer runtime from the
real Chronon3d repo, then the worker on top of it:

```sh
# 1. Build the chronon3d-runtime image from the Chronon3d repository root:
cd ../Chronon3d
docker build -f docker/chronon-runtime/Dockerfile -t chronon3d-runtime:0.1.0 .

# 2. Build the worker image (RenderingGen repo root), FROM that runtime:
cd ../RenderingGen
docker build -f infra/docker/renderinggen-worker.Dockerfile \
  --build-arg CHRONON_RUNTIME=chronon3d-runtime:0.1.0 \
  -t renderinggen-worker:0.1.0 .
```

In CI the runtime image is published by Chronon3d's `docker-runtime.yml`, so the
worker workflow just pins `CHRONON_RUNTIME` to the published tag
(`ghcr.io/marcuss-ops/chronon3d-runtime:0.1.0`).

## End-to-end (CLI)

### Ricreazione di clip con Chronon

Per generare un job dalla clip sorgente, mantenendo framerate razionale,
dimensioni, numero di frame e audio dichiarato:

```sh
infra/e2e/recreate-clip-with-chronon.sh /percorso/clip.mp4 /tmp/clip-job.json
```

Per eseguire la catena queue → worker → Chronon3d → artifact store e
scaricare/verificare il risultato:

```sh
SUBMIT=1 QUEUE_URL=http://localhost:8081 STORE_URL=http://localhost:9000 \
  infra/e2e/recreate-clip-with-chronon.sh /percorso/clip.mp4
```

Lo script non usa FFmpeg come renderer: il worker passa una clip senza overlay
al fast path direct-YUV di Chronon quando le capability lo consentono, oppure
al percorso Vulkan per composizioni. Il controllo finale richiede anche la
provenienza `chronon_version` sull’artifact e rifiuta drift di fps, frame count
o durata.

The full CLI path — queue → worker → materialize → `plan.json` → real
`chronon3d_cli` (software backend) → `result.mp4` → artifact store →
completed — is covered by two integration tests that run against the real
binary and skip when it is absent:

- `renderinggen/internal/chronon/chronon_integration_test.go` — renders the
  asset-free color smoke plan (`colorSmokePlan`) through the real CLI.
- `renderinggen/internal/processor/processor_integration_test.go` — runs the
  whole processor pipeline against the real CLI and verifies the published
  artifact.

Point them at an install prefix with `CHRONON_HOME` (default
`/opt/chronon3d`):

```sh
CHRONON_HOME=/opt/chronon3d go test ./internal/chronon ./internal/processor -run 'Integration|EndToEnd' -v
```

La suite di certificazione **runtime** (render reali con `chronon3d_cli`,
confronto pixel e decodifica) è **opt-in**: si attiva solo indicando il binario
con `CHRONON_BIN`, e si disattiva anche con `RENDERINGGEN_SKIP_GPU_E2E=1`. La
scoperta automatica di un build presente nel workspace **non** basta più: rendeva
`go test ./...` una sequenza di render reali, e su una macchina senza una GPU
utilizzabile la run restava appesa invece di fallire. Il gate veloce e
deterministico resta quello che rende esplicita la distinzione:

```sh
make test-unit   # tutti i moduli, senza la certificazione runtime (gate veloce)

# Suite reali (certificazione + golden render degli Apple style):
CHRONON_BIN=/path/to/chronon3d_cli go test ./internal/overlay/ -count=1
```

### Runtime certification in CI — manual, self-hosted GPU

The certification suite has exactly one owner: the `runtime-certification` job
in `.github/workflows/build.yaml`. It is **manual** and runs on a self-hosted
runner labelled `gpu`. It deliberately does **not** set
`RENDERINGGEN_SKIP_GPU_E2E`, so it renders for real or fails loudly — it can
never report a green run in which every certification test skipped.

Two prerequisites live on the runner side and cannot be created from this
checkout, so the job stays dormant until they exist:

| Requirement | Where it lives | Why |
|---|---|---|
| `CHRONON_BIN` | Repository **variable** (Settings → Secrets and variables → Actions → Variables) | Absolute path to a built `chronon3d_cli` on that runner. The job exits non-zero when it is unset or not executable. |
| `gpu` label | A self-hosted runner registered with the `gpu` label | Gives the job a device; GitHub-hosted runners expose none, which is why the unit jobs cannot run the suite. |

Dispatch it once both exist:

```sh
gh workflow run build -f certification=true
```

`TestCIRuntimeCertificationHasAnOwner` (`internal/architecture`) parses the
workflow and fails if the job loses any of the five properties above (overlay
target, no skip variable, `CHRONON_BIN`, manual trigger, GPU runner). If no GPU
runner is ever provisioned, delete the job **and this section together**: a
documented "local only" is honest, an owner that can never run is not.

To exercise the loop over the real services, start the stack and run the
smoke script (submits a self-contained color job, polls for completion,
downloads the artifact):

```sh
cd infra/docker && docker compose up --build -d
../e2e/run-e2e.sh
```

### Golden canary (permanent regression gate)

The canary fixture is `testdata/golden/golden-semantic-overlay-job-v1.json`
(Go twin `GoldenSemanticOverlayJobV1`) — 1280×720 @ 30fps, 5 seconds
(150 frames), with a `background.jpg` image overlay, an `IMPORTANT_PHRASE`
(`caption_card`) and an `IMPORTANT_WORD` (`active_word_pop`). Its fixtures are
deterministic (`infra/e2e/gen-golden-assets.py`) and their SHA-256 hashes are
baked into the payload; `golden_semantic_test.go` locks the payload, the
fixtures and the Go constant together, so any drift fails at unit-test time.

`testdata/golden/golden-overlay-job-v2.json` (Go twin `GoldenOverlayJobV2`) is
the **universal benchmark golden** — 1280×720 @ 30fps, 8 seconds (240 frames)
with a background video, two phrases, two words, two image overlays and a logo
— used by the benchmark/cache integration tests and pinned the same way by
`golden_v2_test.go`.

The canary (`infra/e2e/run-golden-overlay.sh`) certifies the **whole real
chain**: queue submit → worker claim → asset materialization → `plan.json` →
real `chronon3d_cli` → artifact store → queue completion → download → ffprobe
verification (1280×720, 30fps, ~5s, hash-identical download), plus:

- **PostgreSQL persistence** — `render_jobs` (state=completed,
  attempt_count=1), exactly one `render_attempt`, exactly one
  `JOB_CREATED`/`JOB_CLAIMED`/`JOB_COMPLETED` event, and the
  `render_artifacts` row whose `sha256` matches the downloaded bytes.
- **Idempotent replay without a new render** — re-submitting the byte-identical
  job resolves to the existing job (HTTP 200/409), returns the same artifact
  hash, and leaves attempts and render events unchanged.

Run it with a single command (builds the runtime image from `../Chronon3d`
on first use, boots the stack, runs the canary, tears the stack down):

```sh
make golden-e2e
```

Reset the canary's persisted state (needed after intentional golden drift)
with `make golden-e2e-reset`. The canary also runs in CI on every push to
`main` (`.github/workflows/build.yaml` → `golden-e2e`).

### Concatenated overlay scenario (frasi · date · metric · entità · luoghi · immagini)

`cmd/vidrush-scenario` defines ONE concatenated timeline that walks every
animated overlay family back to back — the shape a social/editorial cut uses:

two important phrases (`phrase_default` + catalog phrase motions), a
`TIMELINE_DATE_CARD` (date_v1) and a `METRIC_STAT_CARD` (metric_v1), exactly
two entity cards with text (a PERSON portrait with an animated
`entity_caption` and a text-only ORGANIZATION riding an `entity_card_v1`
motion), a LOCATION card plus a georeferenced local map with grounded pins,
and composite image items with 2, 3, 4 and 5 independently animated
`image_layers` (the images-x2/x3/x4/x5 matrix). Every act starts on the frame
the previous one ends, so the whole 34 s timeline is one continuous chain of
animations with no gap.

The command generates its own deterministic fixtures (world-map plate,
portraits, scene plates) into `-assets-root`, builds the plan through the
typed `renderbatch` writer and lowers it through the worker's own compiler,
then writes `semantic_plan.json`, `render_plan.json` and a `scenario.json`
descriptor (acts, windows, sampled frames, thresholds). The compile-level gate
is `go test ./cmd/vidrush-scenario/`; the render + per-act pixel probe is:

```sh
scripts/run-vidrush-scenario.sh                 # software lane
LANE=vulkan scripts/run-vidrush-scenario.sh     # GPU-native lane
```

The script never restates a window or a frame number — it reads the
descriptor — and leaves the MP4 plus one still per act under
`out/vidrush-scenario-<date>/` for human inspection.

### Overlay timeline: N overlay consecutivi in UN solo MP4 (cmd/overlay-timeline)

Il batch emette un job (e quindi un MP4) per overlay; il renderer invece non
lo richiede. `cmd/overlay-timeline` è il builder generico che fonde N overlay
in sequenza in UN solo plan multi-item — un solo render, un solo MP4 — con la
stessa regola armoniosa certificata da `vidrush-scenario`:

- **Concatenazione stretta**: l'overlay k+1 inizia sul frame in cui finisce
  l'overlay k (opzionale `gap_ms` uniforme, quantizzato a frame interi).
- **Timing naturale per item**: ogni riga dichiara la propria `duration_ms`;
  le entrate/exit non si accordano a mano. Il compilatore (`motion.go` +
  `motion_timing.go`) dimensiona l'entrata (1/3 della vita per le frasi) e la
  exit speculare (`appendExitTracks`) DENTRO la finestra di ogni item, quindi
  nessuna animazione viene forzata né trascina l'overlay precedente nel
  successivo.
- **Fail-closed in build**: ogni riga passa per lo stesso decoder e compilatore
  del worker (`renderbatch.BuildPlan`), quindi kind/preset/motion/font
  sconosciuti falliscono prima del render.

Input (JSON, ordine di riproduzione): `kind`, `template_id`, `preset_id`,
`motion_id`, `text`/`entity_id`/`entity_caption`, `asset`, `params`,
`image_layers` composite, `map` georeferenziata, `duration_ms`. Output:
`semantic_plan.json`, `render_plan.json` e `timeline.json` (finestre ms/frame
di ogni overlay, per la verifica senza rifare il render).

```sh
go run ./cmd/overlay-timeline \
  -timeline cmd/overlay-timeline/testdata/timeline-smoke.json \
  -out-dir /tmp/overlay-timeline-out -assets-root /percorso/assets
```

Per renderizzare il plan prodotto usare `chronon3d_cli render --plan
render_plan.json` (o il canale queue/batch come per gli altri job).

## Health

`GET /health` returns versioned metadata:

```json
{
  "worker": "renderinggen-77",
  "renderinggen": "0.1.0",
  "chronon": "0.9.4",
  "overlay_schema": 1,
  "backend": "software",
  "status": "ready"
}
```
