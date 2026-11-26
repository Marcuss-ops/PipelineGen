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

Per i job generati da script che invocano il renderer dinamico nativo,
`maps_dynamic_map_render` accetta il campo top-level `map_preset`. I 12 ID
descrittivi sono elencati in
[`ChrononPlugin/plugins/maps/map-presets-catalog.v1.json`](../ChrononPlugin/plugins/maps/map-presets-catalog.v1.json);
il preset sceglie la composizione e la basemap abbinate, e usa Italy come
`map_place` predefinito. Un esempio completo è
[`map_preset_script_example.json`](../ChrononPlugin/plugins/maps/showcases/map_preset_script_example.json).
La selezione è documentata anche in
[`showcases/README.md`](../ChrononPlugin/plugins/maps/showcases/README.md).

### ModernMap1 — placca editoriale vettoriale

Un map item sceglie la famiglia con `params.map_type`: `classic` è il default
(raster locale, pin, eventuale camera/LOD) e resta invariato; `ModernMap1` è la
mappa vettoriale che mostra **solo il territorio richiesto**, senza raster e
senza pin. Il payload dichiara `params.map_place` (nome Natural Earth, id paese
o `admin1:<nome>`); il worker risolve la feature dal catalogo locale, proietta i
ring nel canvas con `tacticalfill` e compone la placca editoriale: carta calda,
reticolo leggero, confini di contesto, territorio in rosso con alone e contorno,
nome in basso a sinistra, credito e logo opzionale dentro il territorio. Il
riempimento è triangolato (ear clipping su un anello semplificato) e ancorato al
bbox dell’intera regione, perché il fill nativo di Chronon scarta sia i path
densi sia i triangoli con bbox proprio; contorno e alone coprono le giunture. Non
c’è download, non c’è un secondo renderer: è lo stesso Chronon3D.

```json
{"kind":"map","template_id":"MAP","start_ms":0,"end_ms":8000,
 "params":{"map_type":"ModernMap1","map_place":"Italy"},
 "map":{"provider":"local","source_id":"natural-earth",
   "source_license":"public-domain","center":{"latitude":0,"longitude":0},
   "zoom":0,"width":1920,"height":1080,"attribution":"Natural Earth","pins":[]}}
```

ModernMap1 rifiuta `pins`, `routes`, `camera_move`, `lods`, `motion_id` e i
parametri di presentazione della lane raster: mescolarli dichiarerebbe due
mappe diverse sullo stesso item. Accetta al più un asset, il logo opzionale
(`params.map_logo`, PNG già dichiarato in `asset_refs`): viene centrato dentro
il territorio e scalato alla sua dimensione, e senza asset non disegna nulla e
non scarica nulla. Le regioni sono disponibili
solo per i dataset admin-1 presenti in `ChrononTemplate/catalog`
(`ne_10m_admin_1_bahia.geojson`, `ne_10m_admin_1_gujarat.geojson`); per mostrare
una provincia italiana serve aggiungere il relativo GeoJSON al catalogo. Con un
piano `ModernMap1` si compila e si renderizza con
`go run ./cmd/modernmap-demo -place Italy -out-dir /tmp/modernmap1`.

### EditorialMap — sei stili vettoriali

`cmd/editorial-map` porta in RenderingGen la lane vettoriale a sei stili
(`editorial_vox`, `paper_cutout`, `dark_radar`, `blueprint`, `connection_arc`,
`floating_island`). Il comando consuma un DTO
`chrononplugin.editorial-map-scene.v1` già proiettato da ChrononPlugin, lo
abbassa con `chronontemplate_emit_editorial_map` e, con `-render`, produce
l'MP4 tramite Chronon3D. RenderingGen non legge GeoJSON e non proietta nulla:
coordina soltanto i due binari nativi.

```bash
# connection_arc anima il trim dell'arco: prova animata della lane.
go run ./cmd/editorial-map \
  -scene ../../ChrononTemplate/tests/fixtures/editorial_map_connection_arc.scene.json \
  -out-dir ../../.tmp/renderinggen-connection-arc \
  -backend vulkan \
  -render ../../.tmp/renderinggen-connection-arc/connection_arc.mp4
```

