# PR-W6-SMOKE-DOC — Wave 6 Group C forward-pointer (operational smokes)

**Canonical surface** (godlike/06 SSOT, one canonical owner per fact): this
document is the SOLE source of truth for WHY the legacy operational smokes
stay byte-stable after Wave 6 Group B, AND the reservation contract for the
forward-pointer envs that Wave 7 will use to deliver a `kind: semantic`
smoke variant.

**Cross-references** (godlike/06 3-surface lockstep):
- `architecture/current.yaml#WAVE-6-REGISTER-LOCATION-MIGRATION.linked_issues[PR-W6-SMOKE-DOC]` (this doc's wave-tracker slot, status: pending → shipped via doc-delivery)
- `architecture/current.yaml#FASE-2.1-VOICE-FREEZE.linked_issues` (legacy freeze contract until 2026-12-31)
- `AGENTS.md ## Recent cross-cutting closures` (Wave 6 Group C mirror)
- `CHANGELOG.md ## Unreleased` (Wave 6 Group C mirror)

---

## §1 Scope

Document the canonical decision to NOT migrate the legacy operational smokes
(per `tests/operational/voiceover_c2_legacy_fallback_smoke.sh` and similar)
to the new `AssetLocationInput` DTO during Wave 6. Reserve the canonical envs
(`SMOKE_DRIVE_PROJECT_ID` + `SMOKE_DRIVE_LANGUAGE`) for the future
`kind: semantic` smoke variant to be delivered in Wave 7 (post
`PR-RESOLVER-PORT-EXTRACT` — the LocationResolver Pattern 0 port).

## §2 Why the legacy smokes stay byte-stable

The legacy operational smokes test the **legacy contract**:

```yaml
destination:
  kind: "explicit"
  folder_id: "1B8b...legacy_folder_id_hex"
```

Mutating these smokes during Wave 6 would:

1. **Break the legacy-client safety net.** `FASE-2.1-VOICE-FREEZE` (deadline
   2026-12-31) explicitly protects the `destination: {kind: "explicit"}` wire
   shape because the canonical retirement constraint is BOTH:
   - `rate(legacy_generate_from_clips_total[7d]) == 0 AND rate(legacy_generate_with_images_total[7d]) == 0`,
   - hard deadline 2026-12-31.

   Mutating the smoke during Wave 6 would observably prove these counters
   wrong on the 7-day rolling window — a future agent reading the dashboard
   would conclude the legacy contract is dead and proceed to physical
   `git rm` of `handler_legacy_*.go`, breaking in-flight legacy clients.

2. **Pollute the operator logs.** The legacy smokes intentionally probe the
   `BADVENDOR`+`FALLBACK_OPEN` paths; replacing them with kind: semantic
   probe signatures would silently shadow real failure modes the operator
   dashboard watches.

3. **Violate godlike/07 minimum-blast-radius.** Per the project's
   carry-forward convention, waves ship additive behavior changes.
   Mutating legacy smokes in a forward-migration wave is an orthogonal
   surface change that deserves its own slim-shape wave entry.

## §3 Env reservations for the Wave 7 `kind: semantic` variant

The following envs are RESERVED for the future `kind: semantic` smoke
implementation that ships in Wave 7:

- **`SMOKE_DRIVE_PROJECT_ID`** — canonical ProjectID slot for the
  `AssetLocationInput.Semantic.ProjectID` field. The future smoke reads this
  env at startup, embeds it in the canonical `AssetLocationInput` DTO, and
  asserts the canonical `LocationResolver` resolves it to a known
  `FolderID` without reverting to the legacy `folder_id` field.

- **`SMOKE_DRIVE_LANGUAGE`** — canonical Language slot for the
  `AssetLocationInput.Semantic.Language` field. Same resolution contract
  as above.

These envs MUST NOT be referenced by legacy smokes (which use
`destination: {kind: "explicit"}` with hard-coded folder IDs). Introducing
the reserved envs into a legacy smoke would conflate the two contracts and
hide bit-rot.

## §4 Wave 7 migration recipe (canonical surface contract)

**Wave 7 ships `PR-RESOLVER-PORT-EXTRACT` (deadline 2026-08-15) FIRST**, then
the canonical `kind: semantic` smoke implementation (forward-pointer, no
deadline yet per godlike/07 honest-limitation: the resolver port is the
gating dependency; until it ships, the smoke cannot resolve a
`ProjectID+Language` to a `FolderID` deterministically).

Once the resolver port lands, the canonical recipe for the new smoke is:

1. Write a parallel set of hermetic shell ops (`tests/operational/semantic_*.sh`)
   that exercise `kind: semantic` via `AssetLocationInput`. The canonical
   request shape is:

   ```json
   {
     "location": {
       "kind": "semantic",
       "project_id": "$SMOKE_DRIVE_PROJECT_ID",
       "language": "$SMOKE_DRIVE_LANGUAGE"
     },
     "url": "https://www.youtube.com/watch?v=..."
   }
   ```

2. The smoke path: `POST /api/media/register-batch` with the canonical
   request above + assert that:
   - **HTTP 202 ACCEPTED** (composition-root wired correctly per
     `PR-DRIVE-AVAILABILITY-GATE-CONSUMER-MIRROR`),
   - **`media_assets.drive_folder_id` resolves** to the expected
     `ProjectID+Language` mapping,
   - **`media_assets.lifecycle_state = ACTIVE`** per the canonical indexer
     state machine,
   - **`qdrant_search_returns_asset`** (the indexer wrote through Qdrant).

3. **REPLACE (NOT mutate) the legacy smokes.** Legacy smokes stay
   byte-stable until the 7-day `rate(legacy_*) == 0` trigger OR 2026-12-31
   freeze expiration, whichever comes first. New `semantic_*` smokes live
   alongside as a parallel rail.

## §5 Cross-references (godlike/06 umbrella)

- **`architecture/current.yaml#WAVE-6-REGISTER-LOCATION-MIGRATION`** —
  umbrella wave-tracker entry; this PR-W6-SMOKE-DOC slot is one of the 7
  linked_issues.
- **`architecture/current.yaml#WAVE-6-REGISTER-LOCATION-MIGRATION.linked_issues[PR-RESOLVER-PORT-EXTRACT]`** —
  the Wave 7 gating dependency (resolver Pattern 0 port). When this lands,
  the `kind: semantic` smoke implementation can proceed.
- **`architecture/current.yaml#WAVE-6-REGISTER-LOCATION-MIGRATION.linked_issues[PR-DRIVE-AVAILABILITY-GATE-CONSUMER-MIRROR]`** —
  the Wave 7 composition-root dependency that mirrors
  `PR-DRIVE-AVAILABILITY-GATE` to fail-closed 503 when Location resolves
  but `*drive.Uploader.Service` is nil.
- **`architecture/current.yaml#FASE-2.1-VOICE-FREEZE`** — legacy
  contract freeze. Protects the destination: {kind: "explicit"} smokes
  until 2026-12-31.
- **`architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-06`** —
  6-item voiceover + app build-issue carry-forward. NOT a regression of
  this commit.
- **`AGENTS.md ## Recent cross-cutting closures`** — Wave 6 Group C
  mirror entry (lands during the bookkeeping commit).
- **`CHANGELOG.md ## Unreleased`** — Wave 6 Group C mirror entry (lands
  with this commit).

## §6 Pre-flight invariants for the future `kind: semantic` smoke

The future smoke MUST satisfy these conditions before its first `bash -n`
acceptance:

1. The `SMOKE_DRIVE_PROJECT_ID` + `SMOKE_DRIVE_LANGUAGE` envs are present
   in the operator's `docker-compose.yml` (or equivalent environment
   provisioning) — placeholder values are acceptable for testing the
   smoke's harness, but production runs MUST use real Drive folder IDs.

