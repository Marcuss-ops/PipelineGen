# Stock Pipeline

The stock pipeline generates video clips from one or more sources, cuts them according to a plan or explicit time ranges, composes the result, and publishes the output to Google Drive.

- Endpoint: `POST /api/stock-pipeline/run`
- `clip_duration` must be between 3 and 30 seconds (default from `video.clip_duration`)
- Output: MP4 files uploaded under the configured Google Drive root

## Supported source types

| Source type | Payload field | Behaviour |
|-------------|-----------------|-----------|
| Search query | `search_queries` | Searches the configured stock providers and selects matching videos. |
| Direct URL | `direct_urls` | Downloads a public video URL directly (e.g. YouTube). |
| Google Drive | `drive_urls` | Downloads a single Drive file or expands a Drive folder and picks the first video file. |
| Explicit clips | `clips` | Uses per-clip timestamps from the provided `ClipSpec` list. |

Source priority when mixed: `direct_urls` and `drive_urls` are treated as concrete sources. The orchestrator falls back through `direct_urls` → `drive_urls` → `search_queries` when resolving the root source for planning.

## Google Drive source support

The pipeline can read source media directly from Google Drive. This is useful when raw footage is already stored in a shared Drive folder.

### Supported URL shapes

Both folder URLs and single-file URLs are accepted in the `drive_urls` array.

- Folder URL: `https://drive.google.com/drive/u/2/folders/<folder_id>`
- File URL: `https://drive.google.com/file/d/<file_id>/view`
- Open URL: `https://drive.google.com/open?id=<file_id>`
- Direct download URL: `https://drive.google.com/uc?id=<file_id>`

When a folder URL is provided, the pipeline lists the folder contents and selects the **first file whose MIME type starts with `video/`**. Subfolders are not traversed.

### Authentication requirements

Google Drive access is configured through OAuth2 credentials stored in two files:

1. `credentials.json` — Google OAuth 2.0 desktop/web client credentials. Path configured by:
   ```yaml
   paths:
     credentials_file: "credentials.json"
   ```
2. `token.json` — OAuth token file for the authorized account. Path configured by:
   ```yaml
   paths:
     token_file: "token.json"
   ```

Both files must be present at server startup. The Drive reader uses `drive.DriveScope`. Ensure the account that generated `token.json` has at least **read** access to the source folders/files and **write** access to the configured destination folder.

To generate or refresh the token, use the existing Google auth flow helpers in `internal/infrastructure/drive/auth.go` or the project-specific tooling.

### Example payload: Google Drive folder with explicit clips

The following payload reads from a Drive folder (`drive_urls`) and produces five short clips, one per round. With explicit `start_sec`/`end_sec`, the pipeline extracts exactly the requested range (5 seconds in this example).

```json
{
  "drive_urls": [
    "https://drive.google.com/drive/u/2/folders/1NakegleSWsW59Npp8qnLNaoRU2r4iVtP"
  ],
  "folder_name": "Zhilei_Zhang_vs_Deontay_Wilder_Test",
  "clip_duration": 5,
  "clips": [
    {
      "round": 1,
      "title": "Round 1 - Inizio scontro",
      "description": "Inizio dell'incontro. Zhang prende subito il centro del ring...",
      "start_sec": 0,
      "end_sec": 5
    },
    {
      "round": 2,
      "title": "Round 2 - Montante e corpo",
      "description": "Zhang incrementa il ritmo mettendo a segno un buon montante...",
      "start_sec": 80,
      "end_sec": 85
    },
    {
      "round": 3,
      "title": "Round 3 - Studio e Jab",
      "description": "Ripresa molto tattica e a ritmo ridotto...",
      "start_sec": 143,
      "end_sec": 148
    },
    {
      "round": 4,
      "title": "Round 4 - Reazione Wilder",
      "description": "Wilder dimostra maggiora iniziativa cercando di variare i colpi...",
      "start_sec": 226,
      "end_sec": 231
    },
    {
      "round": 5,
      "title": "Round 5 - Knockout finale",
      "description": "Wilder sembra trovare fiducia piazzando un buon colpo destro...",
      "start_sec": 305,
      "end_sec": 310
    }
  ]
}
```

Submit it with curl (replace the token with your admin token):

```bash
curl -X POST http://127.0.0.1:8000/api/stock-pipeline/run \
  -H "Authorization: Bearer test-admin-token-12345" \
  -H "Content-Type: application/json" \
  -d @payload.json
```

A successful response looks like:

```json
{
  "job_id": "job_...",
  "run_id": "job_...",
  "status": "QUEUED",
  "deduplicated": false
}
```

