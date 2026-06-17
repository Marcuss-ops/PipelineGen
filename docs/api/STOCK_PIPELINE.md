# API Reference: Stock Pipeline

The Stock Pipeline service automates the process of searching for videos, extracting clips, applying transitions/effects, and uploading the final rendered chunks to Google Drive. It also handles semantic indexing for future script generation suggestions.

## Run Stock Pipeline

Downloads and processes video sources into a compilation.

- **Endpoint:** `POST /api/stock-pipeline/run`
- **Auth Required:** Optional (depends on configuration)
- **Response Type:** Job Enqueued (Asynchronous)

### Request Body

```json
{
  "search_queries": ["query 1", "query 2"],
  "direct_urls": ["https://youtube.com/watch?v=..."],
  "total_minutes": 20,
  "chunk_duration": 25,
  "clip_duration": 4,
  "no_effects": false,
  "no_transitions": false,
  "max_videos": 20,
  "folder_id": "DRIVE_FOLDER_ID",
  "folder_name": "TargetFolderName",
  "subfolder": "CategoryName"
}
```

| Field | Type | Description | Default |
| :--- | :--- | :--- | :--- |
| `search_queries` | array | List of strings to search on YouTube. | [] |
| `direct_urls` | array | List of direct YouTube/Video URLs to process. | [] |
| `total_minutes` | int | Total duration of the generated footage in minutes. | 5 |
| `chunk_duration` | int | Duration of each output video file in seconds. | 25 |
| `clip_duration` | int | Duration of each individual clip cut from sources. | 5 |
| `no_effects` | bool | If true, skips visual overlay effects. | false |
| `no_transitions` | bool | If true, skips transitions between clips. | false |
| `max_videos` | int | Limit the number of source videos to download. | 0 (no limit) |
| `folder_id` | string | Explicit Google Drive Folder ID for output. | "" |
| `folder_name` | string | Name of the folder to create inside `folder_id` or root. | "" |
| `subfolder` | string | Optional category subfolder name. | "" |

### Success Response

```json
{
  "job_id": "job_123456789",
  "message": "Stock pipeline job enqueued",
  "status_url": "/api/jobs/job_123456789/full"
}
```

## Search and Run

Specifically optimized for search-based stock generation.

- **Endpoint:** `POST /api/stock-pipeline/search-and-run`

### Request Body

```json
{
  "queries": [
    { "q": "nature slow motion", "limit": 10 }
  ],
  "total_minutes": 10,
  "no_effects": true,
  "folder_name": "NatureStock"
}
```

## Features

- **Interleaving:** Clips are automatically interleaved from different sources to ensure visual variety.
- **Normalization:** All clips are normalized to the project's standard resolution/FPS before concatenation.
- **Semantic Indexing:** Each generated chunk is automatically indexed into the local database and Qdrant vector store, making it searchable by concepts.
- **Metadata Sidecar:** Generates `metadata.json` for each chunk on Drive containing source references and timestamps.