2. The Wave 7 resolver port (`PR-RESOLVER-PORT-EXTRACT`) is shipped +
   the composition root wires the canonical concrete (per AGENTS.md
   Pattern 0). The smoke fails-closed at composition time per
   `godlike/07 NO-FAKE-AVAILABILITY` if the resolver returns
   `ErrLocationResolverNotWired`.

3. The `PR-DRIVE-AVAILABILITY-GATE-CONSUMER-MIRROR` is shipped (today
   only fires on FolderID traffic). The smoke MUST probe the gate
   behavior (HTTP 503 surfaced when Location is set without the gate
   wired).

## §7 Honest scope-lock (godlike/07 minimum-blast-radius)

This document ships an audit-pin + future-recipe + env reservation. **Zero
production code change today. Zero operator-facing dashboard change today.
The legacy smokes remain byte-stable until 2026-12-31.** The future
`semantic_*` smokes live as a parallel rail; they never REPLACE legacy
smokes until the 7-day counter or 2026-12-31 trigger fires.

**Pre-existing 6-item voiceover + app build-issue carry-forward unchanged**
per `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-06` —
NOT regressions of this commit.

Direct-to-main per AGENTS.md Git-Lesson-2 (no branches, no `--no-ff`,
no `--force`). Co-authored-by trailer preserved per AGENTS.md Git-Lesson-3.
