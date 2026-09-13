#!/usr/bin/env python3
"""Report emitter for scripts/bench/generate-video.sh.

This program was extracted verbatim from the single ``python3 - <<'PYEOF'``
heredoc that used to occupy the Stage-4 tail of generate-video.sh, so that no
single source file exceeds 400 lines. The parts are sequential fragments of
one top-to-bottom program: this entry executes them in order inside a single
module namespace (identical to the original heredoc execution), preserving
module-level state, function definitions and sys.argv exactly.
"""
import os

_HERE = os.path.dirname(os.path.abspath(__file__))
_PARTS = (
    "generate_video_report_part1.py",
    "generate_video_report_part2.py",
    "generate_video_report_part3.py",
    "generate_video_report_part4.py",
    "generate_video_report_part5.py",
)

for _part in _PARTS:
    with open(os.path.join(_HERE, _part), encoding="utf-8") as _f:
        exec(compile(_f.read(), _part, "exec"))
