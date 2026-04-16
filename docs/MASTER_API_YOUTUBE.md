# Master API – YouTube & Thumbnails

Fonte: `VeloxEditing/refactored/routes/youtube_routes.py`

Base URL (esempio): `http://<MASTER>:8000`

## Stato credenziali / quota
- **GET** `/api/v1/youtube/credentials/health`
- **GET** `/api/v1/youtube/credentials/quota`
- **POST** `/api/v1/youtube/credentials/cleanup`

## Canali e gruppi
- **GET** `/api/v1/youtube/channels`
- **GET** `/api/v1/youtube/groups`
- **GET** `/api/v1/youtube/groups/{group_name}`

## Uploads (stato dei video caricati)
- **GET** `/api/v1/youtube/uploads`
- **POST** `/api/v1/youtube/uploads/by_output_ids`
- **GET** `/api/v1/youtube/uploads/by_channel/{channel_id}`
- **GET** `/api/v1/youtube/uploads/by_output_id/{output_video_id}`
- **GET** `/api/v1/youtube/uploads/by_youtube_video_id/{youtube_video_id}`
- **GET** `/api/v1/youtube/uploads/latest_by_group`

## Video
- **GET** `/api/v1/youtube/videos`
- **GET** `/api/v1/youtube/videos/cached`
- **GET** `/api/v1/youtube/videos/latest_by_group`

## Pubblicazione / Privacy
- **POST** `/api/v1/youtube/videos/{youtube_video_id}/publish`
  - body: `{ "channel_id": "...", "privacy": "public|unlisted|private", "publish_time": "ISO8601"?, "title": "..."? }`
- **POST** `/api/v1/youtube/uploads/{output_video_id}/publish`
  - body: `{ "privacy": "public|unlisted|private", "publish_time": "ISO8601"?, "title": "..."? }`

## Titolo
- **POST** `/api/v1/youtube/videos/{youtube_video_id}/title`
  - body: `{ "channel_id": "...", "title": "..." }`

## Thumbnail (copertine)
### A) Per youtube_video_id
- **POST** `/api/v1/youtube/videos/{youtube_video_id}/thumbnail`
  - body: `{ "channel_id": "...", "thumbnail_url": "https://..." }`

### B) Per output_video_id (consigliato)
- **POST** `/api/v1/youtube/uploads/{output_video_id}/thumbnail`
  - body: `{ "thumbnail_url": "https://..." }`

### C) Alias compatibili
- **POST** `/api/v1/youtube/thumbnail/set_url`
  - body: `{ "output_video_id": "...", "thumbnail_url": "https://..." }`
- **POST** `/api/v1/videos/thumbnail/set_url`
  - body: `{ "output_video_id": "...", "thumbnail_url": "https://..." }`

### D) Bulk apply
- **POST** `/api/v1/youtube/uploads/thumbnails/bulk_apply`
  - body: 
    - `{ "thumbnail_url": "https://...", "output_video_ids": ["...", "..."] }` **oppure**
    - `{ "mapping": { "output_id": "https://...", "output_id2": "https://..." } }`

### E) Auto match (per lingua)
- **POST** `/api/v1/youtube/uploads/thumbnails/auto_match`
  - body:
    - `group` o `job_id` o `output_video_ids`
    - `thumbnails_by_lang` oppure `thumbnails` (lista filename/url)
    - `default_thumbnail_url` (opzionale)
    - `apply` (bool, default true)

## Livestream
- **GET** `/api/v1/youtube/livestreams`
- **GET** `/api/v1/youtube/groups/{group_name}/livestreams`
- **GET** `/api/v1/youtube/livestreams/{stream_id}`
- **POST** `/api/v1/youtube/livestreams/start`
- **POST** `/api/v1/youtube/livestreams/{stream_id}/stop`
- **POST** `/api/v1/youtube/livestreams/{stream_id}/metadata`
- **POST** `/api/v1/youtube/livestreams/{stream_id}/thumbnail`
- **POST** `/api/v1/youtube/livestreams/{stream_id}/add-local-video`

## Utility
- **GET** `/api/v1/youtube/channels/{channel_id}/videos/private_unlisted`
- **GET** `/api/v1/youtube/groups/{group}/videos/private_unlisted`

## Note operative
- Per impostare la copertina è consigliato usare l’endpoint basato su **output_video_id**:
  `POST /api/v1/youtube/uploads/{output_video_id}/thumbnail`
- Per aggiornare la copertina partendo da **youtube_video_id**, usa:
  `POST /api/v1/youtube/videos/{youtube_video_id}/thumbnail` con `channel_id`.
- Quota exceeded restituisce HTTP 429 con dettagli.
