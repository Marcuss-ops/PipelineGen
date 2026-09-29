# YouTube Live Search and Clip Testing Runbook

Operational notes, current contracts, and explicit verification boundaries for YouTube discovery, clip extraction, and the Stock pipeline.

## Canonical search endpoint

```http
POST /api/media/search
Content-Type: application/json

{
  "query": "Mike Tyson training documentary",
  "sources": ["youtube"],
  "universe": "discovery",
  "mode": "hybrid",
  "filters": {
    "sort": "views",
    "published_after": "2025-01-01T00:00:00Z"
  },
  "min_score": 0.5,
  "limit": 10
}
```

## Current limitations

### 1. Discovery provider wiring and behavior

The canonical composition path conditionally registers the YouTube search adapter when `features.youtube_enabled` is true and the YouTube service is available (`internal/app/wiring/registry_internal_modules.go`). The provider registry is frozen before search composition; `BuildSearchBackends` adapts registered providers into `SearchDiscovery` backends. If YouTube is disabled or the service is unavailable, it is not mounted; discovery then fails with no eligible backend rather than falling back to the catalog.

For unified search, callers must request `universe: "discovery"` and `sources: ["youtube"]`. The endpoint is metadata search; clip downloads/commits go through `POST /api/clips/process` and the asynchronous job flow. Stock acquisition's text queries are separately resolved through its YouTube `ChannelLister`.

### 2. No true "Suggested videos" endpoint

There is currently no `/api/media/suggested`, `/api/media/related`, or equivalent endpoint that returns the official YouTube suggested videos. Related-content behavior can only be approximated by building a new search query from the metadata of an existing video (title, uploader, tags) and calling `POST /api/media/search` again.

**Forward pointer:** any new related/suggested endpoint should be owned by the search capability (`internal/capabilities/assets/search`) and delegate to the canonical `search.Aggregator`.

### 3. Sort and publication-date filter contract

For YouTube discovery, the canonical request fields are `filters.sort` and `filters.published_after` (RFC3339 timestamp, inclusive). Supported sort values are `relevance`, `newest`, `oldest`, `longest`, `shortest`, and `views`. `relevance` is the default ordering and is accepted as a neutral value; the non-default sort modes and `published_after` are rejected with HTTP 400 unless the request selects `universe: "discovery"` and `sources` includes `youtube`. Sort/date metadata survives the provider adapter; after merge, the aggregator applies the requested deterministic ordering and inclusive date floor. Missing publication dates fail closed when a date floor is requested. Provider scores break ties after the requested primary sort.

### 4. Score floor and catalog/discovery separation

`min_score` is optional and must be in `[0,1]`; a positive value is forwarded to the semantic backend when selected and applied as an inclusive floor to merged candidates from every backend. Zero means no caller-specified floor (catalog backends may still apply their own retrieval floor). Use `universe: "discovery"` to avoid catalog semantic results entirely. A score floor changes what is returned but does not prove a live YouTube result set is empty.

## Verified working surfaces

### Local contract evidence

- `handler_discovery_contract_test.go` pins JSON binding, discovery selection, forwarded sort/date/min_score, invalid fields, date filtering and min_score behavior.
- `search_backend_provider_test.go` pins query forwarding, sort metadata preservation, and discovery backend mounting from a registered YouTube provider.
- `adapter_test.go` pins YouTube sort/date translation and candidate metadata; `search_topic_test.go` pins sorted/date-filtered results.
- `make verify-youtube`, `make verify-stock-unit`, and `make verify-pipeline-e2e` are the repository-owned local gates. They are not live YouTube/Drive/PostgreSQL certification.

## Live end-to-end certificate: YouTube → 10 languages → PostgreSQL → pgvector

