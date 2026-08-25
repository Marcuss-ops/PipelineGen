# Repository tools

This directory is reserved for developer utilities. The previously documented
tools (`golden06`, `overlaytimings`, `researchlive`) have been removed; their
functionality now lives under `cmd/` entrypoints and `scripts/` bridges.

- `regen_hotspots.py` — shim forwarding to `scripts/regen_hotspots.py`
  (canonical). Kept for CI backward-compatibility: the `60_check_import_zero`
  gate invokes `python3 tools/regen_hotspots.py --check`, while the
  implementation lives in `scripts/`. Both paths are valid.

Runtime entrypoints remain under `cmd/server`, `cmd/worker`, and `cmd/admin`.
Add new tools here only if they are genuinely reusable developer utilities;
otherwise prefer `scripts/` for operational bridges.
