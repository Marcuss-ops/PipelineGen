"""CLI entry point for the semantic tagger.

`python3 -m scripts.bridges.semantic_tagger --prompt "..."` prints the
orchestrator's payload as JSON to stdout. Anything written to stderr from
this script (call_ollama fallback warnings, etc.) is harmless; only stdout
is parsed by the Go subprocess caller in
`internal/media/semantic/tagger.go`.
"""

import argparse
import json
import sys

from .orchestrator import build_payload


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Semantic tagger for generated and retrieved assets"
    )
    parser.add_argument("--prompt", required=True, help="Original generation prompt or query")
    parser.add_argument("--style", default="", help="Generation style or subject context")
    parser.add_argument("--media-type", default="image", help="image, video, audio, or voiceover")
    parser.add_argument("--generator", default="unknown")
    parser.add_argument("--taxonomy", default="config/semantic_taxonomy.yaml")
    parser.add_argument("--ollama-url", default="", help="Ollama base URL")
    parser.add_argument("--ollama-model", default="", help="Ollama model for enrichment")
    parser.add_argument("--language", default="en", help="Source language code (ISO 639-1)")
    parser.add_argument("--translate-to", default="", help="Comma-separated target languages")

    # Retrieval / provenance parameters
    parser.add_argument("--source-type", default="generated",
                        choices=["generated", "retrieved"])
    parser.add_argument("--retriever", default="")
    parser.add_argument("--page-url", default="")
    parser.add_argument("--image-url", default="")
    parser.add_argument("--license", default="")
    parser.add_argument("--author", default="")

    # VLM visual analysis parameters
    parser.add_argument("--vlm-image-url", default="",
                        help="Image URL or base64 data URI for VLM visual analysis")
    parser.add_argument("--vlm-endpoint", default="http://127.0.0.1:8000",
                        help="VLM service endpoint URL")

    args = parser.parse_args(argv)
    result = build_payload(args)
    print(json.dumps(result, indent=2, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    sys.exit(main())
