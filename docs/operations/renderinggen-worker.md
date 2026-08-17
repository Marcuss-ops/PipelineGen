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