`-backend` accetta `software` (default) o `vulkan`; il backend software è
utile come fallback ma può essere molto più lento su 240 frame 1080p.

`-emit`, `-chronon` e `-assets-root` sovrascrivono i path dei binari e del root
font; in alternativa si usano `CHRONONTEMPLATE_EMIT_EDITORIAL_MAP`,
`CHRONON_BIN` e `CHRONON_TEMPLATE_ROOT`.

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

Per una sola immagine si può chiedere uno sfondo riempitivo ricavato dalla
stessa foto: `params.image_backdrop` aggiunge dietro al soggetto una copia a
schermo intero, `cover` e sfocata. Il default è zoom `1.15x` e blur `18 px`;
il payload può scegliere `zoom` da `1x` a `10x` e `blur_px` da `6` a `48`.
Questa opzione è rifiutata sui compositi e sui gruppi.

`image_backdrop.grid` aggiunge una griglia chiara a schermo intero sopra il
backdrop e la card, come nel pacchetto editoriale `image_backdrop_grid`.
Il payload sceglie `enabled`, `style` (`lines` o `dots`), `color`
(`#RRGGBB`, default `#7FA8C8`), `spacing_px` (`4..512`, default `120`),
`opacity` (`0..1`, default `0.1`) e, per lo stile scelto, `line_width_px`
(`0.25..32`, default `1`) oppure `dot_radius_px` (`0.5..64`, default `2`).
Le due geometrie sono mutuamente esclusive: un `line_width_px` su uno stile
a punti, o un `dot_radius_px` su uno a linee, viene rifiutato.

Ombra e contorno sono selezionabili nel payload sia per una foto singola
(`item.frame`) sia per ogni membro di un composito o gruppo
(`image_layers[].frame`). `frame` espone `border` (spessore, colore e raggio),
`shadow` (colore, opacità, blur e offset), `shadows`, `stroke` e
`clip_radius_px` per il ritaglio arrotondato del bounding box. Le dimensioni e
il fit di ciascun box restano in `params.width`, `params.height`, `params.fit`
e `params.bounded`.

`shadows` è la forma multipla della stessa ombra: un array ordinato da 1 a 3
voci con gli stessi campi di `shadow` (`color` obbligatorio, poi `opacity`
`0..1`, `blur_px` `0..256`, `offset_x_px`/`offset_y_px` `±256`). Serve per
ombre stratificate — un contatto scuro più due aloni colorati, per esempio — e
arriva al renderer come `style.shadows`, dove ogni voce è un'ombra distinta.
`shadow` e `shadows` sono **mutuamente esclusivi**: dichiararne entrambi è un
errore di compilazione, non una precedenza silenziosa. I tasti piatti
`shadow_*` restano la scorciatoia per l'ombra singola e vengono ignorati quando
il frame dichiara già uno stack esplicito.

Esempio di foto singola:

```json
{
  "params": {"width": 600, "height": 800, "bounded": true, "fit": "contain",
    "image_backdrop": {"zoom": 1.15, "blur_px": 18,
      "grid": {"style": "lines", "color": "#7FA8C8", "spacing_px": 120,
        "line_width_px": 1, "opacity": 0.1}}},
  "frame": {
    "border": {"width_px": 5, "color": "#FFFFFF", "radius_px": 24},
    "shadows": [
      {"color": "#000000", "opacity": 0.6, "blur_px": 10, "offset_y_px": 14},
      {"color": "#1040C0", "opacity": 0.5, "blur_px": 14, "offset_x_px": -22,
        "offset_y_px": 8},
      {"color": "#C04010", "opacity": 0.5, "blur_px": 14, "offset_x_px": 22,
        "offset_y_px": 8}
    ],
    "clip_radius_px": 19
  }
}
```

Per un composito, spostare `frame` e le dimensioni dentro ciascun elemento di
`image_layers[]`; `image_backdrop` non è ammesso:

```json
{"image_layers": [
  {"id": "left", "asset_id": "photo-a", "start_ms": 0,
    "end_ms": 5000, "preset_id": "image_focus_in",
    "params": {"width": 420, "height": 560, "bounded": true, "fit": "contain"},
    "frame": {"shadow": {"color": "#000000", "opacity": 0.6,
      "blur_px": 14, "offset_y_px": 5}}},
  {"id": "right", "asset_id": "photo-b", "start_ms": 0,
    "end_ms": 5000, "preset_id": "image_focus_in",
    "params": {"width": 420, "height": 560, "bounded": true, "fit": "contain"},
    "frame": {"border": {"width_px": 4, "color": "#38BDF8", "radius_px": 20},
      "stroke": {"width_px": 2, "color": "#FFFFFF"}}}
]}
```

`params.effects` è opzionale e il suo stato predefinito è **nessun effetto**:
omletterlo, dichiarare `[]` oppure `[{"type":"none"}]` producono lo stesso
risultato. `none` è un valore esplicito del contratto, non un tipo di effetto:
deve essere l’unica voce dello stack e non accetta parametri; combinarlo con un
effetto reale viene rifiutato, così il payload non può dire due cose diverse
sullo stesso layer. Gli altri tipi restano quelli dell’enum `layer_effect`; un
tipo sconosciuto o un parametro fuori scala è un errore di compilazione
(fail-closed), mai uno scarto silenzioso.

**Fotografia runtime verificata:** c’è **1 renderer** (Chronon3D); il worker
RenderingGen compila il piano semantico, non è un secondo renderer. Il registry
contiene i `ItemKind`, i template e i preset definiti da `overlay/registry.go` e
`overlay/official_presets.go`, e **6 comportamenti di compilazione** (testo,
entità, immagine, video, shape, mappa). Sono livelli diversi: kind/template/
preset non sono sinonimi di “sezione”.

I **conteggi non sono ripetuti qui**: kind, template, preset, motion, famiglie e
use case sono viste compilate che invecchiano a ogni famiglia aggiunta, e un
numero scritto in prosa diventa una seconda fonte di verità che contraddice il
codice. La vista autorevole è `go run ./cmd/motion-catalog`: include l’artefatto
ChrononTemplate incorporato, i pack C++ e i cataloghi metric/date/entity, e va
eseguita al momento del consumo per leggere la disponibilità corrente.

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
dieci motion map obsolete (`map_image_*`, scadute il 2026-10-08) sono state
rimosse dal catalogo: non erano più selezionabili e la data di rimozione era
passata; le 10 mappe correnti specifiche sono abbinate per renderer e hanno
durata fissa di 150 frame. Il registry contiene una undicesima `map_image_v1`
non deprecata (`map_middle_east_oil_focus`) esclusa dal picker
georeferenziato dalla regola track `centeredMapMotion`, quindi il picker resta a
14 opzioni. Il producer automatico usa un sottoinsieme sicuro di 4 motion
centrate, ruotandole deterministicamente tra mappe. Il pool automatico delle
caption comprende 238 motion compatibili con il target `caption`; senza `caption_motion_id`, la scelta è
stabile per piano/video/layer-caption e differisce tra layer distinti. Per la
certificazione GPU consultare il report runtime separato: la registrazione non
implica che ogni ID sia stato certificato.

## Heterogeneous animation groups

`animation_groups` is the V2 owner for a mixed set of 2..8 existing items. A
group owns one shared timeline and one motion per role (`image`,
`important_phrase`, `caption`, `date`, `metric`); members must not already set
`motion_id`, `caption_motion_id` or `group_motion_id`, and an item may belong to
only one group. `sequence_v1` is the only choreography: members enter in array
order, `params.stagger_frames` (0..240) spaces consecutive entrances, and
`offset_ms` / `end_offset_ms` can override a member's window relative to the
group. Without `motion_id`, the compiler chooses one stable motion per role and
group, filtered by the role's certified pool, actual 3D/camera tracks and a
neutral final rest pose. Timing is applied to the existing item, then the
normal per-kind lowering runs unchanged; legacy plans without
`animation_groups` are untouched.

