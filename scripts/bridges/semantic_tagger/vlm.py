"""VLM (Vision-Language Model) visual analysis.

Called at INGEST time when an image URL is supplied; results are stored
alongside the rest of the metadata envelope. The default VLM endpoint
points at the FastAPI VLM service hosted next to embedding_server, but
callers can override with --vlm-endpoint.
"""

import sys

from . import VLM_AVAILABLE

VLM_VISUAL_TAG_PROMPT = """\
Analyze this image and return ONLY a JSON object with these keys:
- "visual_objects": list of physical objects/elements visible (5-10 items)
- "scene_type": one of "talking_head", "interview", "podcast", "b_roll", "landscape", "urban", "studio", "outdoor", "indoor", "close_up", "wide_shot", "screen_recording", "other"
- "mood": list of 3-5 emotional/atmospheric descriptors
- "text_on_screen": list of any visible text (logos, titles, subtitles) or empty list
- "dominant_colors": list of 3-5 hex color codes
- "composition": one of "centered", "rule_of_thirds", "symmetrical", "asymmetrical", "leading_lines"
- "lighting": one of "natural", "studio", "dramatic", "soft", "backlit", "neon"
No explanation, no markdown, only the JSON."""


def call_vlm_visual(image_url, vlm_endpoint="http://127.0.0.1:8000"):
    """Call VLM endpoint for structured visual analysis of an image frame.

    Returns the canonical empty dict on any failure (VLM unavailable,
    endpoint unreachable, malformed JSON). Non-fatal by design — the rest
    of the pipeline still runs.
    """
    empty = {
        "scene_type": "other",
        "visual_objects": [],
        "mood": [],
        "text_on_screen": [],
        "dominant_colors": [],
        "composition": "",
        "lighting": "",
        "raw_description": "",
    }
    if not VLM_AVAILABLE or not image_url:
        return empty

    try:
        import httpx
        resp = httpx.post(
            f"{vlm_endpoint.rstrip('/')}/vlm/visual-tag",
            params={"image_url": image_url, "prompt": VLM_VISUAL_TAG_PROMPT},
            timeout=30.0,
        )
        if resp.status_code != 200:
            print(f"[semantic_tagger] VLM visual-tag failed: {resp.status_code}", file=sys.stderr)
            return empty

        data = resp.json()
        tags = data.get("tags", {})

        if tags.get("parse_error"):
            return {**empty, "raw_description": tags.get("raw_response", "")}

        return {
            "scene_type": str(tags.get("scene_type", "other")),
            "visual_objects": [str(t) for t in tags.get("visual_objects", []) if t],
            "mood": [str(t) for t in tags.get("mood", []) if t],
            "text_on_screen": [str(t) for t in tags.get("text_on_screen", []) if t],
            "dominant_colors": [str(t) for t in tags.get("dominant_colors", []) if t],
            "composition": str(tags.get("composition", "")),
            "lighting": str(tags.get("lighting", "")),
            "raw_description": "",
        }
    except Exception as e:
        print(f"[semantic_tagger] VLM visual analysis failed (non-fatal): {e}", file=sys.stderr)
        return empty
