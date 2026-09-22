# Remote worker — connection kit and first pre-job

Everything here was verified on 2026-09-20 from **creator-77 (this host,
`YOutube`, 77.93.152.122)** against the **remote master `51.91.11.36:8000`**.

> **Status (updated 2026-09-22):** the two-stage flow is live on the remote
> master, four real jobs completed end-to-end from this host and the render of
> each one was found on Drive by content address — see §7 and §8. The remote
> worker renders **video only**: the audio leg is owned by this host and is now
> built, gated and documented — see §8.1.

| File | Purpose |
|---|---|
| `preflight.sh` | Read-only connection check: liveness, readiness, M2M key, route inventory. Never enqueues work. |
| `inventory.sh` | Read-only inventory of the media SSOT (`pipelinegen_media`) — what material a pre-job can reference. |
| `pre-job.creator-77.json` | First PREPARE payload, built from real DB assets. |
| `dolly5-pre.creator-77.json` | Dolly Parton 5-clip preview PREPARE payload (the 5 canonical clips, 10 s window each). Run it with `run-flow.sh --pre-payload`; see §8. |
| `finalize-job.creator-77.json` | First FINALIZE payload (overlay + runtime audio), built from real Drive identities. |
| `run-flow.sh` | Submitter/poller for `pre → finalize`. Submits only with `--yes`. `--pre-payload` / `--finalize-payload` select a payload other than the two defaults; `--phases` prints the canonical phase table below. |
| `sync-payload-hashes.sh` | Fills each payload's `sha256` from the media SSOT `content_sha256` (read-only on the DB; `--write` patches the JSON). |
| `mux-final-audio.sh` | Builds the canonical audio of the scene list and copy-muxes it onto the worker's video-only composite; fails closed when the audio is absent or not canonical. See §8.1. |
| `verify-delivery.sh` | Proves the render reached Drive: streams the master artifact, hashes it, looks the file up on Drive by content address — and gates it on the canonical audio identity (exit 6 when silent). |

## 1. Topology (verified)

| Role | Endpoint | Evidence |
|---|---|---|
| Remote master (target of the pre-job) | `http://51.91.11.36:8000` | `GET /health` → `commit bddf00b4`, `version 1.4.39`, `/health/ready` → `ready`, 11 checks |
| Local master (creator-77) | `http://127.0.0.1:8000` (77.93.152.122) | `pipelinegen --mode all`, commit `0eaa5ab22`, worker_id `YOutube` |
| RenderingGen queue / worker / daemon | `127.0.0.1:8081` / `:8085` / `/run/chronon3d/chronon.sock` | native services on this host |

The remote reaches us on port 8000 only; no SSH key from this host
(`pierone@51.91.11.36` → `Permission denied (publickey,password)`), so a build
deployed there must be installed from that side.

## 2. Credentials

M2M client env files live **outside the repository** (`0600`), never committed:

| File | Master URL | Client id |
|---|---|---|
| `~/creator-77-master.env` | `http://51.91.11.36:8000` | `creator-77-master-01` |
| `~/computer-editor-77-01.env` | `http://77.93.152.122:8000` | `computer-editor-77-01` |

Auth is `Authorization: Bearer $VELOX_M2M_SECRET` (scopes `jobs.submit`,
`jobs.read`). `preflight.sh` resolves `$VELOX_M2M_ENV` → `$HOME/creator-77-master.env`
→ `/home/pierone/creator-77-master.env`, in that order. Secrets are never printed.

**Different `$HOME` (agents / sandboxes).** The file lives at the absolute path
`/home/pierone/creator-77-master.env`, but the resolvers try `$HOME/...` first. An
agent whose `HOME` is e.g. `/home/pierone/freebuff_agents/<profile>` will report
the credentials as **missing even though they exist** unless it either sets
`VELOX_M2M_ENV=/home/pierone/creator-77-master.env` or can read the `0600` file.
That is an environment/ownership issue, not a missing key — check readability
before regenerating a token.

## 3. Endpoint matrix on the remote master (live, today)

| Method + path | Result | Meaning |
|---|---|---|
| `GET /health`, `GET /health/ready` | 200 | master healthy and ready |
| `POST /api/v1/jobs` | 401 without token, **400 with our key** | M2M surface mounted, key accepted (`invalid_json` on an empty body — no job created) |
| `GET /api/v1/jobs/{id}` | 404 on an unknown id | read route mounted, job lookup answered |
| `POST /api/v1/jobs/pre` | **400 / 202** | PREPARE route **mounted** (the older build answered 404) |
| `POST /api/v1/jobs/{id}/finalize` | **400 / 202** | FINALIZE route **mounted** |
| `GET /api/v1/media/assets?limit=1` | **200 / 403** | media SSOT read surface; 403 = key without `media.read` |
| `GET /api/v1/media/assets/{id}` | **200 / 404** | single-element inspection |
| `GET /api/v1/media/facets` | **200** | filter counts (sources, media types, states) |
| `POST /api/v1/admin/m2m/keys` | 401 (admin token) | admin key-minting surface present on the remote |