The handler always runs stock pipeline jobs asynchronously (`Async=true` is forced before binding), so the response is always `QUEUED` with a `job_id`. Poll `/api/jobs/{job_id}/full` for terminal state.

Poll the job until it reaches a terminal state:

```bash
JOB_ID="job_..."
curl -s http://127.0.0.1:8000/api/jobs/$JOB_ID/full \
  -H "Authorization: Bearer test-admin-token-12345"
```

## Payload fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `drive_urls` | `[]string` | No | Google Drive folder or file URLs to use as source media. |
| `direct_urls` | `[]string` | No | Public video URLs (e.g. YouTube). |
| `search_queries` | `[]string` | No | Search terms for the stock provider search. |
| `clips` | `[]ClipSpec` | No | Explicit clip specifications. |
| `folder_name` | `string` | Yes* | Destination subfolder name under the configured Drive stock root. The handler does not validate emptiness; a missing value will fail later during the publish step. |
| `clip_duration` | `int` | No | Length of each generated clip in seconds (default from `video.clip_duration`; range 3–30). |
| `async` | `bool` | No | The handler always forces `true` for `/api/stock-pipeline/run`. Present for wire-shape audit. |
| `persist` | `bool` | No | When `true` and `async=false`, writes to `media_assets` in sync mode. |
| `drive_folder_id` | `string` | No | Legacy destination folder ID override. |
| `folder_id` | `string` | No | Legacy destination folder ID override. |

At least one source must be provided: `search_queries`, `direct_urls`, `drive_urls`, or `clips`.

## Output destination

The pipeline uploads results under the folder configured in `config.yaml`:

```yaml
drive:
  stock_root_folder: "<Drive folder ID>"
```

If `stock_root_folder` is empty, the unified `media_root_folder` is used as fallback. The actual output folder is created under the configured root using the `folder_name` value.

If both `drive_folder_id` and `folder_name` are supplied, the pipeline creates the subfolder under the provided `drive_folder_id` instead of the configured stock root.

## Canonical output profile

Every produced stock clip is normalized to the same canonical technical profile:

| Property | Value |
|----------|-------|
| Resolution | 1920×1080 |
| Frame rate | 24 fps (constant frame rate) |
| Video codec | H.264 (`libx264`, CPU-first default) |
| Pixel format | `yuv420p` |
| Audio codec | AAC |
| Audio sample rate | 48 kHz |
| Audio channels | stereo (2) |
| Container | MP4 with faststart |

Source material may have a different resolution, frame rate, or codec; the stock cutter re-encodes through a shared canonical FFmpeg filter chain (`scale/pad/fps/setpts`) so that all published clips share the same profile. The stock pipeline defaults to CPU-based `libx264` (`veryfast`, CRF 23) and does not require NVIDIA hardware. Hardware codecs such as `h264_nvenc` remain infrastructure-level overrides only when explicitly selected and supported by the host.

## VERIFIED state and ffprobe validation

A stock batch transitions to `VERIFIED` only after every produced clip passes mandatory ffprobe checks. The verification step fails closed: if a single clip is non-conformant, the whole batch is marked failed.

Required checks per clip:

| Field | Requirement |
|-------|-------------|
| `width` | 1920 |
| `height` | 1080 |
| `fps` | 24 (within a small technical tolerance) |
| `codec_name` | `h264` |
| `pix_fmt` | `yuv420p` |
| `audio_codec` | `aac` (when audio is enabled) |
| `sample_rate` | 48000 Hz |
| `channels` | 2 |
| `duration` | within tolerance of the requested clip length |
| `size` | > 0 |
| `sha256` | present and matches the on-disk file |

`VERIFIED` therefore means the clips exist **and** match the canonical profile, not merely that the files are present and have a positive duration.

## Limitations and behaviour notes

- Folder expansion is deterministic only to the extent that the Drive API list order is stable; the pipeline selects the first video file returned by `ListFiles`.
- Only files with a MIME type starting with `video/` are considered source candidates. Other file types are ignored.
- Drive source URLs are validated at the HTTP boundary: they must use HTTPS and a public hostname. Private IP or `file://` URLs are rejected.
- Source download failures surface as `stock.stage_sources` step failures in the job timeline.

## Related documentation

- [`docs/operations/stock-e2e-runbook.md`](operations/stock-e2e-runbook.md) — operational E2E battery and diagnostics.
- [`internal/api/assets/stock/handler.go`](../internal/api/assets/stock/handler.go) — HTTP handler and validation rules.
- [`internal/application/assets/providers/stock/stockpipeline/stager_adapter.go`](../internal/application/assets/providers/stock/stockpipeline/stager_adapter.go) — Drive download and folder expansion logic.
