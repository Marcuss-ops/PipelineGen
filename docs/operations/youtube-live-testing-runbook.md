# YouTube Live Search Testing Runbook

Operational notes and current limitations for testing the PipelineGen YouTube search surface.

## Canonical search endpoint

```http
POST /api/media/search
Content-Type: application/json

{
  "query": "Mike Tyson training documentary",
  "sources": ["youtube"],
  "mode": "hybrid",
  "limit": 10
}
```

## Current limitations

### 1. YouTube live backend is not mounted

`GET /api/capabilities` reports the `youtube` capability as `NOT_MOUNTED`. As a result, `POST /api/media/search` with `sources: ["youtube"]` does **not** reach a live YouTube search provider. The aggregator falls back to the only eligible backend: the semantic (Qdrant) backend.

Consequences for callers:

- Returned items have `"source": "semantic"` instead of `"youtube"`.
- Items expose `"drive_link"` (a Google Drive URL hydrated from SQLite) instead of `"thumbnail_url"` / `"preview_url"`.
- Titles are often asset IDs or generic test labels rather than real YouTube titles.

**Forward pointer:** canonical wiring surface is `internal/app/search_backends.go` (`BuildSearchBackends`). The YouTube adapter lives in `internal/capabilities/assets/providers/youtube/adapter.go` and must be registered in the provider registry passed to `BuildSearchBackends`.

### 2. No true "Suggested videos" endpoint

There is currently no `/api/media/suggested`, `/api/media/related`, or equivalent endpoint that returns the official YouTube suggested videos. Related-content behavior can only be approximated by building a new search query from the metadata of an existing video (title, uploader, tags) and calling `POST /api/media/search` again.

**Forward pointer:** any new related/suggested endpoint should be owned by the search capability (`internal/api/assets/search`) and delegate to the canonical `search.Aggregator`.

### 3. `sort` and `publishedAfter` are not exposed in the public DTO

The internal YouTube provider adapter (`internal/capabilities/assets/providers/youtube/adapter.go`) supports native sort modes and a `publishedAfter` filter, but the public `POST /api/media/search` request DTO does not include `sort` or `publishedAfter` fields. Callers cannot request view-based sorting or date filtering through the canonical HTTP surface.

**Forward pointer:** extend `searchRequest` in `internal/api/assets/search/handler.go` and map the new fields to `providers.SearchFilters` in the handler.

### 4. Nonsense queries still return semantic matches

Because the semantic backend performs approximate vector matching, a query such as `zzzz_pipelinegen_impossible_query_987654321` does **not** return an empty `items` array. It returns low-score matches from the indexed corpus. This makes the "no results" negative test fail until the live YouTube backend is mounted or a score cutoff is applied.

**Forward pointer:** add a minimum-score threshold in `internal/application/search/aggregator.go` or in the semantic backend (`internal/application/search/search_backend_semantic.go`).

## Verified working surfaces

The following endpoints were tested and behave as expected:

- `GET /api/clips/diagnostics` — returns `ok: true` with `ytdlp`, `ffmpeg`, and `node` checks passing.
- `GET /api/clips/info?url=...` — correctly resolves both `https://www.youtube.com/watch?v=...` and `https://youtu.be/...` URLs and returns full metadata including `id`, `title`, `duration`, `uploader`, `view_count`, `thumbnail`, `thumbnails`, `chapters`, `categories`, and `tags`.

