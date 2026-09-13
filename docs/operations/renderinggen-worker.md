# RenderingGen worker

RenderingGen is the GPU-only overlay worker profile of the existing Go
worker binary. It does not generate scripts, voiceovers, entities, stock
searches or final videos. PipelineGen owns the `OverlayPlan`; Chronon3d owns
the pixels.

The profile registers only:

```text
overlay.prepare
overlay.render
```

`overlay.render` emits the existing `ArtifactManifest` contract. Its
metadata carries `source=chronon` and `drive_subpath=["overlay"]`; the
Sender preserves those fields through staged completion and the Drive
publisher ensures/reuses the `overlay` folder below the already-resolved
video artifact folder. The manifest artifact also carries `sha256` and
`size_bytes` at emit time, plus `drive_file_id`/`drive_link` slots that
are populated by the Drive publisher after publication.

Example service environment on Worker 77:

```text
VELOX_WORKER_ID=worker-77-renderinggen
VELOX_WORKER_PROFILE=renderer
VELOX_MASTER_URL=http://127.0.0.1:8000
CHRONON_RENDER_BIN=/opt/chronon3d/bin/chronon3d_cli
RENDERINGGEN_CACHE_ROOT=/var/cache/renderinggen
RENDERINGGEN_GPU_LOCK=/run/pipelinegen/gpu-0.lock
```

The cache is disposable and content-addressed. Job state remains in the
PipelineGen broker. `overlay.prepare` may warm assets and overlay outputs;
`overlay.render` always verifies the plan/assets and can render from a cold
cache. The renderer profile skips the creator-only `script_generate`
readiness check and fails startup when `nvidia-smi -L` or `ffmpeg` is not
available.

## Chronon transport: keep the warm daemon (`chronon.mode: ipc`)

The clip.render path (PipelineGen → RenderingGen queue → Chronon3d) must run
Chronon as a **warm daemon over the IPC socket**, not as a CLI process spawned
per render:

```yaml
# /etc/renderinggen/renderinggen.yaml
chronon:
  mode: ipc
  socket_path: /run/chronon3d/chronon.sock
  hardware_encoder: nvenc
  encode_preset: p2
  strict_native_backend: true
```

- `mode: cli` spawns and initializes a fresh `chronon3d_cli` per job. That cost
  is not free: measured on the RTX A4000 host on 2026-09-13 it is **488–570 ms
  of `chronon_job_backend_init_ms` on EVERY render** — ~2 s over a 4-clip batch,
  about **7% of the render-plane wall**, spent re-opening device, pipelines and
  the glyph atlas the daemon already holds.
- `mode: ipc` reuses the warm daemon state and removes that cost entirely, with
  no change to the sealed plan or to the rendered bytes.
- An explicit `mode:` in the config always wins. The `gpu-vulkan-native`
  profile now **defaults** to `ipc` (it IS the native hot path, and every shipped
  GPU config already declares `ipc`); the global default stays `cli` for
  software/no-profile deployments. A deployment that genuinely wants a cold
  spawn must say `mode: cli`.

Operational check — confirm the daemon is actually being used:

```bash
# the socket must exist and the worker must have opened it
ls -l /run/chronon3d/chronon.sock
journalctl -u renderinggen-worker.service | grep -i 'chronon report telemetry'
# per-render backend init must NOT reappear in the metrics
#   chronon_job_backend_init_ms ~ 0  (ipc)   vs   490-570 ms (cli)
```
