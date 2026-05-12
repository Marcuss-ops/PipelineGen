# YouTube Clip Extraction Service

**Status:** ACTIVE - Service documentation

The `youtubeclip` service provides a robust and flexible solution for extracting, processing, and storing video segments from YouTube. This service is designed to be highly configurable, allowing for precise control over the output format, destination, and metadata of the extracted clips.

## Key Features

- **Segment Extraction:** Extracts multiple video segments from a YouTube URL based on start and end timestamps.
- **Video Normalization:** Optionally normalizes videos to a standard format (1080p, 30fps) while preserving the original aspect ratio.
- **Audio Preservation:** Supports keeping the original audio track or removing it as needed.
- **Google Drive Integration:** Automatically uploads extracted clips to a specified Google Drive folder.
- **Automatic Subfolder Creation:** Organizes clips by creating subfolders named after the YouTube video ID, preventing a cluttered root folder.
- **Database Integration:** Saves clip metadata to a dedicated SQLite database for easy tracking and retrieval.

## API Endpoint

The service is exposed through the following API endpoint:

- **POST** `/api/youtube-clips/extract`

### Request Body

```json
{
  "url": "https://www.youtube.com/watch?v=ECXXrQmW9E8",
  "segments": [
    {
      "start": "10:31",
      "end": "11:01",
      "name": "Clip Name"
    }
  ],
  "normalize": false,
  "keep_audio": true,
  "save_db": true,
  "upload_drive": true,
  "destination": {
    "group": "group_name",
    "create_subfolder": true
  }
}
```

- `url` (string, required): The URL of the YouTube video.
- `segments` (array, required): A list of segments to extract.
  - `start` (string, required): The start timestamp of the segment (e.g., "10:31").
  - `end` (string, required): The end timestamp of the segment (e.g., "11:01").
  - `name` (string, optional): A name for the extracted clip.
- `normalize` (boolean, optional, default: `true`): If `true`, the video will be normalized.
- `keep_audio` (boolean, optional, default: `false`): If `true`, the audio will be preserved.
- `save_db` (boolean, optional, default: `true`): If `true`, the clip metadata will be saved to the database.
- `upload_drive` (boolean, optional, default: `true`): If `true`, the clip will be uploaded to Google Drive.
- `destination` (object, optional): Specifies the Google Drive destination.
  - `group` (string, optional): The name of the destination group.
  - `create_subfolder` (boolean, optional, default: `false`): If `true`, a subfolder will be automatically created for the video.

### Response Body

```json
{
  "ok": true,
  "source_url": "https://www.youtube.com/watch?v=ECXXrQmW9E8",
  "items": [
    {
      "name": "clip_name",
      "start": "10:31",
      "end": "11:01",
      "local_path": "data/youtube-clips/20260502_090040/001_clip_name.mp4",
      "drive_link": "https://drive.google.com/file/d/DRIVE_FILE_ID/view?usp=drivesdk",
      "status": "processed",
      "drive_folder_id": "DRIVE_FOLDER_ID"
    }
  ],
  "drive_folder_id": "DRIVE_FOLDER_ID"
}
```

- `ok` (boolean): `true` if the request was successful.
- `items` (array): A list of the processed segments.
  - `drive_link` (string): The direct link to the uploaded clip on Google Drive.
  - `drive_folder_id` (string): The ID of the folder where the clip was uploaded.

## Automatic Subfolder Creation

To maintain a clean and organized folder structure in Google Drive, the service automatically creates a subfolder for each video when `create_subfolder` is set to `true`. The subfolder is named using the YouTube video ID, prefixed with `yt_`.

This ensures that all clips from the same video are grouped together, making them easy to find and manage.