`tests/e2e/youtube_multilingual_live_test.go` is the opt-in real (non-hermetic) certificate for the
whole chain: download a real YouTube clip, acquire its real subtitle transcript, commit it to the
PostgreSQL media SSOT, translate it into every configured language, rebuild the multilingual
`search_text`, and index it into PostgreSQL pgvector. It was not run as part of this repository-only verification. It is hard-gated behind `VELOX_E2E_LIVE=1` so
`go test ./...` stays hermetic — a live test that silently degrades to a mock is worse than no test.

The test calls **no** fakes on the critical path: `yt-dlp` for the download and the subtitles, the
canonical VTT parser, `PostgresMediaCommitter.CommitClipTextAndIndexEvent`, the real
`TextTrackMaterializer` with the real Ollama translator, the real `PostgresIndexWorker` and the real
E5 embedding sidecar.

### Prerequisites

- PostgreSQL + pgvector test instance (`docker compose -f docker-compose.test-postgres.yml up -d`),
  with `TEST_POSTGRES_DSN` pointing at it.
- A reachable Ollama with the configured model (default `gemma4:e2b`).
- The E5 embedding sidecar (default `http://127.0.0.1:8001`).
- `yt-dlp` on `PATH`, or `VELOX_E2E_YTDLP` set to the command. When the process runs with a
  `HOME` other than the pipeline user's (agent sandboxes, cron, a CI shell), the bare `yt-dlp`
  resolves its user `site-packages` against that `HOME` and dies with `No module named 'yt_dlp'`
  even though the install is present. Point the knob at the canonical wrapper the service itself
  uses (`YTDLP_PATH` in the service environment):

  ```bash
  VELOX_E2E_YTDLP='bash scripts/yt-dlp-pipeline'
  ```

  That wrapper also pins the node runtime and the bgutil POT plugin path, which the bare binary
  does not: without the provider YouTube answers 403 and the download fails deep inside the run.

### Run

```bash
TEST_POSTGRES_DSN='postgres://pipelinegen:pipelinegen@localhost:16432/pipelinegen_media_test?sslmode=disable' \
VELOX_E2E_LIVE=1 \
VELOX_E2E_YOUTUBE_URL='https://www.youtube.com/watch?v=iHaK0M-207o' \
go test ./tests/e2e/ -run TestLiveYouTube_TranscriptTranslatedInTenLanguagesAndIndexed -count=1 -v
```

### What it asserts

1. Download-once: one source stage is reused for the configured segments; hermetic coverage for the extraction service is in `extraction_staging_test.go`. The opt-in live test uses its configured workdir cache.
2. The real English subtitle track parses through the canonical VTT parser and yields cues; the
   empty-text cues YouTube auto-captions emit are dropped (the committer rejects them).
3. The clip, the READY transcript, the timed cues and the index request land in **one PostgreSQL
   transaction** — no SQLite media write is involved.
4. The materializer creates READY transcripts for all 10 configured languages (`it`, `en`, `pl`,
   `ru`, `de`, `es`, `pt-BR`, `fr`, `tr`, `id`) with `source_type=translation`, the source language,
   provider and a `translation_key`.
5. A second identical run **skips** every target (`created=0`, `skipped=9`): the `translation_key`
   gate prevents retranslation. The same run still **rebuilds** `search_text` — the repair is not
   conditional on something being created, or a clip whose translations are already present could
   never be re-indexed — and reports `changed=false`, so it requests **no** reindex
   (`texttracks.materialize.reindex_skipped`). That pair is the idempotence contract: a repeated run
   over a correct asset costs one `SELECT` and one string compare, not an embedding.
6. `media_assets.search_text` is rebuilt from all current transcript tracks, so the Italian
   translation is present in the document that is about to be embedded.
7. The reindex request appears in the **PostgreSQL** `outbox_events`. A stub wired as the SQLite
   outbox fails the test if the materializer ever falls back to it.
8. The `PostgresIndexWorker` drains the event, the E5 sidecar embeds the **new** document (the test
   records the embedded text and asserts it contains the translated phrase), `index_state` reaches
   `INDEXED` and `media_embeddings` holds a 768-dim vector for `intfloat/multilingual-e5-base`.

### Environment knobs