```json
"animation_groups": [{
  "id": "scene-1", "start_ms": 0, "end_ms": 4000,
  "choreography_id": "sequence_v1",
  "params": {"stagger_frames": 4},
  "members": [
    {"item_id": "headline", "role": "important_phrase"},
    {"item_id": "portrait", "role": "image"},
    {"item_id": "date", "role": "date"}
  ]
}]
```

The runtime catalog publishes the choreography and role pools under
`group_choreographies`; the opt-in canary is
`TestAnimationGroupHeterogeneousRenderCanary` (`CHRONON_BIN`).

## Date animation catalog (multi-date sequences)

`renderinggen/internal/dateanim` carries the 13 approved complete date
animations used when a plan contains more than one date. The styles are the
clean Playfair/cream set (no labels under the years); `14_mask_reveal` and
`15_archival_chronology` are intentionally absent. The catalog fixes each
style's authoring font size so selecting a style never changes typography.

One date uses `01_timeline_progress` for selection compatibility; the
multi-date materializer intentionally requires at least two dates, because a
single date belongs to the ordinary `timeline_date` overlay. Two or more dates
use a stable random choice over all 13 styles, seeded by plan, video, group and
the date values: retries keep the same cut, while different sequences can get
different styles. The odometer style needs at most one century transition; if
the chosen sequence cannot satisfy that, materialization falls back to
`01_timeline_progress` instead of failing the render. The selector is data-only;
the visual plans remain owned by ChrononTemplate.

The CLI can take the date sequence directly or extract the four-digit years
from editorial text. Text extraction preserves first-seen order, drops
duplicates and does not match video resolutions such as `1920x1080`; a text
without a year is rejected instead of falling back to the sample dates.

Selecting is not the end of the path. `-plan-out` rewrites the chosen style's
authoring plan with the real dates, and `-render` runs that plan through the
real Chronon3d renderer. Two, three and four dates are supported: unused slots
are removed and the last date holds to the end. The plans are embedded from
ChrononTemplate's approved golden plans, so the command also works without a
sibling checkout at runtime (the checkout is still needed for the fonts during
rendering, via `-assets-root` or `CHRONON_TEMPLATE_ROOT`). A text with more than
four dates is rejected instead of silently dropping one.

```sh
cd renderinggen
go run ./cmd/date-animation -list
go run ./cmd/date-animation -dates 1948,1969,1998,2026 -plan-id p -video-id v
go run ./cmd/date-animation -text "Nel 1948 nasce; nel 1969 cambia; nel 1998 arriva il lancio; nel 2026 il presente." -plan-id p -video-id v

# Concrete plan with the real dates (no sample 1948/1969/1998/2026)
go run ./cmd/date-animation -text "Nel 2007 parte; nel 2013 cresce; nel 2017 cambia; nel 2024 si chiude." \
  -plan-id garlasco -video-id ep01 -plan-out /tmp/date.plan.json

# Same selection, rendered to MP4
go run ./cmd/date-animation -text "Nel 2007 parte; nel 2013 cresce; nel 2017 cambia; nel 2024 si chiude." \
  -plan-id garlasco -video-id ep01 -plan-out /tmp/date.plan.json -render /tmp/date.mp4
```

The produced MP4 is a full-frame 1920×1080 clip. To put it into a RenderingGen
job, upload it as an asset and reference it from a full-frame `video_overlay`
item; the worker then treats it like any other materialized video asset. The
renderer path is also covered by the opt-in test
`TestRenderMaterializedDateAnimation` (`RENDERINGGEN_DATE_ANIM_E2E=1`,
`CHRONON_BIN`, `CHRONON_TEMPLATE_ROOT`).

### Tactical territory fill from a place name

`cmd/map-tactical-fill` joins the Natural Earth registry to ChrononTemplate's
native `TacticalFillPack`: it resolves a place, reads the feature's outer rings,
projects them into the canvas territory box (Web Mercator, the same window
formula as the map plates), writes the
`chronontemplate.tactical-fill-sample.v1` document and runs
`chronontemplate_emit_tactical_fill`.

