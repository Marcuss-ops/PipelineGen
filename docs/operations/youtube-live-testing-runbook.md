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

