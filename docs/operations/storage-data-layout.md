# Storage & runtime data layout

**Single place of truth:** `internal/platform/config/types_storage.go`.
This doc is the operator-readable companion — it states which directory owns
which artifact, why `data/` inside the repo is gitignored, and how to move the
runtime root off the repo tree.

## Roots

| Root | Config | Env | Default | What lives there |
|---|---|---|---|---|
| Data root | `storage.data_dir` | `VELOX_DATA_DIR` | `./data` | every SQLite DB, `blobs/`, `cas/`, `stock/`, `media/`, `backups/`, `jobs/`, `observability/` |
| Temp dir | `storage.temp_dir` | `VELOX_TEMP_DIR` | `tmp` | disposable scratch; safe to delete when the process is stopped (`data/tmp/`) |
| Staging dir | `storage.staging_dir` | `PIPELINEGEN_STAGING_WORKSPACE` | `/var/lib/pipelinegen/staging` | artifact_stages pipeline staging (`{job_id}/{stage_id}`); NOT under `DataDir` |
| Media subdirs | `storage.media_dir` | `PIPELINEGEN_MEDIA_DIR` | `media` | legacy filesystem layout under `DataDir/media/{voiceovers,assets,downloads,artlist,youtube}` |
| Workspace | `storage.workspace_dir` | `VELOX_WORKSPACE_DIR` | `<DataDir>/workspace` | transient job scratch (overrides `DataDir` when set) |
| Cache | `storage.cache_dir` | `VELOX_CACHE_DIR` | `<DataDir>/cache` | derived artifacts / cache.db.sqlite |
| Export | `storage.export_dir` | `VELOX_EXPORT_DIR` | `<DataDir>/export` | one-off export bundles |

`media_cache` (`-media-cache-db`) is always an in-memory SQLite DB and never
persists to disk — the flag exists only for deterministic test startups.

## Why `data/` in the repo is gitignored

`refactored/.gitignore` has `data/` at top-level: all of the above is
artifacts, not source. The repo ships `config.example.yaml`, never real
credentials, folder IDs or DB content. A clone with `VELOX_DATA_DIR=./data`
reproduces the default layout without any manual directory shuffle.

## The external-runtime variant (VeloxEditing-runtime)

When the 10 G `stock/workspaces` or `data/media` bloat pushes the checkout
over a useful `du -sh` or cripples `git status`, point the root outside the
tree — no code change needed:

```yaml
# config.yaml
storage:
  data_dir: /srv/velox/runtime          # VELOX_DATA_DIR overrides this
```

```ini
# /etc/systemd/system/pipelinegen.service.d/storage.conf
[Service]
Environment="VELOX_DATA_DIR=/srv/velox/runtime"
Environment="VELOX_TEMP_DIR=tmp"   # still relative; becomes /srv/velox/runtime/tmp
```

Preferred external layout:

```
/srv/velox/runtime/             ← VELOX_DATA_DIR
  media/
  blobs/
  cas/
  stock/                        ← 10 G of job workspaces
  data_tmp/                     ← disposable (VELOX_TEMP_DIR if you prefer it here)
  jobs/
  cache/
  export/
  workspace/
```

Move with `rsync -a` while the service is stopped and keep the symlink
migration as a one-shot operator step — do not check a symlink into the repo.

## `data/tmp` policy

Disposable fragments (`.part`, yt-dlp Frag*, staging pulls) are pure garbage.
`rm -rf $VELOX_DATA_DIR/tmp/*` when the worker is stopped is always safe;
the materializer re-downloads what it needs. The 2.0 G cleaned on 2026-09-20
was 10 k `.part` files.

## `.venv-argos` / `.venv-whisper`

In-tree venvs kept for the operator's local `transcribe` / `argos` toolchains.
Both are `/.venv-argos/` and `/.venv-whisper/` in `.gitignore` (2.8 G + 5.5 G);
never tracked. Prefer `UV_PROJECT_ENVIRONMENT` or a host-installed wheel when
the repo travels.

## Chronon3d & RenderingGen artifacts

`Chronon3d/output/`, `test_renders/`, `build/` and `RenderingGen/*_videos/`,
`typewriter_*_videos/` are the renderer-side equivalents — also gitignored
(`Chronon3d/.gitignore`: `output/`, `test_renders/`; `RenderingGen/.gitignore`:
`renderinggen/*_videos/`, `*.mp4.timing.json`). No commit should carry an MP4
or a timing sidecar.

## Verify

```sh
git -C refactored status --porcelain | grep -E '^.*data/' && echo BAD || echo OK  # OK
du -sh "${VELOX_DATA_DIR:-./data}" ./data/tmp 2>&1 | head
```

_Recorded 2026-09-20 after the runtime-data dedup pass (1257 empty
`google_slides_session_profile_*` dirs removed, 2.0 G `data/tmp` `.part`
garbage deleted). Stock/workspaces (10 G) stays local under the gitignored
`data/` tree; the external-root move is an operator opt-in via `VELOX_DATA_DIR`,
not a repo reorg._