```sh
cd renderinggen
go run ./cmd/map-tactical-fill -place "Israel" -out-dir /tmp/tactical/israel
go run ./cmd/map-tactical-fill -place "admin1:india:gujarat" -title "GUJARAT" -out-dir /tmp/tactical/gujarat

# Lower and render in one step
go run ./cmd/map-tactical-fill -place "country:ISR" -out-dir /tmp/tactical/israel \
  -render /tmp/tactical/israel.mp4 -assets-root ../../ChrononTemplate
```

The emitter is resolved from `-emit`, `CHRONONTEMPLATE_EMIT_TACTICAL_FILL`, or
the adjacent `ChrononTemplate/build` tree. RenderingGen owns the projection and
the polygon source; ChrononTemplate only draws rings that are already in canvas
space. Holes are not modelled: every outer ring becomes a filled territory, and
the left third of the canvas stays free for the place name. The plan is the
contract; pixel certification of the native shape render lane is tracked
separately from plan generation.

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

### Motion ownership

Each layer has exactly one owner; everything below it is a derived projection.
Do not add a parallel id list in a consumer or edit an embedded catalog by hand.

| Layer | Single owner | Rule |
| --- | --- | --- |
| Phrase/motion/preset vocabulary | `ChrononTemplate/catalog/motion_catalog.v1.json` plus the C++ recipe definitions | Authored data and C++ emitters; every id resolves from here. |
| Multi-image certification | `MultiImagePack.cpp::reusableMotions()` | One table carries id, direction and entrance. `multiImageMotionIds()`, `isMultiImageMotion()` and `beatsFor()` derive from it. |
| Camera-pan runtime semantics | `image_group_camera_pan.go::cameraPanDirections` | One table; `cameraPanSpecs` and `cameraPanMotionIDs` are derived at init. |
| Go reusable image-group membership | `motion.ReusableImageGroupMotionIDs()` | Derives the reusable `image_group_v1` set from the embedded catalog; pickers and tests must not restate it. |
| Count-specific image-group choreographies | `ChrononTemplate/catalog/motion_catalog.v1.json` (`image_group_v1`, `apply_to_all_members=false`) | 21 rows: 9 duo, 4 trio, 4 quad, 4 penta. Historical replay only; not in `reusableMotions()` and not offered by the picker. |
| Refactored planner pools | `refactored/internal/capabilities/overlays/preset_selection.go` for the curated selection; `ChrononTemplate` owns every id | Pools select from the canonical catalog; they never define a second vocabulary. Parity and retirement tests fail on unknown or removed ids. |
| Short-phrase recipes | `ShortPhraseRecipes.def` + the C++ `ShortPhrasePack` definitions | 14 recipes, 13 runtime-enabled. Emitted by `chronontemplate_emit_short_phrase_catalog`; never hand-trim the embedded artifact. |
| Embedded catalogs | `renderinggen/internal/motion/catalog/*.json` | Generated only by `scripts/sync_motion_catalog.sh`; `--check` is the parity gate. |

The 21 count-specific rows are deliberately outside the C++ reusable table: the
emitter permits them for saved-plan replay, while a reusable `image_group_*` row
missing C++ certification fails the emit. The `refactored` pools are curated
subsets — some are generated from the canonical animation registry, others are
explicit lists — and
`refactored/internal/capabilities/overlays/motion_catalog_parity_test.go::TestMotionPoolsAreCanonicalSubsets`
plus the short-phrase parity/retirement tests are the drift gate.

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
certification evidence, **not** a count of all motions currently registered.
The compiled runtime catalog is the authoritative count. Do not infer GPU
certification for newly registered IDs from
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

`assets/backgrounds/` contains normalized background videos supplied from
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
render; the renderer's own stall watchdog aborts it. GPU memory growth alone is
never treated as progress evidence.

The frame stream above is the CLI transport. The warm-daemon IPC transport runs
the render job as one blocking call, and its `GET_JOB_STATUS` reply carries
lifecycle state only (`prepared`/`running`/`completed`/`failed`), not frame
counts. A daemon render therefore logs elapsed-time heartbeats instead of
inventing a percentage.

### Stato del job nel log (job_state)

