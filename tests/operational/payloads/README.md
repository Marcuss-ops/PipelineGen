# register-batch payloads (CLIPS timestamp path)

Payloads for `POST /api/media/register-batch` — the **clips** capability's
YouTube registrar. This is the correct endpoint when you already know *which
seconds* you want: operator-supplied highlight windows, one clip window in →
one clip file out, uploaded to Drive.

> Do **not** use `POST /api/stock-pipeline/run` for this. That endpoint is the
> stock pipeline: its explicit planner
> (`stockpipeline/step_plan_clips.go::expandExplicitClipSpecs`) auto-splits any
> clip whose duration is **>= 60s** into **5-second children**, so a 61-83s
> highlight window comes back as a dozen 5s fragments. `register-batch` has no
> such split: `seconds_per_segment` omitted ⇒ the window is downloaded whole.

## Files

| File | Source video | Clips | Drive `folder_id` |
|---|---|---|---|
| `weeknd-goat-talk.register-batch.json` | `PnmPmDznqnU` — *The Weeknd & Jenna Ortega ... GOAT Talk* | 7 hooks + 13 clips | `19rnS2…SiRtl` |
| `weeknd-sally-interview.register-batch.json` | `lQ66h_B5f00` — *Sally ft. The Weeknd ... L'interview* | 7 hooks + 13 clips | `19rnS2…SiRtl` |
| `rihanna-graham-norton.register-batch.json` | `5ksU76kWyvU` — *Rihanna Has A Little Problem \| The Graham Norton Show* | 9 clips | `1u1rYp…hizJL` |
| `rihanna-seth-meyers-day-drinking.register-batch.json` | `X3n5Pk8fkLg` — *Seth and Rihanna Go Day Drinking* | 12 clips | `1u1rYp…hizJL` |

Each payload writes into the Drive folder `folder_id` carried in its own body —
the four files target two different folders, so run them by explicit path
rather than relying on the directory-wide default:

```bash
bash tests/operational/register_clips_batch.sh --wait \
  tests/operational/payloads/rihanna-graham-norton.register-batch.json \
  tests/operational/payloads/rihanna-seth-meyers-day-drinking.register-batch.json
```

## Field mapping (analysis artefact → wire contract)

| Analysis field | Wire field | Notes |
|---|---|---|
| `video_url` | `clips[].url` | per clip, so several videos can share one request |
| `title` (video) | — | not a wire field; provenance lives in `group` / `tags` |
| `hook_clips[]` | `clips[]` | **`hook_clips` is not any server contract** — hook windows are ordinary clips |
| `hook_clips[].topic` / `clips[].topic` | `clips[].name` + `topics[]` | `name` also drives the Drive filename |
| `summary` | `clips[].summary` (+ `hook` for hooks) | |
| `vector_description` | `clips[].description` | keyword string, kept verbatim |
| `start` / `end` (`"00:23"`, `"01:24"`) | `clips[].start` / `clips[].end` | **seconds as numbers**: `02:11` → `131` |
| `clip_id` | — | not a wire field; identity comes back as the response `ClipID` |

`start`/`end` are `float64` seconds. `"00:23"` is **not** accepted — convert
first (`mm*60 + ss`).

## Run it

```bash
# 1) validate + summarize locally, no request, no token needed
bash tests/operational/register_clips_batch.sh --dry

# 2) real run (needs the server up with Drive wired + worker active)
API_BASE=127.0.0.1:8000 \
VELOX_ADMIN_TOKEN="$VELOX_ADMIN_TOKEN" \
  bash tests/operational/register_clips_batch.sh --wait
```

Response is an **enqueue ack** (`{ok,total,enqueued_count,enqueue_failed,results[]}`),
not the download result: `enqueued_count` means the clip jobs reached the
worker queue. `--wait` polls `GET /api/jobs/{id}` per clip; `results[].JobID`
is the handle.

### Per-clip report (`--wait`)

With `--wait` the driver closes with a per-clip table read from each job's
`/full` envelope:

```
== per-clip summary (from job results) ==
CLIP                                        SEC  CLIP_ID                   DELIVERY   DRIVE_LINK
Hook 1 - L'ossessione per Pippo            10.0  yt_PnmPmDznqnU_0_10_v1    PUBLISHED  https://drive.google.com/file/d/1lp2yOFg5.../view
…
40 clip: 40 con drive_link, 40 duplicati, 0 senza link
```

`clip_id`, `drive_link` and `delivery_status` come from `result{}`. The
duration is the requested window (`end - start`) from the job payload:
`result{}` carries no duration field, and the catalog's own `duration` for the
same clip equals it (759→799 = 40s).

A row whose `drive_link` is `-` is printed as-is (delivery `LOCAL_ONLY`, i.e.
not published) — the real state, never an invented link.

