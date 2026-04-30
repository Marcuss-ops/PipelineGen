# Script Docs Guide

This document explains, end to end, how to create a script document through the API and make it appear in Google Docs.

## 1. What This Endpoint Does

`POST /api/script-docs/generate` builds a modular script document from a JSON payload, generates the text with Ollama, assembles timeline and entity sections, saves the record in the local database, and publishes the final content to Google Docs.

If Google Docs credentials are missing or invalid, the request must fail. Do not treat the request as successful if the document was not published.

## 2. Required Prerequisites

Before calling the endpoint, make sure the server has:

1. A running Ollama backend.
2. Google OAuth files:
   - `credentials.json`
   - `token.json`
3. The server restarted after those files were added or moved.

If the files are not in the current working directory, the config resolver also checks the common local development paths used in this repo, including the repo root and `~/Downloads`.

## 3. Authentication

The script-doc routes are protected when auth is enabled.

Use one of these headers:

```http
X-Velox-Admin-Token: <admin_token>
```

or

```http
Authorization: Bearer <token>
```

If `VELOX_ENABLE_AUTH=false`, the endpoint is accessible without auth.

## 4. Endpoint

### Generate and publish

```http
POST /api/script-docs/generate
```

Use this route when you want the final document created in Google Docs.

### Preview

```http
POST /api/script-docs/preview
```

This route uses the same generation pipeline and also writes a local preview file. In the current codebase it still runs the same document creation step, so it is not a pure local-only path.

## 5. Request Body

Minimal payload:

```json
{
  "topic": "Amish"
}
```

Full example:

```json
{
  "topic": "Amish",
  "duration": 120,
  "language": "it",
  "template": "documentary",
  "source_text": "Racconta la storia, lo stile di vita e i valori della comunita Amish.",
  "voiceover": false,
  "preview_only": false
}
```

### Fields

| Field | Required | Default | Notes |
|---|---:|---:|---|
| `topic` | yes | - | Main topic of the script and document title. |
| `duration` | no | `60` | Duration in seconds. |
| `language` | no | `it` | Language code passed to the prompt and voiceover. |
| `template` | no | `documentary` | Used as the tone/style input. |
| `source_text` | no | `topic` | Extra source material for the LLM. |
| `voiceover` | no | `false` | If true, the voiceover service is called. |
| `preview_only` | no | `false` | Present in the payload, but the route behavior is driven by the endpoint you call. |

## 6. Execution Flow

When the request arrives, the server does this:

1. Validates and normalizes the payload.
2. Builds the script document with Ollama.
3. Cleans the generated text.
4. Creates the timeline plan.
5. Extracts narrative entities.
6. Saves the script in the local SQLite database if the repository is available.
7. Creates the Google Doc.
8. Returns the document ID and URL to the client.

If `voiceover=true`, the server also tries to generate a voiceover file after the document is built.

## 7. Expected Success Response

Example:

```json
{
  "ok": true,
  "doc_id": "14ptJuDwarbAON0VJCYHo7-dE7Rb9wSQPSC8CHxq6yJE",
  "doc_url": "https://docs.google.com/document/d/14ptJuDwarbAON0VJCYHo7-dE7Rb9wSQPSC8CHxq6yJE/edit",
  "docs_url": "https://docs.google.com/document/d/14ptJuDwarbAON0VJCYHo7-dE7Rb9wSQPSC8CHxq6yJE/edit",
  "title": "Amish",
  "full_content": "...",
  "timeline": {
    "primary_focus": "Amish",
    "segment_count": 6,
    "total_duration": 120,
    "segments": []
  },
  "voiceover": null
}
```

## 8. What To Check If The Doc Does Not Appear

1. Confirm the server log does not show `google docs client not initialized`.
2. Confirm `credentials.json` and `token.json` exist in one of the supported locations.
3. Restart the server after updating those files.
4. Check the HTTP response:
   - `doc_id` should not be empty.
   - `doc_url` should contain a `docs.google.com` link.
5. If the response is `503`, the Docs client was not ready.
6. If the response is `500`, the Google API call failed during creation.

## 9. Example Curl

```bash
curl -X POST http://127.0.0.1:8080/api/script-docs/generate \
  -H 'Content-Type: application/json' \
  -H 'X-Velox-Admin-Token: <token>' \
  -d '{
    "topic": "Amish",
    "duration": 120,
    "language": "it",
    "template": "documentary",
    "voiceover": false
  }'
```

## 10. Practical Notes

1. `topic` is the most important field. Without it, validation fails.
2. `source_text` improves quality when the topic is broad.
3. The Google Doc title is the `topic`.
4. The document is published only if the Docs client is initialized correctly.
5. If you need a local-only workflow, the current code does not provide a pure no-publish route.

