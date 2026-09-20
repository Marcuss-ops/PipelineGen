# Remote worker — connection kit and first pre-job

Everything here was verified on 2026-09-20 from **creator-77 (this host,
`YOutube`, 77.93.152.122)** against the **remote master `51.91.11.36:8000`**.

| File | Purpose |
|---|---|
| `preflight.sh` | Read-only connection check: liveness, readiness, M2M key, route inventory. Never enqueues work. |
| `inventory.sh` | Read-only inventory of the media SSOT (`pipelinegen_media`) — what material a pre-job can reference. |
| `pre-job.creator-77.json` | First PREPARE payload, built from real DB assets. |
| `finalize-job.creator-77.json` | First FINALIZE payload (overlay + runtime audio), built from real Drive identities. |
| `run-flow.sh` | Submitter/poller for `pre → finalize`. Submits only with `--yes`. |

## 1. Topology (verified)

| Role | Endpoint | Evidence |
|---|---|---|
| Remote master (target of the pre-job) | `http://51.91.11.36:8000` | `GET /health` → `commit 106f59e4`, `/health/ready` → `ready`, 11 checks |
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

## 3. Endpoint matrix on the remote master (live, today)

| Method + path | Result | Meaning |
|---|---|---|
| `GET /health`, `GET /health/ready` | 200 | master healthy and ready |
| `POST /api/v1/jobs` | 401 without token, **400 with our key** | M2M surface mounted, key accepted (`invalid_json` on an empty body — no job created) |
| `GET /api/v1/jobs/{id}` | 404 on an unknown id | read route mounted, job lookup answered |
| `POST /api/v1/jobs/pre` | **404** | PREPARE route **not on the running binary** |
| `POST /api/v1/jobs/{id}/finalize` | **404** | FINALIZE route **not on the running binary** |
| `POST /api/v1/admin/m2m/keys` | 401 (admin token) | admin key-minting surface present on the remote |

`preflight.sh` exits **4** for this state: connection and credentials are good,
but only the legacy submit/poll surface is live. A prepare/finalize build has to
be deployed on `51.91.11.36` before the two-stage flow can be exercised there.
The same probe against the local master (`VELOX_MASTER_URL=http://127.0.0.1:8000`)
also reports `pre`/`finalize` absent, and its `m2m_clients` table is empty
(no M2M client provisioned locally).

## 4. What material we actually have

`./inventory.sh` (media SSOT `pipelinegen_media`, 2 314 rows):

| source | n | with Drive id | hours | notes |
|---|---|---|---|---|
| youtube | 1 193 | 1 190 | 3.25 | the real scene material (clip extracts) |
| voiceover | 717 | 717 | 3.23 | TTS masters |
| script | 168 | 168 | — | text tracks |
| internet_images | 103 | 103 | — | overlay images |
| script.localized_render | 78 | 78 | 1.04 | multilingual renders |
| clip.render | 21 | 21 | 0.28 | rendered clips |
| sound_effect | 9 | 9 | — | SFX |
| bgm | 6 | 6 | 0.40 | background music |
| editorial | 2 | 2 | — | curated plates |
| **stock** | **2** | **2** | **0.00** | `clip_001.mp4`, `clip_002.mp4` (5 s each) |

The stock collection itself is effectively empty (two 5-second clips plus a
metadata blob); the usable scene material is the 1 190 Drive-backed youtube
clips. `binary_sha256` is populated for none of them, so the payloads carry
`sha256: ""` for everything except `clip_001.mp4`, whose
`metadata_json.content_hash` is used instead.

## 5. Provenance of the assets in the payloads

| Payload field | Asset | Drive file id | Hash |
|---|---|---|---|
| `scenes[0].clip` / `.stock` | `planner:6432396435356433:0` (`stock/clip_001.mp4`, 5 s, 1 336 115 B) | `18MzFZcGHPqOnSwtqrWG5dIF9X8jKIvG-` | `ed23dd9ac93951253a0bacc49445dbc1972d878c1f76c4e22b26175f338a9cd9` (content_hash) |
| `scenes[1].clip` | `yt_vLRjqTIiMjc_270_355_v1` (Dolly Parton, 85 s) | `1kC7WB7S-HlIdOCiTEPAeB-Uv7gqzY-JN` | not stored |
| `overlays[0]` | `internet_images` image row | `1doHJp98YAGsbOe3q1ZXRdI-6UM1O4axh` | not stored |
| `runtime_assets[0]` | `bgm1` (`type beat`) from the editorial catalog | `1X4-wfIwrR51eDxIegciuBAJzKSdP3gcX` | catalog has no hash |
| `runtime_assets[1]` | `whop1` (transition SFX) from the editorial catalog | `1Fgr2jWQC1G6EHo-jhBAwjGtdcZo1PfaX` | catalog has no hash |

Caveats:

- the `internet_images` rows store Drive ids with a trailing `|` artifact
  (`…4axh|`); the payload uses the trimmed id — if the overlay 404s on Drive,
  re-read the id from the media search surface before blaming the worker;
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

./run-flow.sh --yes --pre            # submit only the pre-job, poll to terminal
./run-flow.sh --yes --finalize JOB   # finalize an existing job
./run-flow.sh --yes --all            # pre → poll → finalize → poll
./run-flow.sh --yes --all --surface=enqueue   # force the legacy submit surface
```

Without `--yes` nothing is submitted: a real send creates a job on the target
master. `--run-id` overrides the idempotency-key prefix (default: UTC
timestamp), `--timeout`/`--interval` control polling.

The `velox` operator CLI is currently out of sync with this master surface
(`velox search … --json` → `job not found: status=404`); the scripts here talk
HTTP directly.