`preflight.sh` now exits **0** against `51.91.11.36` (master ready, key accepted,
prepare/finalize mounted). The same probe against the local master
(`VELOX_MASTER_URL=http://127.0.0.1:8000`) still reports `pre`/`finalize` absent
and an empty `m2m_clients` table: the local build is a different, older line.

### 3.0 Seeing the elements (M2M media read surface)

The Master's media SSOT (`media_assets` — youtube clips, voiceover, script,
images, stock, …) is readable from a remote computer with the SAME M2M key,
through a read-only surface that mirrors the operator console projection
(lifecycle/index state, derived asset state, content hash, Drive/local
presence, pending outbox events):

| Method + path | Scope | Returns |
|---|---|---|
| `GET /api/v1/media/assets` | `media.read` | `{items[], total, has_more, next_cursor}` |
| `GET /api/v1/media/assets/{id}` | `media.read` | full `AssetInspection` (locations, processing, outbox) |
| `GET /api/v1/media/facets` | `media.read` | counts per source / media type / lifecycle / asset state / index state |

Filters on `/assets`: `source`, `provider`, `media_type`, `lifecycle_state`,
`asset_state`, `index_state`, `search`, plus `limit` (page size) and `cursor`
(the next offset). This is the HTTP equivalent of `./inventory.sh` — use it so
a remote host does NOT need a `docker exec psql` session against the master:

```bash
source ~/creator-77-master.env
curl -sS -H "Authorization: Bearer $VELOX_M2M_SECRET" \
  "$VELOX_MASTER_URL/api/v1/media/assets?source=youtube&limit=20" | jq -r \
  '.items[] | [.id,.name,.media_type,.duration_ms // "-",.has_drive_file] | @tsv'
```

The surface is read-only by construction: no mutation route is mounted on the
`/api/v1/media` group, and `jobs.read` alone is NOT enough — the key must be
granted `media.read` (a key with only `jobs.read`/`jobs.submit` gets 403).

**Enforcement.** The M2M guard is only real when the master has
`security.enable_m2m: true` (env `VELOX_ENABLE_M2M=true`). With it `false` the
middleware passes through in admin context and the `/api/v1/*` surface is open
— that default is for dev/test fixtures only and MUST be `true` on any host
reachable from outside the loopback (`config.yaml`, `config.production.example.yaml`
and `config.example.yaml` now ship `enable_m2m: true`). `preflight.sh` probes
`GET /api/v1/media/assets` and reports `media.read` accordingly.

`GET /api/v1/jobs/{id}` remains the polling endpoint for job status; polling
semantics are unchanged (see §7 for the PREPARE-stays-PENDING caveat).

### 3.2 The phases this lane drives (canonical names)

The two stages carry seven phases. The NAMES are the canonical vocabulary owned
by this repository (`internal/kernel/observability/registry.go` for the
execution phases, `internal/kernel/job/stage_progress.go` for the workflow
stages) — the kit does not invent phase names:

| phase | surface | carried by |
|---|---|---|
| `script` | PREPARE | `script_text` + `scenes[].text` |
| `clips` | PREPARE | `scenes[].clip{asset_id,drive_file_id,sha256,size_bytes}` |
| `stock` | PREPARE | `scenes[].stock{asset_id,drive_file_id,sha256,duration_ms}` |
| `overlay` | FINALIZE | `overlays[]{start_frame,end_frame,frame_count,mode,z_index}` |
| `audio_compile` | FINALIZE | `runtime_assets[]{kind,role,url,sha256}` (music/SFX) |
| `render` | FINALIZE | the worker's certified artifact — sha-addressed, polled to terminal |
| `publish` | FINALIZE | `delivery_plan[].destination_id` (`drive-production`) |

`run-flow.sh --phases` prints this table, and `run-flow.sh` without `--yes`
prints it as part of the dry plan. Two consequences worth keeping in mind:

- **PREPARE is not a phase that completes.** A pre-only job stays `PENDING` with
  `started_at: null` forever (verified 2026-09-20), so `script`/`clips`/`stock`
  are bound by the PREPARE call but never observed as completed by polling.
  `clips`/`stock` are bound, not rendered: the render is the FINALIZE stage.