Ogni transizione scrive una riga con lo stesso prefisso stabile, sia nel log del
processo sia nel log duraturo del job (`<jobs_root>/<job_id>/worker.log`):

| `job_state=` | Quando | Dati utili |
|---|---|---|
| `claimed` | il worker ha preso il job dalla coda | tipo, tentativo, lease generation, scadenza del lease, chunk |
| `preparing` | inizio del lowering semantico + materializzazione | tipo, tentativo, lease |
| `prepared` | piano compilato e asset pronti per la GPU | layer, asset, item, durata prepare |
| `waiting_gpu` | il job è in attesa di una lane GPU libera | `lane_wait_ms` |
| `rendering` | Chronon ha iniziato a renderizzare | canvas, fps, frame totali, backend, encoder |
| `rendering progress=…` | ogni milestone del renderer (max 1 riga/10s, più la prima e l'ultima) | percentuale, ETA, frame, fps |
| `rendering state=in_progress …` | nessuna milestone da almeno 15 s, oppure trasporto daemon che non le emette | `elapsed`, ultimi frame noti (o `progress=unknown`), `note` |
| `rendered` | il renderer ha restituito il file | durata, `frames` richiesti, `frames_done`/`frames_total` osservati, fps, `progress`, output |
| `finalizing` | probe/hash/store/publish | segmenti |
| `publishing` | retry di pubblicazione di un artefatto già durevole | artifact, policy |
| `completed` | la coda ha accettato il completamento | artifact, byte, `durable` |
| `failed` | qualunque path di errore | motivo |

Per seguire un job: `grep 'job_state=' <jobs_root>/<job_id>/worker.log`. La
sequenza attesa è `claimed → preparing → prepared → waiting_gpu → rendering →
rendered → finalizing → completed` (oppure `failed` con il motivo).
L'ETA è lineare sull'fps medio e va letta come stima, non come garanzia.

La riga `rendered` riporta `frames=` (i frame che quella invocazione doveva
produrre: piano intero oppure finestra del chunk) e i campi osservati
`frames_done`/`frames_total`. Quando il renderer non ha emesso milestone,
`progress=not_reported` sostituisce la percentuale: significa "nessuna
posizione di frame riportata dal renderer", non 0%. Il job può comunque essere
`completed` con un artifact valido, e `frames=` resta la quantità prodotta.
Sul trasporto IPC del daemon è il caso normale, perché il daemon non inoltra i
frame al worker.

Se il renderer emette milestone ma si ferma per almeno 15 secondi, il worker
aggiunge una riga `job_state=rendering state=in_progress elapsed=…` con
`progress=…` dell'ultima posizione nota e `note=no_new_frames`. Il log continua
quindi a dire "sono ancora qui, da quanto" anche nei tratti silenziosi; le
righe con percentuale ed ETA compaiono solo quando arrivano nuove milestone.

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

### Runtime certification — local GPU lane

The real-engine certification suite (real `chronon3d_cli` renders, decoded and
pixel-compared) is **not** a CI job: this repository has no self-hosted GPU
runner, and a job that can never run is not an owner. The lane is owned by the
GPU host operator and runs from the checkout with the engine explicitly named:

```sh
CHRONON_BIN=/path/to/chronon3d_cli make test-certification
```

`make test-certification` runs `-tags certification` over
`renderinggen/internal/overlay/` and deliberately does **not** set
`RENDERINGGEN_SKIP_GPU_E2E`, so every test that needs the engine runs for real.
Without `CHRONON_BIN` those engine-dependent tests self-skip, so an unqualified
run proves only the compile-level suites. CI still runs the tagged suites in
`test-renderinggen` with `RENDERINGGEN_SKIP_GPU_E2E=1`; that keeps the tag from
orphaning the compile-level corpus but is not runtime certification.

If a self-hosted runner with the `gpu` label is ever provisioned, restore a
manual `runtime-certification` job with the five properties (overlay target, no
skip variable, `CHRONON_BIN`, manual trigger, GPU runner) and update
`TestRuntimeCertificationHasAnOwner` accordingly.

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
