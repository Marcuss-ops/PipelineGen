# Native map render cost — 2026-10-30

## Command

```bash
cd refactored
VELOX_MAP_NATIVE_RENDERER_PATH=/usr/local/bin/maps_dynamic_map_render \
CHRONON_MAPS_CATALOG_DIR=/usr/local/share/chrononplugin/maps-catalog \
/usr/bin/time -f 'MAP_TEST_WALL=%e' \
go test ./internal/capabilities/scripts -run '^TestNativeMapRenderEndToEnd$' -count=1 -v
```

Result: **PASS**, `go test` 19.23 s, wall **22.13 s**. The test exercises one
6-second, two-pin `route_line` map through the real native renderer boundary
(`renderDynamicMapVideoOnce`), not a synthetic mock.

## Reproducible local comparison (2026-10-31)

Using the checked-out `ChrononTemplate/catalog` and the locally built
`ChrononPlugin/build-native-maps/plugins/maps/maps_dynamic_map_render`, run:

```bash
cd refactored
VELOX_MAP_NATIVE_RENDERER_PATH="$PWD/../ChrononPlugin/build-native-maps/plugins/maps/maps_dynamic_map_render" \
CHRONON_MAPS_CATALOG_DIR="$PWD/../ChrononTemplate/catalog" \
go test ./internal/capabilities/scripts -run '^TestNativeMapRenderEndToEnd$' -count=1 -v -p=1
```

One run before versus one after the bounded per-render editorial tile cache:
process wall 20.70 → 9.01 s, plate composition 13.999 → 3.633 s,
locally rasterized tiles 19,680 → 463 (19,217 memory hits). Same six-second,
180-frame route-line workload. These are single-run measurements, not a
statistical throughput certification; the existing cache is bounded at 1024
tiles and the native E2E test verifies output and telemetry, not pixel parity
of every frame. Raster test verifies repeated and wrapped tiles match pixels.

## Repeatability + parity (2026-11-01)

Five renders of the SAME plan on the same host (RTX A4000), each in a fresh
process (`-count=1`, so a fresh per-render tile cache):

| Run | Process wall | Frame pipeline | Encode wall | Plate compose | Tile hits | Tile misses |
|---|---:|---:|---:|---:|---:|---:|
| 1 | 16.88 s | 15.30 s | 15.45 s | 6,384.3 ms | 19,217 | 463 |
| 2 | 15.26 s | 14.04 s | 14.18 s | 5,788.4 ms | 19,217 | 463 |
| 3 | 11.18 s | 10.32 s | 10.43 s | 4,295.0 ms | 19,217 | 463 |
| 4 (parity A) | 9.86 s | 9.13 s | 9.27 s | 3,969.0 ms | 19,217 | 463 |
| 5 (parity B) | 9.79 s | 9.01 s | 9.16 s | 3,952.8 ms | 19,217 | 463 |
| **median** | **11.18 s** | **10.32 s** | **10.43 s** | **4,295.0 ms** | | |

The output is 429,383 bytes in every run and its SHA-256 is identical 5/5:
`22499663a5763e856f0cfc76e5f1a3998545c9666741cf0882a96c88c8b01451`.
`VELOX_MAP_E2E_PARITY=1` makes the test render the plan twice in one command and
assert byte equality, so cache parity is a check and not an inference.

Pixel parity is pinned at three levels:

- `maps_map_plate_raster_test` composes the same window with a cold raster and
  with a cache-warm raster and requires identical pixels (single-tile and a
  512×512 multi-tile window at a fractional offset), plus wrapped-tile equality.
- the E2E above requires the two full artifacts to be byte-identical.
- **cache bypassed vs cache on (2026-11-01)**: the same source rebuilt with the
  tile lookup short-circuited (`cache_hit=0 cache_miss=19680`) produced the
  byte-identical artifact `22499663…1451` in 29.80 s, against a median 12.35 s
  for the cache-on build (six runs today: 10.27, 10.71, 11.86, 12.84, 15.60,
  23.43 s). The cache is therefore pixel-exact by measurement, not by
  construction: 19,217 placements are served from 463 rasterizations without
  changing a single byte. Delta on that pair: **−17.5 s per item** (−58.6%);
  against the historical cold 18.95–22.13 s it is −6.6 to −9.8 s.

Reproduce the bypass comparison (temporary patch, then restore):