- **`audio_compile` is unproven on this lane.** The payload supplies
  `runtime_assets` (bgm1 + whop1) and earlier runs on this master reported
  `AAC stereo`, but the 5-clip preview run delivered an MP4 with **no audio
  stream** (see §8). Treat the audio leg as unproven until the remote owner
  explains the difference, and do not report it as certified.
- **Cover/thumbnail is NOT a phase of this lane.** It is produced by the owner
  of the cover lane and must never be added here; the daemon-side contract
  pins its absence (`TestRegistry_HasNoCoverPhase` in the observability
  registry test suite).

### 3.3 Generic jobs from another PC

The M2M endpoint is generic: the same `POST /api/v1/jobs` accepts every job
registered by the Master (clip, script, stock, voiceover, and future types).
Use the canonical envelope:

```json
{
  "type": "script.generate",
  "project": "project-01",
  "idempotency_key": "project-01-run-001",
  "payload": { }
}
```

The remote `velox` CLI supports the same flow without admin credentials:

```bash
export VELOX_M2M=true
export VELOX_M2M_ENV="$HOME/computer-editor-77-01.env"
set -a; . "$VELOX_M2M_ENV"; set +a
velox submit jobs --m2m --key project-01 --payload enqueue.json --json
velox poll <job_id> --m2m --interval 3s --timeout 30m
```

`--m2m` selects `POST /api/v1/jobs` and `GET /api/v1/jobs/{id}`; without it,
`velox` keeps the admin endpoint aliases for endpoint-specific flows.The M2M secret needs both `jobs.submit` and `jobs.read` scopes. Before submitting, a remote PC can discover exactly which consumers are active on this Master:

```bash
velox types --m2m
```

Only types returned by this catalog are accepted by M2M enqueue. This prevents
policy-only or not-yet-wired types from becoming permanently queued jobs.

### 3.1 `delivery_plan.destination_id` (discovered the hard way)

An explicit delivery plan is **mandatory**:

- omitted → `422 {"error":"DELIVERY_TARGET_REQUIRED"}`;
- unknown id → `422 {"error":"invalid_payload","details":[{"issue":"destination_not_found","path":"delivery_plan.0.destination_id"}]}`.

Probed against the live master (`drive-production`, `drive-prod`, `production`,
`drive`, `velox-drive`, `clips`, `stock`, `youtube`, `editorial`, `default`,
`local`): **only `drive-production` is registered** — the payloads here use it.
An accepted pre-job answers:

```json
{"phase":"PREPARE","dispatch_status":"waiting_runtime_assets","job_id":"job_…","ok":true}
```

## 4. What material we actually have

`./inventory.sh` (media SSOT `pipelinegen_media`, 2 314 rows):

| source | n | with Drive id | hours | notes |
|---|---|---|---|---|
| youtube | 1 343 | 1 340 | 3.58 | the real scene material (clip extracts) |
| voiceover | 717 | 717 | 3.23 | TTS masters |
| script | 168 | 168 | — | text tracks |
| internet_images | 103 | 103 | — | overlay images |
| script.localized_render | 78 | 78 | 1.04 | multilingual renders |
| clip.render | 21 | 21 | 0.28 | rendered clips |
| sound_effect | 9 | 9 | — | SFX |
| bgm | 6 | 6 | 0.40 | background music |
| editorial | 2 | 2 | — | curated plates |
| **stock** | **2** | **2** | **0.00** | `clip_001.mp4`, `clip_002.mp4` (5 s each) |

The stock collection itself is effectively empty (two 5-second clips —
`clip_001.mp4` 1 336 115 B, `clip_002.mp4` 4 125 211 B — plus a `metadata.json`
blob); the usable scene material is the 1 340 Drive-backed youtube clips.

Hashes: `content_sha256` is populated for **100% of the rows** (1 344/1 344
youtube, 168/168 script, 103/103 images, …), and it matches the Drive
`sha256Checksum` of the same file — verified on `clip_001.mp4`, the Dolly
Parton clip and the overlay image. `binary_sha256` is the *compatibility
projection* of that column (see `internal/capabilities/mediaregistry/hashes.go`)
and is empty on **every** row of the PostgreSQL plane: no Postgres migration
projects it, unlike the SQLite migration
`152_add_canonical_metadata_columns.sql`. Readers are unaffected because every
read path goes through `COALESCE(binary_sha256, content_sha256, legacy_file_md5)`,
but the invariant "binary_sha256 == content_sha256" does not hold at rest on the
Postgres plane — that is an owner task, not a payload problem.