| Variable | Default | Purpose |
| --- | --- | --- |
| `VELOX_E2E_LIVE` | unset | Gate: must be set to run this test |
| `TEST_POSTGRES_DSN` | unset | Live PostgreSQL + pgvector DSN |
| `VELOX_E2E_YOUTUBE_URL` | `https://www.youtube.com/watch?v=iHaK0M-207o` | Source video |
| `VELOX_E2E_YTDLP` | `yt-dlp` | May be a multi-word command |
| `VELOX_E2E_WORKDIR` | `.tmp/e2e-youtube-live` | Download cache directory |
| `VELOX_E2E_OLLAMA_URL` | `http://localhost:11434` | Translator endpoint |
| `VELOX_E2E_OLLAMA_MODEL` | `gemma4:e2b` | Translator model |
| `VELOX_E2E_EMBED_SERVER_URL` | `http://127.0.0.1:8001` | E5 sidecar endpoint |
| `VELOX_E2E_FORCE_DOWNLOAD` | unset | Re-download even when the cache is warm |

### Troubleshooting

- `ModuleNotFoundError: No module named 'yt_dlp'` — `yt-dlp` is installed with `pip install --user`
  and its `site-packages` is not on the Python path of the test process. The canonical fix is to use
  the repo wrapper (`VELOX_E2E_YTDLP='bash scripts/yt-dlp-pipeline'`), which pins `HOME` to the
  pipeline user before resolving `yt-dlp` — the same resolution the service uses. The narrower
  alternative is to prefix the run with
  `PYTHONPATH="$HOME/.local/lib/python3.X/site-packages"` (matching the Python that `yt-dlp`'s
  shebang uses); it fixes the module path but NOT the node runtime or the POT plugin, so a video that
  needs a player challenge still fails.
- `invalid cue (... text_len=0)` — the video's captions carry empty cues; the test filters them.
  If it recurs, the caption track changed shape and the filter needs revisiting.

### Repairing clips that were translated but never re-indexed

Every clip committed before the September 2026 fix has its language rows in `asset_text_tracks`
while its `asset.index.requested` went to the operational SQLite outbox and dead-lettered, so
`media_assets.search_text` never contained the translations. Those clips are invisible to
multilingual search until the index input is repaired:

```bash
go run ./cmd/admin text-tracks-backfill \
    --source youtube \
    --languages en,it,de,es,pt-BR,fr,pl,ru,tr,id \
    --all --apply --json
```

`--all` (not `--only-missing`) is required: `--only-missing` skips a clip as soon as all target
languages are READY, which is exactly the state of a clip that needs repairing, so the repair would
never run. The command is safe to repeat — a clip whose `search_text` is already correct is rebuilt
but not reindexed. Read the two counters that answer "did this run change what search can see":
`index_repaired_total` (assets whose index input was recomposed) and `reindex_requested_total`
(assets actually sent to the index plane). The human output warns when `--only-missing` suppressed
the repair for every clip.

### Subtitles on Drive

Per-language subtitle artifacts (`.ass`) are delivered by one canonical owner,
`BackfillService.MaterializeSubtitleArtifacts`. Both the operator backfill and the
`asset.text.materialize` job handler call it — the job handler's fast path (the one a freshly
extracted YouTube clip takes when the acquisition chain found subtitles) previously ran the
translator only, which is why such a clip got its language rows and no subtitle files. The delivery
is idempotent: an unchanged artifact reuses its recorded Drive reference instead of re-uploading.

This test still does **not** drive the job handler (it calls the materializer directly), so the
Drive delivery is pinned hermetically instead: `backfill_subtitles_test.go` covers the delivery step
itself, and `jobs_subtitles_test.go` drives the REAL `MaterializeJobHandler` (with the real
materializer and the real `BackfillService`) to prove the fast path actually calls it — the
regression was a missing call, and a test of the step alone would not have caught it.

The other two chain rules are also hermetic, not live:

- **Priority 1** (`backfill_process_test.go`): a READY transcript with timed cues never triggers
  YouTube subtitles or Whisper, so re-running a repair over the catalog stays cheap; a READY
transcript *without* cues does re-acquire, and that re-acquire is deliberately fail-soft.
- **Download-once** (`extraction_staging_test.go` — previously untested): a multi-segment batch
  stages the full source exactly once and falls back to per-segment `yt-dlp` whenever the
  optimization cannot hold.

## Live certificate: subtitle clip-window contract (captions → clip timeline → `.ass`)

`tests/e2e/youtube_subtitles_clip_window_live_test.go` is the opt-in certificate for
PR-SUBS-CLIP-WINDOW. It exists because this deployment runs the acquisition chain with
`media.multilingual.source_priority=whisper_first`, so the live
`POST /api/clips/process` certificate above proves the **Whisper** leg and never the
**YouTube-caption** leg — and the caption leg is the one that broke.

The defect: the full video's VTT is read and only WINDOW-filtered, so its cues kept
SOURCE-video timestamps (e.g. 65000–80000 ms for a clip cut at 65s) while every consumer of a
clip's cues expects the CLIP timeline:

- `texttracks.ValidateASSFile` rejects an artifact whose last cue end exceeds
  `clipDurationMs + 250ms` → the artifact is `FAILED` and nothing is published;
- `cliprender.trimClipRenderCues` drops every cue with `StartMs >= duration` → the render ships
  with no subtitles at all.

The fix lives in `detail.RebaseCuesForClip` (`internal/kernel/asset/detail/timed_cues_clip_window.go`),
applied by `TextTrackResolver.acquireFromSubtitles` and by the backfill's `AcquireCommand`; the
video-level `GET /api/clips/transcript` port deliberately keeps source-video times (operators pick
clip windows from them).

### Prerequisites

- `yt-dlp` on `PATH`, or `VELOX_E2E_YTDLP` set to the command — same rule as the certificate above,
  including the multi-word wrapper (`VELOX_E2E_YTDLP='bash scripts/yt-dlp-pipeline'`). The test
  splits the value into argv and resolves a cwd-relative wrapper path against the module root (the
  test binary runs from `tests/e2e/`).
- **No** PostgreSQL, **no** Ollama, **no** embedding sidecar: the resolver is wired with the real
  `SubtitleFetcherAdapter` only, with `Repo` and `Transcriber` nil on purpose, because this
  certificate is about the caption leg alone.

### Run

```bash
VELOX_E2E_LIVE=1 \
VELOX_E2E_YTDLP='bash scripts/yt-dlp-pipeline' \
go test ./tests/e2e/ -run TestLiveYouTube_SubtitleCuesAreClipLocalAndASSValidates -count=1 -v
```

Expected: `--- PASS ... (8s)` with an `INFO text track acquired from YouTube subtitles` line
reporting a non-zero `cues` count.

### What it asserts

1. Real captions are downloaded (`yt-dlp --write-subs/--write-auto-subs --skip-download` into a
   throwaway cache) and parsed through the canonical VTT parser — no fakes on the critical path.
2. The bundle's language is honestly one of the configured ones, i.e. it is the language of the
   file that was actually read, not the first entry of the configured CSV.
3. Every cue is CLIP-local: `0 <= start < end <= durationMs`. Source-absolute cues (the pre-fix
   behaviour) start near `startSec*1000` and end near `endSec*1000`, both far outside that range —
   the default window (65→80s, 15s long) is chosen so the two timelines can never overlap
   accidentally.
4. The `.ass` compiled from those real cues passes `texttracks.ValidateASSFile` with the real clip
   duration — the exact gate the subtitle materializer and `clip.render` run.

