"""Word-boundary capture helpers shared by the sidecar and the CLI.

Owns the ONE canonical Edge-tick → microsecond conversion and the
canonical metadata.jsonl line shape, so the persistent sidecar and the
legacy CLI emit byte-identical metadata. Edge reports timestamps in
ticks at 10,000,000 ticks/second; microseconds are ticks // 10.
"""

import json
import os

_TICKS_PER_MICROSECOND = 10  # 10,000,000 ticks/second → 1,000,000 µs/second.


def boundary_line(chunk: dict) -> str:
    """Normalize an edge-tts WordBoundary chunk into one canonical
    metadata.jsonl line (microseconds, single conversion site).

    Edge offset/duration are JSON numbers (integral ticks); they are
    converted exactly once here — never again downstream.
    """
    start_us = int(chunk["offset"]) // _TICKS_PER_MICROSECOND
    duration_us = int(chunk["duration"]) // _TICKS_PER_MICROSECOND
    return json.dumps(
        {
            "type": "WordBoundary",
            "text": chunk["text"],
            "start_us": start_us,
            "end_us": start_us + duration_us,
        },
        ensure_ascii=False,
    )


def remove_partials(*paths: str) -> None:
    """Best-effort removal of .part artifacts so no half-valid files survive.

    Called on every error path (provider exception, empty audio, zero
    boundaries) — never leave a partial file behind.
    """
    for path in paths:
        try:
            if os.path.exists(path):
                os.remove(path)
        except OSError:
            pass