`sync-payload-hashes.sh` therefore fills the payloads from `content_sha256`
(never from a download). Before it existed the payloads carried `sha256: ""`
for every asset, which silently removed any integrity check on the worker side.

## 5. Provenance of the assets in the payloads

| Payload field | Asset | Drive file id | Hash |
|---|---|---|---|
| `scenes[0].clip` / `.stock` | `planner:6432396435356433:0` (`stock/clip_001.mp4`, 5 s, 1 336 115 B) | `18MzFZcGHPqOnSwtqrWG5dIF9X8jKIvG-` | `ed23dd9ac93951253a0bacc49445dbc1972d878c1f76c4e22b26175f338a9cd9` (content_hash) |
| `scenes[1].clip` | `yt_vLRjqTIiMjc_270_355_v1` (Dolly Parton, 85 s) | `1kC7WB7S-HlIdOCiTEPAeB-Uv7gqzY-JN` | not stored |
| `overlays[0]` | `internet_images` image row | `1doHJp98YAGsbOe3q1ZXRdI-6UM1O4axh` | not stored |
| `runtime_assets[0]` | `bgm1` (`type beat`) from the editorial catalog | `1X4-wfIwrR51eDxIegciuBAJzKSdP3gcX` | catalog has no hash |
| `runtime_assets[1]` | `whop1` (transition SFX) from the editorial catalog | `1Fgr2jWQC1G6EHo-jhBAwjGtdcZo1PfaX` | catalog has no hash |

Caveats:

- a "trailing `|`" on `internet_images` Drive ids was reported earlier in this
  kit and is **wrong**: every `drive_file_id` in the media SSOT is a clean
  33-char Drive id (`drive_file_id LIKE '%|%'` matches 0 rows). The pipe was an
  artifact of a table dump, not of the data — do not "fix" ids on that basis;
- overlay windows are frame-native: `frame_count = end_frame - start_frame`
  (120 frames = 5 s at 24 fps);
- `bgm1`/`whop1` identities come from
  `internal/capabilities/mediaregistry/editorial_catalog.go` (the payload's
  `ops/jobs/bgm_catalog.json` / `sfx_catalog.json` are projections of it).

## 6. Running it

```bash
cd refactored/ops/jobs/remote

./inventory.sh                       # what we have
./preflight.sh                       # connection verdict (exit 4 = surface absent)
./run-flow.sh                        # preflight + the exact requests that would be sent
./run-flow.sh --phases               # the canonical phase table of this lane

./sync-payload-hashes.sh            # payload hashes vs the media SSOT (exit 1 if stale)
./sync-payload-hashes.sh --write    # pin them into the JSON files
./verify-delivery.sh JOB             # prove the render reached Drive

./run-flow.sh --yes --pre            # submit only the pre-job (stays PENDING by design)
./run-flow.sh --yes --finalize JOB   # finalize an existing job
./run-flow.sh --yes --all            # pre → poll → finalize → poll
./run-flow.sh --yes --all --surface=enqueue   # force the legacy submit surface

./run-flow.sh --yes --all --pre-payload ./dolly5-pre.creator-77.json   # Dolly 5-clip preview lane (see §8)

# the worker returns video only: close the audio leg on the artifact it produced
./mux-final-audio.sh --video /tmp/dolly5_final.mp4 --pre-payload ./dolly5-pre.creator-77.json \
  --clips-dir <repo>/data/tmp/localization --out /tmp/dolly5_final_av.mp4   # see §8.1
```

Without `--yes` nothing is submitted: a real send creates a job on the target
master. `--run-id` overrides the idempotency-key prefix (default: UTC
timestamp), `--timeout`/`--interval` control polling.

The `velox` operator CLI is currently out of sync with this master surface
(`velox search … --json` → `job not found: status=404`); the scripts here talk
HTTP directly.

## 7. Verified run (2026-09-20)

First real two-stage job driven from this host with `pre-job.creator-77.json`
(2 scenes: 5 s stock `clip_001` + Dolly Parton youtube clip), finalized with
`finalize-job.creator-77.json` (frame-native overlay 120→240, BGM + SFX):

| Field | Value |
|---|---|
| job | `job_aa976f0b9607ad1d` |
| task / attempt | `ab20c971-8464-4166-bc74-49b60fd50e02` / `86ad655c-9016-48c9-b2a3-6303f95788da` |
| worker / lease | `velox-worker-13197` / `l-velox-worker-13197-943a4eb2` |
| phases | `PREPARE` 202 `waiting_runtime_assets` → `FINALIZE` 202 `prefetch_refresh_queued` |
| outcome | `SUCCEEDED` in ~64 s (started `13:06:39Z`, completed `13:07:43Z`) |
| artifact | 47 839 864 B, `sha`-addressed download on the master |
| `ffprobe` | h264 1920×1080 24 fps 95.0 s + AAC stereo 95.0 s |

