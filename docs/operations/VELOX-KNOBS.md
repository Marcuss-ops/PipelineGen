<!-- GENERATED FILE — DO NOT EDIT. Run `go run ./cmd/velox-knobs -write` after editing internal/app/knobs/registry.go. -->

# VELOX_* knobs — registry

Single source: `internal/app/knobs/registry.go`. The composition root reads
these names through that package, logs every non-default value at boot, and
a test fails when a `VELOX_*` name appears in `internal/app/wiring` without
a registry entry.

| Knob | Kind | Default | Owner | Safe in production | Description |
| --- | --- | --- | --- | --- | --- |
| `VELOX_CONCURRENT_OLLAMA_CALLS` | config | 4 | `internal/platform/config/types.go` | 4 | Parallel Ollama calls allowed per host. |
| `VELOX_CONCURRENT_VIDEO_EXTRACTS` | config | 5 | `internal/platform/config/types.go` | 5 | Parallel stock-video extraction jobs. |
| `VELOX_CREDENTIALS_FILE` | config | credentials.json | `internal/platform/config/types_misc.go` | /etc/pipelinegen/credentials.json | OAuth client credentials file used by the Drive publisher. |
| `VELOX_DRIVE_STRICT_STARTUP_VALIDATION` | config | true | `internal/app/wiring/build_bundles_drive_gates.go` | true | Fail boot when Drive credentials/roots are invalid; false is the soft-mode escape hatch. |
| `VELOX_EDITORIAL_BACKGROUNDS_DIR` | env |  | `internal/app/wiring/media/editorial_assets_bootstrap.go` | unset (plates are fetched and verified from Drive) | Local directory of certified editorial plate fixtures; an optimization, not the authority. |
| `VELOX_FAKE_FINAL_JOB` | env |  | `internal/app/wiring/fake_final_job.go` | unset | Simulate the remote final-job handoff so a run measures only the local pipeline. |
| `VELOX_FINAL_AUDIO_CACHE_DIR` | env |  | `internal/app/wiring/script_generation_runtime.go` | unset (default cache root) | Relocates the per-run final-audio cache. |
| `VELOX_FINAL_JOB_ATTACH_SECONDS` | env | 3000 | `internal/app/wiring/remote_final_job.go` | 3000 | Maximum seconds a script.generate attempt stays attached to the remote render (0 disables the hand-off wait). |
| `VELOX_FINAL_JOB_CREDENTIALS_FILE` | env |  | `internal/app/wiring/remote_final_job.go` | /etc/pipelinegen/final_job_credentials.json | Credentials file carrying the final-job bearer token when VELOX_FINAL_JOB_M2M_SECRET is unset. |
| `VELOX_FINAL_JOB_M2M_SECRET` | env |  | `internal/app/wiring/remote_final_job.go` | 64-hex secret from the host env file | Per-client bearer secret for job.submit against the Master. |
| `VELOX_FINAL_JOB_MASTER_URL` | env |  | `internal/app/wiring/remote_final_job.go` | https://<master-host> | Master URL for the remote final-job handoff; unset falls back to the configured base URL. |
| `VELOX_FINAL_JOB_QUEUE_SECONDS` | env | 180 | `internal/app/wiring/remote_final_job.go` | 180 | How long a handoff may sit NOT-STARTED before it is handed back (0 disables the budget). |
| `VELOX_FINAL_JOB_RESOLVE_CONCURRENCY` | env | 1 | `internal/app/wiring/remote_final_job_resolve.go` | 4 (1..16) | Drive sources resolved in parallel while preparing a handoff. |
| `VELOX_FINAL_JOB_WORKER_ID` | env |  | `internal/app/wiring/remote_final_job.go` | unset (Master places the job) | Pins the handoff to one worker; the retired worker-51 id is rejected loudly. |
| `VELOX_MEDIA_MANIFEST_PATH` | env |  | `internal/capabilities/scripts/adapters/media_manifest_shim.go` | unset | Opt-in media_manifest fast-path used by the composition root. |
| `VELOX_POLL_MAX_BACKOFF` | config | 60s | `internal/platform/config/jobs.go` | 60s | Ceiling of the job-runner poll backoff. |
| `VELOX_PROGRESS_COALESCE_WINDOW` | config | 100ms | `internal/platform/config/jobs.go` | 100ms | Window over which job progress writes are coalesced (0 = passthrough). |
| `VELOX_SCHEDULER_INTERVAL` | config | 30s | `internal/platform/config/jobs.go` | 30s | Scheduler tick interval for background jobs. |
| `VELOX_SCRIPTS_LOCALIZED_RENDER_GLOBAL_CONCURRENCY` | config | 6 | `internal/platform/config/scripts.go` | 6 (machine render ceiling) | Machine-wide ceiling for localized render jobs. |
| `VELOX_YOUTUBE_ASYNC_ENRICHMENT` | env |  | `internal/capabilities/youtube/usecase/process_segment_step6to9.go` | unset (inline enrichment) until the outbox worker is certified | Commit YouTube clips before asynchronous metadata enrichment; boot refuses when the media outbox worker is unavailable. |

## Inventory-only names

These look like knobs but are not deployment configuration; they are kept in
the table so the inventory is complete.

| Name | Kind | Owner | What it is |
| --- | --- | --- | --- |
| `VELOX_ASSEMBLY_READY_V1` | marker | `internal/kernel/media/assembly_contract.go` | Assembly contract identifier (one audio stream, closed GOP), printed by media handoff logs; never read as configuration. |
| `VELOX_ENQUEUE` | marker | `internal/capabilities/scripts/performance/resolver.go` | Execution step name used by the render-enqueue performance phase; never read as configuration. |
| `VELOX_FEATURE_CLIP_INDEXER_ENABLED` | marker | `internal/app/wiring/build_bundles_qdrant_gates.go` | Escape-hatch name quoted by the Qdrant/ClipIndexer boot gate; the effective toggle is config clip_indexer.enabled. |
| `VELOX_FEATURE_QDRANT_ENABLED` | marker | `internal/app/wiring/build_bundles_qdrant_gates.go` | Escape-hatch name quoted by the Qdrant/ClipIndexer boot gate; the effective toggle is config qdrant.enabled. |
| `VELOX_OVERLAY_VIDEO_ONLY_V1` | marker | `internal/kernel/media/assembly_contract.go` | Legacy overlay video-only assembly contract identifier; never read as configuration. |
| `VELOX_RUST_SEMANTIC_ENGINE_PATH` | test-only | `internal/app/wiring/semantic_blend_live_test.go` | Rust semantic-engine binary used by the opt-in live blend test. |
| `VELOX_SEMANTIC_BLEND_FIXTURE` | test-only | `internal/app/wiring/semantic_blend_live_test.go` | Fixture payload for the opt-in live semantic blend test. |
| `VELOX_SEMANTIC_BLEND_LIVE` | test-only | `internal/app/wiring/semantic_blend_live_test.go` | Enables the opt-in live semantic provider blend test. |