Those rows are then repeated in a dedicated block so a missing upload cannot
hide behind the counters:

```
== UNPUBLISHED CLIPS (no drive_link) ==
CLIP                       CLIP_ID                  DELIVERY     JOB
Cast al femminile ...      yt_5ksU76kWyvU_43_100_v1 -            SUCCEEDED
…
  -> LOCAL_ONLY: POST /api/media/clips/{source}/clips/<clip_id>/reupload (only while the local file still exists)
  -> otherwise re-send that window with "force": true (…)
```

The column `JOB` separates the two situations: `SUCCEEDED` + no link means the
local file exists but was never uploaded (`LOCAL_ONLY`), while `FAILED` means
the clip job itself died. **A run with unpublished clips exits 1** so it cannot
pass unnoticed; pass `--allow-unpublished` when a local-only result is
acceptable.

Preconditions: Drive configured (`folder_id` routing fails closed with 503
otherwise), jobs worker running, yt-dlp/provider available. The payload
contract itself is pinned by
`internal/capabilities/assets/register/payload_contract_test.go`, which
decodes these files through the real `BatchRegisterRequest` DTO and asserts the
windows survive `expandClipsBySegments` unsegmented.

## Verified live — 2026-09-21

Run against the local server (`bin/pipelinegen --mode all`, API on `:8000`)
with `--wait`:

```
weeknd-goat-talk      enqueued: 20/20   enqueue_failed: 0
weeknd-sally-interview enqueued: 20/20   enqueue_failed: 0
40/40 clip jobs -> SUCCEEDED        (driver exit 0)
```

39 of the 40 windows already existed in the catalog from an earlier run and
were deduplicated by the per-clip external ref; the single missing window
(`yt_PnmPmDznqnU_759_799_v1`) was downloaded and uploaded during this run:
`https://drive.google.com/file/d/1I6MfAW9FCvzzlCoqsFsQ9AQFlNrulF-N/view`.

The run closed with the per-clip table above: `40 clip: 40 con drive_link, 40
duplicati, 0 senza link` (every window already existed, so every job took the
dedup path and still exposed its canonical `clip_id` + `drive_link`).

Post-run catalog check: every expected id is present. Note that
`yt_PnmPmDznqnU_759_798_v1` also exists — an earlier registration of the same
window with `end=798`; the two differ only in the trailing second (clip
identity is `yt_<videoID>_<start>_<end>_v1`, so a 1-second change mints a new
id).

## Verified live — 2026-09-21 (Rihanna run)

Same day, second folder (`1u1rYpWeTkcedezM2f-lX6FTrq2hiIZJL`), both videos new
(real downloads, no dedup shortcut):

```
rihanna-graham-norton        enqueued:  9/9   enqueue_failed: 0
rihanna-seth-meyers-day-drinking  enqueued: 12/12  enqueue_failed: 0
21/21 clip jobs -> SUCCEEDED · catalog: 21/21 con drive_link, 21/21 INDEXED
21/21 local files present under data/media/clips/general/
```

`yt_X3n5Pk8fkLg_120_180_v1` is the useful proof that this path does NOT
reshape the windows: its catalog `duration` is exactly `60000000000` ns (60s),
while the same 60s window sent through `/api/stock-pipeline/run` would have
been cut into twelve 5s children.

### Two operational notes from that run

1. **Never auto-retry the POST.** The driver used to re-post on a 429; since
   `/api/media/register-batch` is not idempotency-gated, a re-post enqueues the
   batch twice and the second copy fails on already-terminal outbox rows
   (`outbox: event suppressed by existing terminal row`). The driver now
   reports the 429 and stops instead.
2. **Re-registering a window that is in the catalog but has no file.** A
   duplicate job can leave a row that is `INDEXED` while its local file is
   missing and no Drive upload happened (`delivery_status: LOCAL_ONLY`).
   `POST /api/media/clips/youtube/clips/{id}/reupload` fixes it only while the
   local file still exists; when it does not, re-send that single window with
   `"force": true` (`service.go:154` skips the external-ref dedup). The forced
   job completes download + Drive upload; its final index commit is still
   refused by the already-completed outbox row, so the job reports FAILED
   even though the file and the `drive_link` are now correct.

Drive credentials: the doctor's `google_token` probe only stats
`<DataDir>/token.json` and `./token.json`, so with the repo's relative
`VELOX_TOKEN_FILE`/`VELOX_CREDENTIALS_FILE` defaults it reports `missing` even
when `~/.config/velox/token.json` exists. Symlinking both into the repo root
fixes the probe; the value of the remaining `ok: false` is `google_accounting:
disabled` (an optional subsystem, not a blocker).