Second run, this time entirely through the kit (`./run-flow.sh --yes --all
--timeout 240`), 2026-09-20 13:10 UTC:

| Field | Value |
|---|---|
| job | `job_10af228ce8bbd21d` |
| task | `e3ee9c8b-c494-4095-b6fa-21baaae4afc4` |
| worker | `host_57_129_132_133` (a *different* worker than the first run) |
| phases | `PREPARE` 202 → pre status `PENDING` → `FINALIZE` 202 → `SUCCEEDED` in ~50 s |
| artifact | 47 880 042 B, h264 1920×1080 24 fps 95.0 s + AAC stereo (ffprobe verified) |

So the remote worker really renders: it claimed the job, executed both stages on
the same `job_id`/`worker_id`, and returned a playable H.264/AAC artifact — and
the pool serves more than one worker.

Third run, after `sync-payload-hashes.sh --write` filled every `sha256` from the
SSOT (`job_b11cccb276ca509f`, 2026-09-20 13:23 UTC): `SUCCEEDED` in ~58 s, same
worker path — the worker accepts payloads whose hashes are pinned.

Delivery is provable, not assumed: `verify-delivery.sh` streams the master's
artifact, computes its SHA-256 and finds it on Drive as
`<sha256>.f4v` (MIME `video/mp4`) in a per-job folder that holds that single
file. Verified on all three jobs:

| job | artifact | Drive file |
|---|---|---|
| `job_aa976f0b9607ad1d` | 47 839 864 B | `afba3475….f4v` in `1TV3M5zR56XOwlrHxf083F1Ml13qZW3XQ` |
| `job_10af228ce8bbd21d` | 47 880 042 B | `63b67b1e….f4v` in `1yTtcTGJ48rkb9t4gX-PjVOWiQF8FigA2` |
| `job_b11cccb276ca509f` | 47 727 418 B | `4197f05f….f4v` in `1b7ZukBiM2zwBgU3jZly4FVFY2XdneO-z` |

The hash in the Drive filename equals the SHA-256 of the artifact downloaded
from the master, byte for byte.

Behaviour worth knowing: a **pre-only** job (no finalize) stays `PENDING` with
`started_at: null` forever — verified over 56 s on `job_ef304abb6214f08c` — so
the PREPARE phase must never be polled to a terminal state. `run-flow.sh`
confirms the job is visible and goes straight to finalize; `GET /api/v1/jobs/{id}`
does not echo `dispatch_status` (only the `/pre` response carries
`waiting_runtime_assets`). There is also **no cancel surface** for an M2M
client: `DELETE /api/v1/jobs/{id}` and `POST /api/v1/jobs/{id}/cancel` both
answer 404, so a submitted-and-never-finalized job can only be reclaimed by a
master-side TTL/GC. Confirm that TTL exists before generating pre-jobs in bulk.

Version note: the master reports `1.4.39` (`bddf00b4`) while the worker release
line quoted by the platform team is `1.4.40` — confirm both before attributing a
future failure to the payload rather than to a version skew.

## 8. Verified run (2026-09-22) — Dolly Parton 5-clip preview

