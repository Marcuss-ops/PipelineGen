# Google Accounting Automation - GEMINI.md

## Overview
This module handles browser-based automation for Google services, specifically:
- **Google Labs ImageFX Flow**: AI Image generation.
- **Google Vids**: Video generation, AI Avatars (Talking Heads), and Character-to-Video.

## Image Generation (Google Flow)

### Key Components
- **Engine**: `google-accounting/automation/flow/engine.py` orchestrates the UI interaction (navigation, prompt entry, x4 setting).
- **Capturer**: `google-accounting/automation/flow/capture.py` handles image retrieval.

### Capture Strategy (Dual Mode)
1. **Network Listener**: Monitors `batchGenerateImages` responses and captures image streams directly from HTTP traffic.
2. **DOM Polling**: Scans the page for `<img>` tags.
    - **Fix (May 2026)**: Added support for relative URLs (e.g., `/fx/api/...`) by converting them to absolute `https://labs.google/...`.
    - **Fix (May 2026)**: Added support for `media.getMediaUrlRedirect` patterns. It now follows redirects (up to 10) to find the final image resource.

### Configuration
Selectors and constants are centralized in `google-accounting/automation/flow/config.py`.

### Testing
Use `google-accounting/test_monkey.py` to verify image generation:
```bash
python google-accounting/test_monkey.py
```

## Video Generation (Google Vids)
Handled via `google-accounting/automation/vids.py`. Supports:
- Full AI video generation from prompt.
- Lip-sync avatars from script.
- Character-consistent video from image reference.

## Operational Notes
- **Sessions**: Auth states are stored in `google-accounting/sessions/`.
- **Headless Mode**: Supported and recommended for production.
- **Health Check**: Always verify the server is up via `GET /health` before sending jobs.
