# Remote material agent — Runtime SDK, poller and resolution policy

**Status:** SDK + agent library landed 2026-09-22. The runnable CLI
(`cmd/material-agent`) is the next step; everything below is callable today from
Go.

## Why

The remote computer must be the operational brain of the material, while the
Master ("77") stays the SSOT: catalog, locations, job registry, receipts. The
rule is:

> **77 does not choose the material. The remote chooses. 77 keeps the truth of
> what exists and where it is.**

So the remote needs (a) a complete client for the runtime endpoints, (b) exactly
one job poller, and (c) a resolution policy that consults the catalog before
acquiring anything new.

## 1. Runtime SDK — `pkg/veloxclient`

`routes.go` is the single place wire paths live; every bound path is asserted
against `architecture/routes.yaml` by `routes_test.go` (the stock-pipeline
routes are anchored to the capability prefix registry for the same reason
`/api/script/generate` is — the gen-api-docs composition does not mount them).

| Method | Endpoint | Purpose |
|---|---|---|
| `SearchClipsByTopic` | `GET /api/clips/search` | live YouTube discovery + ranking |
| `ClipInfo` | `GET /api/clips/info?url=` | full metadata, no download (`Raw` keeps unmodelled fields) |
| `MediaSearch` | `POST /api/media/search` | the Master's registered catalog |
| `SubmitAsync` | any async endpoint | enqueue (`clips/process`, …) |
| `StockPipelineRun` | `POST /api/stock-pipeline/run` | acquisition by queries/direct URLs |
| `StockPipelineSearchAndRun` | `POST /api/stock-pipeline/search-and-run` | acquisition by search (`queries`, not `search_queries`) |
| `RegisterBatch` / `RegisterFromYouTube` | `POST /api/media/register-*` | register durable bytes (wire shape) |
| `UploadVideoClip` | `POST /api/media/clips/upload-video` | multipart self-import |
| `DownloadClip` | `POST /api/media/clips/:source/clips/:id/download` | stream the artifact |
| `WaitJob` | `GET /api/jobs/{id}/full` | **the one poller** |

`WaitJob` policies: 2s → 1.5x → 15s cap, 30m default timeout, context-aware,
treats both server vocabularies as terminal
(`SUCCEEDED/FAILED` and `queued/running/completed`), retries a not-yet-visible
job instead of reporting it missing, returns the final response alongside
`ErrJobFailed`, and `ErrPollTimeout` on deadline.

## 2. Agent — `pkg/materialagent`

```go
agent := materialagent.NewDefaultAgent(veloxclient.New(masterURL, m2mSecret))

materials, err := agent.Resolve(ctx, materialagent.MaterialRequest{
    ProjectID:   "elon_doc_01",
    SceneID:     "scene_17",
    Description: "Elon Musk walking through a Tesla factory",
    MaterialType: "video",
    SemanticRole: "supporting",
    Count:        3,
    Constraints:  materialagent.Constraints{DurationMinSeconds: 5, DurationMaxSeconds: 12},
    Destination:  &materialagent.Destination{Group: "Clips", FolderID: "<id>", SubfolderName: "Tesla"},
})
```

Resolution order (policy, overridable per request via `Sources`):

1. **catalog** — `POST /api/media/search` over the Master's SSOT. A hit is
   already durable: zero acquisition cost.
2. **youtube** — `GET /api/clips/search` → `GET /api/clips/info` →
   `POST /api/clips/process` with `selection.mode=important` (the Master derives
   the important parts; the remote does not guess timestamps) → `WaitJob`.
   Pass `MaterialRequest.Segments` to force explicit windows instead.
3. **stock** — `POST /api/stock-pipeline/search-and-run` → `WaitJob` for generic
   b-roll.

Rules baked into the code:

- adding a source = implementing `Resolver` and `Register`ing it (no if-chain);
- the scorer only adjusts what a resolver cannot know: duration fit, already
  durable bytes, resolution (`DefaultScorer`);
- a retry is deterministic: the idempotency key is
  `mat:<project>:<scene>:<source-ref>`;
- nothing found returns `ErrNoMaterial` joined with every per-resolver error —
  never a fabricated item, and never a silently degraded run;
- `Agent.ImportFile` is the only way the remote adds bytes (`upload-video`);
  it never touches the database.

## 3. MVP chains (to certify against a live Master)

| # | Chain | Expected |
|---|---|---|
| 1 | catalog hit | `Resolve` returns a registered `Material`, **no** `/api/clips/search` call |
| 2 | catalog miss → YouTube | `clips/process` (`selection=important`) → `SUCCEEDED` → registered asset id |
| 3 | generic b-roll | catalog + discovery empty → `search-and-run` → `SUCCEEDED` → registered asset id |
| 4 | self-import | `ImportFile` → `clip_id` + `drive_file_id` |

Chains 1–4 are unit-verified with a fake Master
(`pkg/materialagent/agent_test.go`, `pkg/veloxclient/runtime_test.go`). A live
run additionally needs the M2M key to carry `media.read` (see
[`network-exposure.md`](network-exposure.md) §5) and a running Master.

## 4. Not done yet

- `cmd/material-agent` — the runnable process that turns a script/scene plan
  into `MaterialRequest`s, calls `Resolve`, caches ephemeral downloads under a
  local cache dir and cleans up after registration.
- Worker affinity (jobs declaring a `required_capability` so a remote GPU
  worker claims the heavy ffmpeg/yt-dlp work) — the Master currently executes
  the acquisition jobs itself.
- `MaterialPolicyResolver` as data (a config file) rather than the Go
  `DefaultPolicy`/`DefaultScorer` defaults.
