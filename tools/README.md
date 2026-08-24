# Repository tools

This directory is currently empty and reserved for developer and verification
tools. The previously documented tools (`golden06`, `overlaytimings`,
`researchlive`) have been removed; their functionality now lives under
`cmd/` entrypoints and `scripts/` bridges.

Runtime entrypoints remain under `cmd/server`, `cmd/worker`, and `cmd/admin`.
Add new tools here only if they are genuinely reusable developer utilities;
otherwise prefer `scripts/` for operational bridges.
