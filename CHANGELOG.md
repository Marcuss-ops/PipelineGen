# PipelineGen — CHANGELOG

Per godlike/07 (docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md), this
CHANGELOG records every user-visible API and behavior change. Each entry
cross-references the architecture/deprecations.yaml record (if any) and
the canonical ARCHITECTURE.md section that owns the change.

---

## Unreleased

### Deprecated

**[Deprecation, PR-VO-C1]** Unified `/api/voiceover/generate-with-group`
into the canonical `/api/voiceover/generate` endpoint. New callers MUST
send `destination: {kind: "group", group: "<topic>"}` on `/generate`;
the legacy `/generate-with-group` endpoint is preserved for 90 days as
a deprecated forwarder.

  - **Sunset date:** `Sat, 26 Sep 2026 00:00:00 GMT` (RFC 8594 IMF-fixdate)
  - **Deprecation header:** `Deprecation: true` (RFC 9745 draft standard)
  - **Successor pointer:** `Link: <...>; rel="successor-version"` (RFC 8288)
  - **Migration:** see docs/voiceover/p0-bundle-A1-A6.md §"Deprecation
    contract (90-day Sunset, RFC 8594)"
  - **Deprecation record:** `architecture/deprecations.yaml#PR-VO-C1`
  - **Body unchanged:** the legacy endpoint returns a 100% identical
    payload during the deprecation window — existing clients are
    unimpacted other than the new response headers.

  **Old call (legacy, kept alive until 2026-09-26):**
  ```bash
  curl -X POST http://127.0.0.1:8080/api/voiceover/generate-with-group \
       -d '{"text":"hello world","language":"en","voiceover_group":"boxe"}'
  # 200 + payload + Deprecation/Sunset/Link headers
  ```

  **New call (canonical, recommended):**
  ```bash
  curl -X POST http://127.0.0.1:8080/api/voiceover/generate \
       -d '{"text":"hello world","language":"en",
            "destination":{"kind":"group","group":"boxe"}}'
  # 200 + same payload; no deprecation headers
  ```

### Added

**[PR-VO-C1]** New `DestinationRequest.Kind` field (string; values: `""`,
`"group"`, `"explicit"`). Drives routing strategy at handler boundary:

  - `kind: "group"` — GroupsResolver dispatches `Group → FolderID`
    at request time, stamp the resolved folder back onto the
    destination so downstream service code sees only populated
    `FolderID`.
  - `kind: "explicit"` — caller-supplied `FolderID` is used verbatim
    (no resolver call).
  - `kind: ""` (default) — legacy auto-detect: `FolderID > Group >
    config-level voiceover folder`.

  The handler at `internal/api/assets/voiceover/handler.go::Generate`
  enforces fail-fast semantics on `kind: "group"` + empty `Group`
  (hard 400 at handler boundary) per godlike/07 §"No fake availability".

### Observability

**[PR-VO-C1]** New Prometheus counter `legacy_voiceover_route_invocations_total`
labelled by `route`. Operators expose this via `/metrics` to track
per-route usage during the 90-day sunset window. The companion helper
`LegacyVoiceoverDeprecationCount()` returns the cumulative invocation
count (dto.Metric writeback pattern) for admin/diagnostic surfaces.

---

## Earlier (June 2026 wave)

See ARCHITECTURE.md §"Migration Status (Brutal Care Plan)" for the
historical record. Cross-references:

- **PR-VO-A1** through **PR-VO-A6** — voiceover P0 hardening bundle.
- **PR-VO-B1** — Drive upload split Processor ↔ Lifecycle
  (DriveUploaderPort).
- **PR-VO-B2** — metadata + StyleGroup propagation (no silent-drop
  through `processLanguage` + `resolveDestination`).
- **PR-VO-B3** — sync dedupe by `drive_file_id` + BCP-47 / compact
  locale parser (`pkg/localeutil/locale.go`).