The same two-stage flow, driven with the tracked payload
`dolly5-pre.creator-77.json` (five canonical Dolly Parton preview clips, a 10 s
window each). It exercises the full handoff: submit here → PREPARE (no worker
claim) → FINALIZE (the remote worker's asset prefetch) → render → Drive
publication.

| Field | Value |
|---|---|
| job | `job_80ef4d2a38b0e13a` |
| phases | `PREPARE` 202 `waiting_runtime_assets` → pre `PENDING` → `FINALIZE` 202 **`prefetch_refresh_queued`** (`future_asset_plan=refresh`) |
| worker / lease | `host_57_131_20_173` (remote) / `l-host_57_131_20_173-09d627c7` |
| task / attempt | `725fac58-7373-4ed4-8d3a-37bdd7fbe511` / `406f7e64-b49d-4260-9c2a-f118eb2c61b9` |
| outcome | `SUCCEEDED` in ~65 s (started `11:21:03Z`, completed `11:22:08Z`) |
| artifact | 24 158 582 B, h264 1920×1080, **50.000 s** = 5 × 10 s (scenes in order) |
| sha256 | `7022d0a891eb7b0da5544de62eab8e17de3298659714354609d3a03bae1f9ba1` |
| Drive | `verify-delivery.sh` OK: `1vbqiLn7jn-S4LhEl5N-S0Jfjp2CpRKMR` under `1YiyiQSxd3IqVY3h-TABULDWMbw3t3pdK` (content-addressed `<sha256>.f4v`) |

### 8.1 The audio leg is owned by THIS host, and it is now closed (2026-09-22)

`scene.composite.v1` renders **video only**: the artifact above has a single
video stream and the job still reports `SUCCEEDED`. That is not a payload bug,
and it is not something the remote side can be handed a fix for from here — the
audio of this lane belongs to the host that runs PipelineGen, and this
repository owns both halves of it:

| Piece | Owner (this repository) | Why it settles the question |
|---|---|---|
| canonical audio identity | `internal/kernel/media/assembly_contract.go` → `DefaultAssemblyMediaContractV2()` (`VELOX_ASSEMBLY_READY_V2`): 1 video + 1 audio, `aac/LC/48000/2ch/stereo/192k`, `start_pts 0` | `audio.DefaultAudioProfile()` is derived from that same contract, so there is one SSOT, not two |
| audio render | `RenderAudioPlan` → `FinalAudioAsset` (`internal/platform/media/rustexec/video_processor.go`) | compiles the canonical final audio, copy-eligible and a single final mix |
| mux | `MuxFinalAudioCopy` (same file) | `-c:v copy -c:a copy`, deliberately **no encode fallback** |
| the clips keep their own audio | `internal/capabilities/scripts/audio_timeline.go` (`includeClipAudio`), `internal/capabilities/clips/reprocess.go` (`KeepAudio: true`) | the clip intermediates here are `aac 48000 2ch`, and the extraction step fails closed if the audio is missing |

So a finished artifact of this lane needs the audio produced **here**. That is
now a tracked, gated step instead of an operator assumption:
`ops/jobs/remote/mux-final-audio.sh`.

**It was run on the artifact of the run above** (`job_80ef4d2a38b0e13a`):

| Step | Result |
|---|---|
| remote composite (worker) | 24 158 582 B, h264 1920×1080 24 fps, **50.000 s, 0 audio streams** |
| canonical audio built here (5 scenes × the first 10 s, in scene order) | `aac/LC/48000/2ch/stereo` 192k, **50.000000 s**, 1 231 853 B, sha256 `32ec3dd7450864f87bc121c91b68601ae62836011a6018e342d8c48d7d1fd1fa` |
| copy mux | 25 400 777 B, sha256 `fabbc04984d824b94b402b263fb5feb72205f846095cec45b19edfbf84dc984f` |
| video untouched | video-packet md5 `24709ba8aa7420f19e01d5034876c2fe` **identical before and after the mux** → no re-encode |
| contract gate | **PASS** — 1 video + 1 audio `aac/LC/48000/2ch/stereo`, `start_pts 0`, audio covering the picture |

The window is proven, not assumed: scene k of the composite is the **first
`duration_seconds` of clip k, in payload order**. Measured by matching the scene
*cuts* of the composite against the local clips (cut positions survive a
re-encode, so this is a real check and not a guess):

| composite cut | clip cut | offset |
|---|---|---|
| 10.58 | `yt_tnoMGevqWAM_1841_1903_v1` 0.58 | +10 s (scene 1) |
| 22.04 / 24.17 / 25.21 / 25.42 / 27.25 / 28.50 | `yt_pfaIAdqvlig_457_509_v1` 2.04 / 4.17 / 5.21 / 5.42 / 7.25 / 8.50 | +20 s (scene 2) |
| 34.25 / 36.12 | `yt_pfaIAdqvlig_929_980_v1` 4.25 / 6.12 | +30 s (scene 3) |
| 40.62 | `yt_vLRjqTIiMjc_211_256_v1` 0.62 | +40 s (scene 4) |

```bash
# reproduce: worker's video-only composite → contract-compliant artifact with audio
ops/jobs/remote/mux-final-audio.sh \
  --video /tmp/dolly5_final.mp4 \
  --pre-payload ops/jobs/remote/dolly5-pre.creator-77.json \
  --clips-dir data/tmp/localization \
  --out /tmp/dolly5_final_av.mp4

# gate any artifact (e.g. one pulled from the master) without muxing
ops/jobs/remote/mux-final-audio.sh --verify /tmp/dolly5_final_av.mp4
```

The gate is wired into the delivery proof, so a silent artifact can no longer
verify as delivered: `verify-delivery.sh` streams the artifact, runs
`mux-final-audio.sh --verify` on it and exits **6** when the audio is absent or
not canonical (`--allow-silent` for a legacy artifact only). Both paths were
tested against a loopback stub master: `SUCCEEDED` + silent artifact → **exit
6**; audio-bearing artifact → gate **PASS** and the script proceeds to the Drive
lookup.

Still owned by the worker, and **not** fixable by a copy-mux (never re-encode to
silence them — the assembler rule is copy-only):

- the video identity deviates from V2 on `level` (`40`, want `41`) and the video
  time base (`1/12288`, want `1/90000`). Measured 2026-09-22: this is **not the
  worker's**, it is the renderer's — the render produced by THIS host's
  RenderingGen/Chronon carries the same `level=40` / `tb=1/12288`, so it is a
  contract-vs-encoder question for the whole chain. Everything else (h264,
  1920×1080, `24/1`, `yuv420p`, `start_pts 0`) matches on both sides;
- the composite itself is remote code: this master runs `v1.4.39` (`bddf00b4`),
  a build that does not exist in this repository (`git cat-file -t bddf00b4` →
  *not a valid object name*), so its silent-video behaviour can only be changed
  from that side. What is owned here — the audio and the contract — is closed
  and gated;
- `POST /api/v1/jobs` on this master is a **scene-composite submitter**
  (`idempotency_key`, `job_type`, `script_text`, `scenes[]`, `output`,
  `copy_only`, `delivery_plan[]`) — it is not the generic job broker: `type` and
  `payload` are rejected as unknown fields, and `POST /api/script/generate`
  answers 404. A `script.generate` envelope (the Dolly Parton **Best Moments**
  50-clip manifest) therefore cannot be submitted to this master at all; Dolly
  content enters it only as `scenes[]`, which is exactly what
  `dolly5-pre.creator-77.json` does.

### 8.2 Prefetch on the remote: measured, and what to improve there (2026-09-22)

Two identical runs of the tracked Dolly payload, ~40 s apart, each with
`PREPARE 202 waiting_runtime_assets` → `FINALIZE 202 prefetch_refresh_queued`
(`future_asset_plan=refresh`):

| run | job | worker | started → completed | wall | artifact |
|---|---|---|---|---|---|
| A | `job_7cc7a9704f23fbcd` | `velox-worker-13197` | 12:22:14 → 12:23:05.536 | **51.5 s** | 24 170 008 B |
| B | `job_37cec1defab4d108` | `host_57_129_132_133` | 12:23:46 → 12:24:21.671 | **35.7 s** | 24 232 290 B |

**Verdict: the prefetch is accepted and the repeat is faster, but it is not
proven.** The request is real, run B is 31 % faster, and the master's own
counters moved by +28 hits / +8 misses across it
(`velox_cache_hits_total` 236→264, `velox_cache_misses_total` 90→98). But the two
runs landed on **different workers**, so worker variance is not excluded, and
nothing attributes a hit to a prefetch: `/metrics` exposes only
`velox_worker_prefetch_jobs_active` (a gauge, `0` on both scrapes). Run B's
artifact is again 50.000 s with **0 audio streams**.

Improvements for that master, in the order I would fix them:

1. **Audio, or fail closed.** FINALIZE carries `runtime_assets` (bgm1 + whop1,
   `kind: audio`) and `overlays[].audio_mode: preserve_final_audio`, the artifact
   still has no audio stream, and the job reports `SUCCEEDED`. Either copy-mux a
   certified `final_audio.m4a` (`FINAL_AUDIO_COPY` — the strategy this repository
   already emits) or reject the payload. Publishing a silent artifact as success
   is the defect.
2. **Prefetch observability**: `prefetch_jobs_total`, `prefetch_bytes_total`,
   `prefetch_failures_total`, `prefetch_duration_seconds`, and hits/misses **per
   prefetch**. Without them "does the prefetch work?" cannot be answered, which
   is why this table reports a delta and not an attribution.
3. **`/metrics` is unauthenticated — root cause found, and fixed in this repo.**
   `GET http://51.91.11.36:8000/metrics` → 200, 105 KB, **718 `velox_` series**,
   no token: per-worker CPU/disk/network, queue depth, quarantine counters and
   cost models. The cause is in the repository, not in how the remote is
   deployed: the posture was keyed on **gin mode** (`routes_metrics.go` — release
   ⇒ token required, anything else ⇒ per-request loopback check), which leaves
   two doors open. A dev-mode binary on a `0.0.0.0` bind serves the surface, and
   so does any deployment behind a local reverse proxy: the peer address is then
   the proxy's loopback, so `RemoteAddr.IsLoopback()` reads "local" for a request
   that arrived from the internet.

   Fixed 2026-09-22 by keying on the **bind address** instead — the one input a
   proxy cannot forge:

   | change | where |
   |---|---|
   | `IsLoopbackBindHost(host)`: single definition of "only this machine can reach it" (`""` and `"::"` are **not** loopback — `net.Listen` reads both as all interfaces) | `internal/platform/config/bind_host.go` |
   | `RouterConfig.ServerLoopbackOnly`, derived by the composition root from that predicate | `internal/platform/httpserver/routes.go`, `server.go` |
   | posture: token set ⇒ Bearer required (constant-time compare); no token **and** not loopback-only ⇒ route **not mounted** (fail-closed); no token + loopback + non-release ⇒ per-request loopback check (local dev preserved) | `internal/platform/httpserver/routes_metrics.go` |

   Tests: `TestMetricsRouteNonLoopbackBindIsFailClosed` (a public bind with no
   token is 404 in **every** mode, with or without a spoofed `X-Forwarded-For`),
   `TestMetricsRouteIgnoresSpoofedForwardedFor`, and a `IsLoopbackBindHost` table
   test. The two older dev-mode tests now declare `ServerLoopbackOnly: true`,
   which is the bind their branch actually requires.

   **This is a deploy item for 51**: it answers 200 with no auth, so it runs a
   build without the guard. The local master (same code, token set) answers 401
   without a bearer and 200 with one — verified.

### 8.3 The unauthenticated surface of the remote master (measured 2026-09-22)

136 paths probed with no credentials at all: `GET` on every path in
`architecture/routes.yaml` plus a curated ops list, and `POST` only on
read-shaped routes (never on a mutating name — no `cleanup`, `purge`, `delete`,
`retry`, `cancel`, `reprocess`, … was ever called).

| class | count | detail |
|---|---|---|
| **OPEN (no auth)** | **4** | `/metrics` (200, 718 `velox_` series), `/health`, `/api/health`, `/ready` |
| guarded (401) | every route that touches work or data | `POST /api/v1/jobs`, `POST /api/v1/jobs/pre`, `POST /api/v1/jobs/{id}/finalize`, `GET /api/v1/jobs/{id}`, `GET /api/internal/artifacts/{id}/download` (with a **real** artifact id), `GET /api/v1/admin/m2m/keys` |
| absent (404/405) | 130 | no `/debug/pprof`, `/docs`, `/openapi.json`, `/static`, `/files`, `/internal/v1/*`, `/api/v1/media/*` on this build |
| 5xx | 0 | — |

So this master is **not** broadly exposed: jobs, artifacts and admin are behind
the M2M guard with a uniform 124-byte 401 body (no existence leak — a bogus and a
real artifact id answer identically). What leaks is the metric surface and the
three health endpoints, which disclose the build fingerprint (`commit
6fa6cc79…`, `version main`, `grpc.port 9000`) and capability state including
`opsalerts: DISABLED`. Trim `/health` for anonymous callers, deploy the
bind-address fix for `/metrics`, and turn `opsalerts` on.
4. **Fix the bogus gauge**: `velox_cache_hit_ratio{worker_class="mixed"} 777777`
   while its own `# HELP` says "Cache hit ratio (0-1)"; and every per-worker
   `velox_cache_entries` / `cache_size_bytes` reads 0 while the global hit/miss
   counters move — the per-worker cache telemetry is not wired to the same cache.
5. **Expose the phase in the read model**: `GET /api/v1/jobs/{id}` does not echo
   `dispatch_status` / `future_asset_plan`, so the prefetch is visible only in
   the `/pre` and `/finalize` responses. (`started_at` / `completed_at` /
   `worker_id` are exposed — that is what made this table possible.)
6. **No cancel, no TTL**: `DELETE /api/v1/jobs/{id}` and
   `POST /api/v1/jobs/{id}/cancel` answer 404, and a pre-only job stays
   `PENDING` for ever (§7), so an abandoned pre-job depends on a master-side GC
   whose existence is still unconfirmed.
7. **Stable worker identity**: four ids in one scrape under two naming schemes
   (`velox-worker-13197`, `velox-worker-523925eb`, `host_57_129_132_133`,
   `host_57_131_20_173`). Per-worker cost/cache/prefetch attribution cannot be
   trusted while one pool reports under two schemes.
8. **Pin the deployed build**: `/health` reports `version main`, commit
   `6fa6cc79…`, which is not in this repository (`git cat-file -t 6fa6cc79` →
   *not a valid object name*), while §7 recorded `1.4.39` / `bddf00b4` two days
   earlier. Nobody can diff what is running; tag the release next to the commit.