```bash
cd ChrononPlugin
cp plugins/maps/src/map_plate_raster.cpp /tmp/raster.bak
# replace `auto found = tile_cache_.find(key);` with
#         `auto found = tile_cache_.end();  // TEMP A/B: bypass tile cache`
cmake --build build-native-maps --target maps_dynamic_map_render -j4
cd ../refactored
VELOX_MAP_E2E_ARTIFACT_DIR=/tmp/map-ab-nocache \
VELOX_MAP_NATIVE_RENDERER_PATH="$PWD/../ChrononPlugin/build-native-maps/plugins/maps/maps_dynamic_map_render" \
CHRONON_MAPS_CATALOG_DIR="$PWD/../ChrononTemplate/catalog" \
go test ./internal/capabilities/scripts -run '^TestNativeMapRenderEndToEnd$' -count=1 -v -p=1
# restore ChrononPlugin/plugins/maps/src/map_plate_raster.cpp from /tmp/raster.bak and rebuild
```

`VELOX_MAP_E2E_ARTIFACT_DIR` keeps the MP4 so two builds can be compared frame by
frame instead of by digest alone.

**Deployed renderer is older than this work.** `/usr/local/bin/maps_dynamic_map_render`
is the 2026-10-28 build (`sha256 160380d9…`, 9,239,320 bytes): it predates both
the 29-10 map work and this tile cache, so live runs still pay the cold cost and
still log `cache_hit=0`. It also renders a *different picture* (ochre dashed
route with ringed pins, 504,678 bytes) from the current build (blue route,
429,383 bytes): PSNR Y 31.07 dB, SSIM 0.985, same 1920×1080/30 fps/180 frames.
That delta is the 29-10 renderer work, not the cache — the bypass A/B above
proves the cache contributes zero pixel change. Deploying is therefore an
editorial decision (the map look changes), not a performance-only swap.

**Telemetry fix (same round).** The editorial path wrote
`tile_disk_cache_hits=0` in its certified summary because that field was only
fed by the satellite pyramid, so the log line said `cache_hit=0` even though
`prefetch_tiles_memory_hits` was 19,217 and the validator's own balance check
(`required_tile_count == prefetch_tiles_fetched + prefetch_tiles_memory_hits`,
i.e. 19,680 = 463 + 19,217) already proved the cache was serving. The native
renderer now reports the raster cache hits in that field; the counters and the
balance check are unchanged.

**Variance is real.** The 9.79–16.88 s spread on one host and one workload is
larger than the difference being discussed, so use the median and never a
single run. This is still not a throughput certification of the 100-item golden
mix; it is the per-map cost of one measured class.

## Historical measured telemetry

| Field | Value |
|---|---:|
| Frames | 180 |
| Frame pipeline | 18.012 s |
| Encode wall | 18.099 s |
| Process wall | 18.946 s |
| Tile cache | 0 hit / 19,680 miss / 0 downloaded |
| Plates | 360 / 2,772,705,165 bytes |
| Plate compose | 12,888.2 ms |
| Plate prepare | 213.8 ms |
| Output | 504,678 bytes |

## Implication for the 100-item golden

The pre-cache native map cost was **~19 s per map item** (22.13 s including the
test harness) because every placement re-rasterized its tile. With the bounded
per-render tile cache the measured median is **11.18–12.35 s per item** (5- and
6-run series; compose 4.30–4.84 s, 19,217 of 19,680 placements served from 463
rasterized tiles). The golden mix requires **10 maps**: ~112–124 s sequentially,
or ~37–41 s across 3 lanes at perfect balance, before the 90 text/image items
are counted. That is closer to the 60 s window than the old ~190 s/~63 s
estimate, but still not a pass — the mixed run is the only thing that can
certify it.

This is a **gate risk**, not a certification of failure for the full mix: a
shared tile cache across maps, pre-rendered plates, or a cheaper composition
policy could change the number. The measurement to run next is the v3.1/v4
golden with maps enabled, recording the per-map cache outcome and the actual
materialized count. The map lane must not be assumed cheap from the no-map
series, and it must not be assumed solved from the single-map series above.

## Evidence

- `/tmp/map-e2e-20261030.log` (ephemeral local log; rerun with the command
  above to reproduce)
- Renderer: `/usr/local/bin/maps_dynamic_map_render`
- Catalog: `/usr/local/share/chrononplugin/maps-catalog`
