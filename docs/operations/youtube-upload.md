# YouTube direct upload via InstaEdit

PipelineGen publishes a rendered video to a connected YouTube channel through
InstaEdit. The job type is `youtube.upload`; the destination is the InstaEdit
channel `destination_id` (`velox_destination_id`) or, for legacy callers, the
InstaEdit `platform_account_id`. The raw YouTube channel id is never the
destination: InstaEdit owns the OAuth token and the channel mapping.

## Configuration

The publishing key is separate from the Calendar key: Calendar needs the
`automation` permission, while direct publishing needs `read`, `accounts`,
`media`, `write` and `publish`. Keeping them separate prevents one integration
from overwriting the other.

```dotenv
INSTAEDIT_PUBLISHING_BASE_URL=https://api.instaedit.org
INSTAEDIT_PUBLISHING_API_KEY=...
INSTAEDIT_PUBLISHING_WORKSPACE_ID=5
```

`INSTAEDIT_BASE_URL` is used as the base URL fallback. If the publishing key is
absent the server boots normally but does not register the `youtube.upload`
handler; the M2M automation catalog consequently does not advertise it.

## Discover the destination

Use the InstaEdit catalog directly (the key is workspace-scoped and needs
`read`):

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $INSTAEDIT_PUBLISHING_API_KEY" \
  -H "Accept: application/json" \
  "https://api.instaedit.org/api/v1/publishing/targets?workspace_id=$INSTAEDIT_PUBLISHING_WORKSPACE_ID"
```

Pick a channel whose `status` is `active`, `enabled` is `true` and
`is_publishable` is `true`, then use its `destination_id`
(`velox_destination_id`, for example
`instaedit_extdst_01Jqnvllerxlioacpdxk5mjqxurw4`). The same row also returns
`channel_name` for display and the last publication recorded by InstaEdit
(`last_published_at`, `last_published_title`, `last_published_video_id`,
`last_published_url`). Those fields are informational: publications made
outside InstaEdit are not part of that history. `platform_account_id` remains
supported as a legacy selector.

## Submit the job

Admin surface:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{
    "type": "youtube.upload",
    "payload": {
      "publishing_target": {
        "workspace_id": 5,
        "type": "channel",
        "destination_id": "instaedit_extdst_01Jqnvllerxlioacpdxk5mjqxurw4"
      },
      "publications": [{
        "publication_id": "main",
        "output_ref": {"variant_id": "final"},
        "metadata": {
          "title": "Titolo del video",
          "description": "Descrizione YouTube",
          "tags": ["keyword uno", "keyword due"],
          "privacy": "unlisted"
        }
      }],
      "video_path": "/srv/pipelinegen/output/final.mp4"
    }
  }' \
  http://127.0.0.1:8000/api/jobs
```

M2M surface (`/api/v1/jobs`) uses the M2M bearer token and is allowed only when
the type is present in the automation catalog. `youtube.upload` is advertised
when the publishing credentials are configured and the handler is registered.

## Payload

| Field | Required | Meaning |
|---|---|---|
| `publishing_target.workspace_id` | with target | InstaEdit workspace; must match `INSTAEDIT_PUBLISHING_WORKSPACE_ID` |
| `publishing_target.type` | with target | Must be `channel`; groups are not supported by this job |
| `publishing_target.destination_id` | with target | Opaque channel id from the catalog (`velox_destination_id`) |
| `platform_account_id` | legacy | InstaEdit channel id; alternative to `publishing_target` |
| `publications[]` | no | At most one publication; metadata overrides the legacy fields below |
| `publications[].publication_id` | no | Echoed in the job result |
| `publications[].output_ref` | no | Accepts `variant_id` / `artifact_role`; the rendered video is the job video |
| `publications[].metadata.title` | yes | YouTube title, max 100 characters |
| `publications[].metadata.description` | no | YouTube description, max 5000 characters |
| `publications[].metadata.tags` | no | YouTube tags, at most 500 characters in total |
| `publications[].metadata.privacy` | no | `private` (default), `unlisted` or `public` |
| `publications[].metadata.made_for_kids` | no | YouTube audience flag, default `false` |
| `video_path` | one of path/url | Local file on the worker host (`.mp4`, `.mov`, `.m4v`) |
| `video_url` | one of path/url | Absolute HTTP(S) video URL downloaded by the worker |
| `title` | legacy | YouTube title when no publication metadata is supplied |
| `description` | no | YouTube description when no publication metadata is supplied |
| `tags` | no | YouTube tags when no publication metadata is supplied |
| `privacy_status` | no | `private` (default), `unlisted` or `public` |
| `made_for_kids` | no | YouTube audience flag when no publication metadata is supplied |

`publishing_target` and `publications` follow the Velox publishing envelope.
The canonical fields win over their legacy counterparts when both are present.
Only one publication per job is accepted because InstaEdit publishes one video
to one channel target per post.

`video_path` is resolved inside the process that executes the job, so it must be
readable on that worker. Use `video_url` when the producer and the worker do not
share a filesystem.

## What the job does

1. Reads `GET /api/v1/publishing/targets` and resolves `destination_id`
   against `velox_destination_id` / `external_destination_id`; when both the
   destination and `platform_account_id` are supplied they must match. The
   workspace must equal the configured one.
2. Hashes the file, then calls `POST /api/v1/media/presign`.
3. Uploads to the presigned URL with `PUT`.
4. Calls `POST /api/v1/media/{asset_id}/complete`.
5. Creates the post with `POST /api/v1/posts` (`status: "queued"`), passing the
   job id as `Idempotency-Key`.
6. Polls `GET /api/v1/post-targets/{id}` until the target is `published` or
   fails, then returns the YouTube video id.

The InstaEdit web client creates immediate YouTube posts as **private**; this
job preserves that default. Set `publications[].metadata.privacy` (or the
legacy `privacy_status`) explicitly if a different visibility is required.

## Result

The job result contains:

```json
{
  "ok": true,
  "job_type": "youtube.upload",
  "post_id": 77,
  "target_id": 88,
  "video_id": "dQw4w9WgXcQ",
  "video_url": "https://youtu.be/dQw4w9WgXcQ",
  "status": "published",
  "privacy_status": "private",
  "workspace_id": 5,
  "destination_id": "instaedit_extdst_...",
  "publication_id": "main",
  "channel_id": "UC...",
  "channel_name": "Channel name",
  "platform_account_id": 9
}
```
