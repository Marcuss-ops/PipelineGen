# architecture/generated/

Audit artifacts produced by `go run ./cmd/archgen` (Phase 1+) from the
canonical registries declared in
`docs/architecture/godlike/04_REGISTRIES_AND_SSOT.md` §"Generated
manifests" and §"Anti-duplication rule".

These files are checked into the repository for parity tests
(registry ↔ routes ↔ jobs ↔ providers ↔ health) but are NEVER edited
by hand. They are regenerated from the frozen registry snapshot at
composition time.

Expected files (not present until Slice 19.2+ which lands `cmd/archgen`):

- `capabilities.json`  — canonical capability-descriptor inventory
- `routes.json`        — derived HTTP route table (method, path, owner)
- `jobs.json`          — derived job-type ↔ handler table
- `providers.json`     — provider registry snapshot (name ↔ owner)
- `dependencies.json`  — inter-capability dependency edges
- `health.json`        — capability-owned health-check inventory

Tracking: Wave 19, Slice 19.1 EXPAND, 2026-06-26.