If no bundle is acquired, the failure message carries the resolved yt-dlp command, its last error
and its last output: `FetchFullVTT` ignores the subprocess result on purpose ("best-effort: no
error if yt-dlp can't fetch subs" is the production contract), so without that record a failing
live download would surface only as `text track acquisition exhausted all 5 priorities`.

### Environment knobs

| Variable | Default | Purpose |
| --- | --- | --- |
| `VELOX_E2E_LIVE` | unset | Gate: must be `1` to run this test |
| `VELOX_E2E_YOUTUBE_URL` | `https://www.youtube.com/watch?v=iHaK0M-207o` | Source video; must expose captions |
| `VELOX_E2E_YTDLP` | `yt-dlp` | May be a multi-word command |
| `VELOX_E2E_SUBTITLE_LANGS` | `en,it` | Configured languages CSV handed to `--sub-langs` |
| `VELOX_E2E_CLIP_START_SEC` | `65` | Window start in the SOURCE video; must be `> 0` |
| `VELOX_E2E_CLIP_END_SEC` | `80` | Window end; `end-start` must be `< start` |

### Troubleshooting

- `real captions must be acquired` with `last yt-dlp err` — the download itself failed; read the
  recorded output. The usual causes are the bare `yt-dlp` resolving against the wrong `HOME`
  (use the wrapper) and a video whose captions track disappeared.
- `the cue is still on the SOURCE timeline` — the rebase regression is back: `acquireFromSubtitles`
  or `backfill_acquire.go` no longer passes `StartSec`/`EndSec` through
  `detail.RebaseCuesForClip`.
- `cue N ends after the ... clip` inside `ValidateASSFile` — same root cause, seen from the
  validator instead of the cue loop.

## Register-path clips: post-commit fan-out (translations + `.ass`)

Two commit routes write a clip into the PostgreSQL media SSOT:

| Route | Entry point | Fan-out |
| --- | --- | --- |
| `POST /api/clips/process` (extraction) | `ProcessYouTubeSegmentUseCase.step6to9_SubtitlesDriveWriter` | ✅ `enqueueMaterializeFanOut` |
| `POST /api/media/register-batch` (register) | `youtube.Service.Register → commitClipAtomically` | ✅ since Sept 2026 |

Before the fix the mapping (materialize vs. acquire, `und` language fallback, source-text hash)
lived inline in the extraction use case, so the Register route committed clip + ONE Whisper
transcript and stopped: **no translations, no `.ass` artifacts, invisible to multilingual search —
with no error anywhere** (the job succeeded, the clip was `INDEXED`, everything looked healthy).

The mapping now has ONE owner: `MaterializeFanOut.EnqueueCommittedClip`
(`internal/capabilities/assets/texttracks/fanout_commit.go`). Both routes call it through their
own single-method port, so a third commit route cannot ship without it again.

TWO COMMIT OUTCOMES OWE THE FAN-OUT, and both are handled:

- the clean commit — step 8.6 of `sourcing/youtube/service.go`;
- **BLOCKER #4** — the writer returns `ErrOutboxTerminalConflict` when the asset + transcript ARE
  committed and only the index event collides with a terminal outbox row. That is the normal
  outcome of re-sending an ALREADY PUBLISHED window with `force: true`, so the first live attempt
  at this seam missed it: `commitClipAtomically` returned before scheduling anything and the clip
  kept its single source transcript. The fan-out is now scheduled on that branch too, mirroring
  the extraction path's `processed_but_index_blocked` branch. A job reported FAILED by BLOCKER #4
  still leaves a durable, fully translated clip — only the re-index event is suppressed.

Live certificate of the seam (2026-09-28): force re-registration of
`yt_9Q6T-bzF4Vs_122_151_v1` produced, in one journal window,
`canonical clip writer: returning ErrOutboxTerminalConflict` → `texttracks.fanout.enqueue` →
`caller":"texttracks/fanout_commit.go:107","msg":"texttracks.materialize scheduled after YouTube
clip commit"` → `texttracks.materialize.done` with `retranslated=9, failed=0`, and the 10 `.ass`
came back `READY` with Drive references.

### Deploy note

The seam is Go source: it only takes effect after a rebuild + restart of the service.

```bash
make -o build-muscles build-server        # skip the Rust step when its toolchain is absent
sudo -n /usr/bin/systemctl restart pipelinegen.service
```

### Diagnose a clip that never materialized

```bash
# tracks per clip — a healthy clip has 10 (en + 9 targets), all READY
docker exec -i pipelinegen-postgres-test psql -U pipelinegen -d pipelinegen_media -c \
  "SELECT a.id, count(t.id) FROM media_assets a
   JOIN asset_text_tracks t ON t.asset_id=a.id AND t.is_current=1 AND t.status='READY'
   WHERE a.id LIKE 'yt_<VIDEOID>%' GROUP BY a.id HAVING count(t.id) < 10;"

# subtitle artifacts — READY + drive_file_id present
docker exec -i pipelinegen-postgres-test psql -U pipelinegen -d pipelinegen_media -tA -c \
  "SELECT count(*) FROM asset_subtitle_artifacts
   WHERE asset_id LIKE 'yt_<VIDEOID>%' AND is_current=1 AND status='READY';"
```

### Repair (idempotent, scoped)

```bash
cd refactored && set -a && source .env && set +a
go run ./cmd/admin text-tracks-backfill \
    --source youtube \
    --languages en,it,de,es,pt-BR,fr,pl,ru,tr,id \
    --asset-ids 'yt_9Q6T-bzF4Vs_122_151_v1,yt_9Q6T-bzF4Vs_137_151_v1' \
    --all --apply --progress=4
```

`--asset-ids` scopes the run to specific `media_assets.id` values (drop it, or use `--all`, to walk
the whole `--source` catalog). Read the counters at the end: `Languages created` (new translation
tracks), `Subtitles delivered` (`.ass` written + uploaded to `youtube_subtitles/<videoId>`),
`Index repaired` / `reindex requested`. `EXIT=0` with `Languages failed: 0` is success.

A clip whose artifacts are all `status=FAILED` needs one more step: its stored cues are still on the
SOURCE timeline (it predates `RebaseCuesForClip`), and priority 1 of the acquisition chain trusts
READY cues and never re-acquires — so the backfill happily rebuilds the same rejected `.ass`. The
repair is to drop the cue rows and let the chain re-acquire through the fixed code:

```bash
docker exec -i pipelinegen-postgres-test psql -U pipelinegen -d pipelinegen_media -c \
  "DELETE FROM asset_text_track_segments s USING asset_text_tracks t
   WHERE t.id = s.track_id AND t.asset_id = 'yt_<VIDEOID>_<start>_<end>_v1';"
# then re-run the scoped backfill above for that asset
```

(The text and its hash are untouched — only the timing rows go — so no translation is invalidated.)

## Local certificate: every `.ass` in the catalog validates

`tests/e2e/subtitle_artifacts_catalog_validate_test.go` is the hermetic counterpart of the live
certificate: it walks `asset_subtitle_artifacts` in `data/media/media.db.sqlite` and runs the
canonical `texttracks.ValidateASSFile` against every current `status=READY` file on disk, with each
clip's own `clip_duration_ms`. No network, no server; it skips when the catalog is absent.

```bash
# default: every READY .ass on disk must validate
go test ./tests/e2e/ -run TestSubtitleArtifacts_CatalogCurrentFilesValidate -count=1 -v

# strict: also require the full 10-language set for the listed asset-id prefixes
VELOX_ASS_CATALOG_PREFIXES=yt_9Q6T-bzF4Vs,yt_Lsv8jps7H9k,yt_vVCFbOn77Co,yt_Kq7Kqku3GO0 \
  go test ./tests/e2e/ -run TestSubtitleArtifacts_CatalogCurrentFilesValidate -count=1 -v
```

The default run fails only on real defects — a READY file the validator rejects, or an asset whose
every current row is `FAILED` (a clip that ships no subtitles at all). Language-set completeness is
env-scoped because the historical catalog legitimately holds partially materialized legacy assets;
a freshly delivered batch is held to the full contract by the strict run.

