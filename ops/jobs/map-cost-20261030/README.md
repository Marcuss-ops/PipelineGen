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

## Measured telemetry

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

The current native map cost is **~19 s per map item on a cold tile cache**
(22.13 s including the test harness). The golden mix requires **10 maps**:
~190 s of map work sequentially, or ~63 s across 3 lanes at perfect balance,
before the 90 text/image items are counted. That does not fit the 60 s window
needed for 100 items/min.

This is a **gate risk**, not a certification of failure for the full mix: a
warm tile/plate cache, pre-rendered map plates, or a cheaper composition
policy could change the number. The measurement to run next is the v3.1/v4
golden with maps enabled, recording the per-map cache outcome and the actual
materialized count. The map lane must not be assumed cheap from the no-map
series.

## Evidence

- `/tmp/map-e2e-20261030.log` (ephemeral local log; rerun with the command
  above to reproduce)
- Renderer: `/usr/local/bin/maps_dynamic_map_render`
- Catalog: `/usr/local/share/chrononplugin/maps-catalog`
