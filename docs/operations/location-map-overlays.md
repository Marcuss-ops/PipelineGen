# Location geocoding and map overlays

## Runtime path

ScriptFlow locality maps are connected in `BuildScriptGenerationRuntime`:

1. Grounded `LOCATION`/`GPE` entities are enriched with WGS84 coordinates when
   a request explicitly enables `media_plan.provider_policy.geocoding`, or
   automatically when it requests entity extraction and both map adapters are
   configured.
2. `external.geocoding_base_url` opts the runtime into the Nominatim-compatible
   HTTP adapter. The adapter sends no startup/background traffic, requires an
   identifying User-Agent and a durable positive-result cache, validates
   returned coordinates, and honors its one-request-per-second default pace.
3. `external.geo_map_plate_generator_path` connects ChrononTemplate's
   `tools/generate_map_plate.py`. It samples the Esri World Imagery tile pyramid
   into cached, georeferenced PNG levels around the extracted place; the map
   renderer then adds the camera move, pin, label, and visible attribution.
   `external.map_plate_manifest_path` remains available for deployments that
   provide certified offline plates instead.
4. RenderingGen compiles accepted semantic map plans into Chronon basemap, pin,
   label, and attribution layers. It does not fetch basemap tiles at render
   time.

A geocoding endpoint and one of the plate sources must be configured for
automatic maps. An explicitly geocoding-enabled request without a geocoder
fails closed. If a dynamic generator is configured without a geocoder, or a
configured static manifest cannot be certified, runtime construction fails
instead of silently dropping the declared capability.

## Configuration

The fields live under `external` in the normal YAML config and can be supplied
through environment variables:

| YAML | Environment | Behavior |
|---|---|---|
| `geocoding_base_url` | `VELOX_GEOCODING_BASE_URL` | Empty disables the adapter. Nominatim-compatible `/search` URL when set. |
| `geocoding_user_agent` | `VELOX_GEOCODING_USER_AGENT` | Required identifying adapter User-Agent whenever the geocoder endpoint is configured. |
| `geocoding_cache_dir` | `VELOX_GEOCODING_CACHE_DIR` | Optional durable cache override. Relative paths resolve under `storage.data_dir`; default is `<data_dir>/geocoding-cache`. |
| `map_plate_manifest_path` | `VELOX_MAP_PLATE_MANIFEST_PATH` | Optional manifest. Relative paths resolve under `storage.data_dir`. |
| `geo_map_plate_generator_path` | `VELOX_GEO_MAP_PLATE_GENERATOR_PATH` | Optional path to ChrononTemplate `tools/generate_map_plate.py`; generated plate PNGs are cached under `<data_dir>/chronontemplate-map-plates` when the static certified-raster path consumes them. With RenderingGen queue enabled, the dynamic path defers raster generation to its own tile renderer. |

Minimal example (replace the contact URL with one monitored by the operator):

```yaml
external:
  geocoding_base_url: "https://nominatim.openstreetmap.org/search"
  geocoding_user_agent: "PipelineGen/1.0 (https://your.example/contact)"
  # geocoding_cache_dir: "geocoding-cache"
  geo_map_plate_generator_path: "/path/to/ChrononTemplate/tools/generate_map_plate.py"
```

When both geocoding and a map plate source are configured, entity extraction
that includes `LOCATION`/`GPE` activates the lookup automatically. A caller can
also explicitly set `media_plan.provider_policy.geocoding: enabled`. Public Nominatim is a shared,
low-volume service; retain an identifying contact in the User-Agent and do not
use it for high-volume or concurrent bulk geocoding. The adapter's one-second
pacing is per process instance; deployments with multiple instances should use
a self-hosted/contracted compatible endpoint or otherwise coordinate requests.

## Certified plate manifest

The manifest schema is strict version 1. Each raster is an offline PNG stored
beside the manifest or in a relative subdirectory. A plate declares its ID,
relative `file`, license/provenance, visible attribution, WGS84 center, zoom,
and exact pixel dimensions. Dimensions, PNG format, coordinate ranges, path
containment, and content digest are validated at startup. Raster paths must
remain readable and stable for the runtime's asset prefetch.

Example shape (the PNG file itself must be supplied by the operator):

```json
{
  "version": 1,
  "plates": [
    {
      "id": "new-orleans-z8",
      "file": "new-orleans.png",
      "license": "operator-supplied license/provenance",
      "attribution": "required visible attribution",
      "center": { "latitude": 29.95, "longitude": -90.07 },
      "zoom": 8,
      "width": 1480,
      "height": 630
    }
  ]
}
```

Do not point the manifest at the exploratory/gallery JPEGs under
`ChrononTemplate/catalog/maps/`: those are not certified runtime plates and do
not satisfy the PNG/provenance/runtime-georeference contract by themselves.
No production map is emitted unless a certified plate covers the geocoded
point. Ordinary location text cards do not require a geocoder or plate.

## Latency and render-profile contract

The dynamic-map MP4 cache is wired by the runtime under
`<storage.data_dir>/dynamic-map-video-cache`. Set
`VELOX_MAP_VIDEO_CACHE_DIR` to override that directory and
`VELOX_MAP_VIDEO_CACHE_EPOCH` to invalidate it when a runtime dependency or
basemap source changes without a corresponding source-file change. The cache
fingerprint includes the resolved map payload, renderer Python sources,
cartography Python sources, map assets, output dimensions, and encoder profile.
Cache hits still pass the ordinary media-contract and telemetry checks.

When a RenderingGen queue is configured, PipelineGen plans generated map
coverage from geospatial descriptors only. It does not launch the preliminary
Python PNG plate generator for that path; the dynamic map renderer owns tile
fetching, raster preparation, coverage checks, and its telemetry. Offline
manifest plates retain their certified local-raster validation. This keeps
geographic planning metadata distinct from renderable raster assets.

The dynamic map video is submitted as `overlay.import`, using the existing
versioned `renderinggen.overlay-import.v1` contract. RenderingGen independently
materializes the one source MP4, verifies its byte count and SHA-256, probes the
media contract and frame count, checks duration within one frame, fully decodes
it, and records the source map `item_id` in the import provenance and artifact
metrics. New producers identify themselves as `pipelinegen-map-v2`; the worker
rejects that producer identity with missing, duplicate, or multiple item IDs,
while draining already queued `pipelinegen-map-v1` imports. It stores and
publishes those exact bytes without invoking Chronon or claiming a Chronon
receipt. This removes the second encode for the single-map producer path; it
does not remove the Python/FFmpeg map producer encode or establish a throughput
gain by itself.

This describes behavior wired in code, not an end-to-end latency or quality
certification:

- A targeted 5-second, 1920×1080@24 local render completed with 120 frames and
  zero engine fallback frames, tile fallbacks, or late tile fetches. This was a
  cache-hot local sample, not a production speedup measurement.
- The calibration harness's harmless dry-run resolved representative overlay
  and video-source plans to six future runs (three repetitions each). It did
  not render or measure GPU use.
- No map fast/quality comparison, production latency improvement, or extra GPU
  concurrency is certified by those checks.

The import path is implemented across PipelineGen and RenderingGen. Its
production rollout still requires deploying compatible versions of both
services together. GPU concurrency remains unchanged until the full
calibration is run while safely isolated from active production rendering.

Current encoder settings are implementation defaults, not user-selectable
profiles:

- ChrononTemplate: libx264 `veryfast`, CRF 18, yuv420p.
- RenderingGen: configured encoder backend/preset (e.g. native NVENC p1).

The import path skips the RenderingGen/Chronon encode for dynamic map videos;
other overlay jobs continue to use the normal Chronon render path.

## Verification

Focused verification from the repository checkout:

```bash
cd refactored
go test ./internal/app/wiring ./internal/platform/config ./internal/platform/geocoding ./internal/capabilities/maps ./internal/capabilities/scripts ./internal/capabilities/overlays

cd ../RenderingGen/renderinggen
go test ./internal/overlay -run 'TestGeoreferencedMapCompilesGroundedLayers|TestMapMotionMustResolveAndKeepBasemapCentered|TestPipelineGenOverlayPlanLowersToTimedVideoLayer|TestPipelineGenOverlayPlanIsContractValid|TestTemplateRegistryIsSingleSourceOfKind'
```

The wiring test uses an in-process HTTP test server and real certified PNG
fixtures; it does not contact public Nominatim or require production map
assets.
