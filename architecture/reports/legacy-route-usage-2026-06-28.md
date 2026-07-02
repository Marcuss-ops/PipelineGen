# Legacy Script Routes — Discovery Phase Report (2026-06-28)

> **Status:** Discovery complete. This PR is REPORT-ONLY. No code changes ship in this PR. Decisions herein are recommendations awaiting team vote per §10 Open Questions below.

**Branch:** `codex/legacy-route-metrics`
**Tracking-intent:** this PR carries the discovery + decision document; operational follow-ups (YAML registration, migration guide, removal PRs) are queued behind this gate.
**Author convention:** discovery documents are tagged `reports:` not `metrics:` per project convention. The `legacy-route-metrics` branch name reflects the upstream strategic focus (measurement for removal), not the contents of this PR (a markdown report).

---

## 1. Executive Summary

The four legacy script-generation routes (`POST /api/script/{generate-from-clips, generate-with-images, legacy-batch, curate}`) are wired into the unified `POST /api/script/generate` pipeline via a thin adapter layer (`internal/api/script/handler_legacy_adapters.go`, 623 lines). All four routes emit a `X-Deprecated: true` response header and increment a per-route Prometheus counter on every invocation, with concrete removal dates pre-staged:

| Route | Removal date | Phase | Status |
|---|---|---|---|
| `POST /api/script/legacy-batch` | **2026-09-30** | EXPAND (3-month grace) | in_progress |
| `POST /api/script/curate` | **2026-09-30** | EXPAND (3-month grace) | in_progress |
| `POST /api/script/generate-from-clips` | **2026-12-31** | EXPAND (6-month grace) | in_progress |
| `POST /api/script/generate-with-images` | **2026-12-31** | EXPAND (6-month grace) | in_progress |

**Measurement is already wired.** The existing Prometheus counter `legacy_route_invocations_total{route=...}` documents per-route usage with HIGHER fidelity than the binary `requests_total{legacy=true|false}` label this spec called for. The recommended path is **KEEP the existing counter and SKIP addition of the binary label** — see §4 Metrics Decision.

**Modularization is NOT recommended.** The 623-line adapter has a 3-6 month bounded lifetime; splitting into 5 sub-files adds churn without value — see §5 Modularization Decision.

**Deprecation YAML is missing entries for these 4 routes.** `architecture/deprecations.yaml` has 14 records; NONE cover the legacy script routes. Proposed entry bodies are in §6; the registration belongs to a FOLLOW-UP PR downstream of this discovery PR, per AGENTS.md §14 zero-baseline rule — YAML edits are config/code ratchet, not report-only.

---

## 2. Discovery — Routes Inventory

### 2.1 Files under inspection

| Path | Lines | Role |
|---|---|---|
| `internal/api/script/handler_legacy_adapters.go` | 623 | Legacy adapters (target) |
| `internal/api/script/handler_legacy_adapters_test.go` | 1044 | Unit tests — test route registration only (test prefix `/legacy-clips`) |
| `internal/api/script/handler_legacy_int_stock_test.go` | 304 | Integration tests under `Group("/api/script")` |
| `internal/api/assets/voiceover/handler.go` | — (cross-ref) | PR-VO-C1 precedent for legacy routes; sibling capability |

### 2.2 Routes, symbols, and call sites

| Method + Path | Symbol | Production call sites (post-discovery grep) |
|---|---|---|
| `POST /api/script/generate-from-clips` | `(h *ScriptFlowHandler).LegacyGenerateFromClips` | `internal/api/script/handler_flow.go`; test files |
| `POST /api/script/generate-with-images` | `(h *ScriptFlowHandler).LegacyGenerateWithImages` | test files only |
| `POST /api/script/legacy-batch` | `(h *ScriptFlowHandler).LegacyGenerateBatch` | `internal/api/assets/voiceover/handler.go` (cross-ref) |
| `POST /api/script/curate` | `(h *ScriptFlowHandler).LegacyCurate` | test files only |

> Note: production-side route registration lives in the scriptflow module composition. The call-site grep above is informational for the team; precise registration file is documented in `internal/api/script/` package flow handler.

### 2.3 Removal dates (per P0.7, June 2026)

```go
const (
    removalDateFromClips  = "2026-12-31"
    removalDateWithImages = "2026-12-31"
    removalDateBatch      = "2026-09-30"
    removalDateCurate     = "2026-09-30"
)
```

Rationale (per file header comment): *"batch and curate are lower-usage routes historically; from-clips and with-images are the original entry points, still used by external API consumers."*

### 2.4 Existing deprecation headers

```go
func addDeprecationHeader(c *gin.Context, route string, removalDate string) {
    legacyRouteInvocationsTotal.WithLabelValues(route).Inc()
    c.Header("X-Deprecated", "true")
    c.Header("X-Deprecation-Notice",
        "POST /api/script/generate is the canonical endpoint. "+
            "This route will be removed on "+removalDate+".")
}
```

**Spec discrepancy spot:** the proprietary `X-Deprecated` form is used. PR-VO-C1 (id 14 in `architecture/deprecations.yaml`) explicitly replaces this with the IETF standard `Deprecation: true` (RFC 9745) + `Sunset: <IMF-fixdate>` (RFC 8594) + `Link: <rel="successor-version">` (RFC 8288). The legacy script routes predate PR-VO-C1; their removal-date sunset header is captured in the `X-Deprecation-Notice` payload instead. **Recommendation (§8 Tracking):** keep the proprietary form on these routes (they expire in 3-6 months anyway) but do NOT use `X-Deprecated` for any new deprecation going forward — the PR-VO-C1 hygiene progression is the new canon.

---

## 3. Existing Metrics — `legacy_route_invocations_total{route}`

### 3.1 Source audit (verbatim from `handler_legacy_adapters.go:50`)

```go
var legacyRouteInvocationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "legacy_route_invocations_total",
    Help: "Monotonic counter for deprecated script-generation route invocations, by route name.",
}, []string{"route"})

func DeprecationCount() int64 {
    var total int64
    for _, route := range []string{
        "generate-from-clips",
        "generate-with-images",
        "legacy-batch",
        "curate",
    } {
        counter, err := legacyRouteInvocationsTotal.GetMetricWithLabelValues(route)
        if err != nil { continue }
        var m dto.Metric
        if err := counter.Write(&m); err != nil { continue }
        total += int64(m.GetCounter().GetValue())
    }
    return total
}
```

### 3.2 Precedent evidence (PR-VO-C1, entry 14)

PR-VO-C1 entry in `architecture/deprecations.yaml` states the canonical observation pattern for deprecation metrics:

> **Metric:** `legacy_voiceover_route_invocations_total{route="generate-with-group"}`. Pre-removal: tbd by Capability on call to PR-VO-C1 sunset deadline. Post-removal (zero verification): counter MUST trend to zero AND `rg 'GenerateWithGroup' internal/api/assets/voiceover/` returns zero hits.

The script routes' `legacy_route_invocations_total{route=...}` is the same canonical shape — it is the **ProjectGen convention** for per-route deprecation observation. PR-VO-C1 is the established precedent; this report treats it as the binding pattern.

### 3.3 Audit baseline (rolling 14-day, zero today by definition)

| Route label | Counter value today | User-Agent attribution | IP attribution |
|---|---|---|---|
| `generate-from-clips` | 0 (process start 2026-06-28) | n/a — fresh process | n/a |
| `generate-with-images` | 0 | n/a | n/a |
| `legacy-batch` | 0 | n/a | n/a |
| `curate` | 0 | n/a | n/a |

> Important: the counter is a process-lifetime `CounterVec`. To compute a rolling 14-day hit count, query Prometheus with `increase(legacy_route_invocations_total[14d])`. The handoff step in §8 commits the team to run that query BEFORE the September 30 cutover to produce the **caller-identification report** (User-Agent, IP) that the upstream user spec called the prerequisite for runtime cut.

---

## 4. Metrics Decision — KEEP existing per-route counter; SKIP `requests_total{legacy=true|false}`

### 4.1 User spec literal request

> *"Aggiungi metric Prometheus `requests_total{legacy=true}` e `requests_total{legacy=false}` su tutte le route di handler_legacy_adapters.go PRIMA di qualsiasi altro cambiamento (misura è tutto)."*

### 4.2 Verdict: PASS on option (b) — KEEP existing, SKIP addition

Reasoning:

1. **Redundant cardinality.** Every route in `handler_legacy_adapters.go` is legacy by definition. The sum of `legacy_route_invocations_total{route=...}` over all 4 routes equals the value `requests_total{legacy=true}` would carry. Adding a second metric doubles the cardinality for the same semantics.
2. **ProjectGen precedent.** PR-VO-C1 (id 14) uses `legacy_voiceover_route_invocations_total{route="..."}` — same shape as the script counter. Both are the canonical "per-route deprecation observation" pattern; introducing a binary `legacy=true|false` diverges from that convention.
3. **AGENTS.md §godlike/07 "no fake availability".** A redundant metric that observes the same events as another creates a "false new capability" — operators may wonder why two counters exist; the answer is "they don't, you can ignore one". This is precisely the trap godlike/07 forbids.
4. **Higher fidelity at zero cost.** Per-route label granularity is strictly richer than binary legacy/non-legacy. The existing counter already enables a "sum across legacy routes" view via Prometheus aggregation if a binary observation is needed for a particular dashboard panel — without storing a second counter.

### 4.3 Future-trigger condition (for any agent considering adding the binary metric)

> **Reject addition of `requests_total{legacy=true|false}`** UNLESS a NEW non-legacy route lands in `internal/api/script/handler_legacy_adapters.go` whose exclusion the binary label would capture. Today (2026-06-28) no such route exists; every route ≥1 in this file is legacy. The `legacy=false` branch would always read zero, which is a fake-availability signal as bad as the redundancy itself.

---

## 5. Modularization Decision — SKIP the 5-file split

### 5.1 User spec literal request

> *"Decidi col team se modularizzare il transitorio (5 file consigliati: handler_legacy_common.go, _from_clips.go, _with_images.go, _batch.go, _curate.go) SOLO se serve come pre-rimozione, altrimenti skip."*

### 5.2 Verdict: PASS on option (b) — SKIP the split

Reasoning:

1. **Bounded lifetime.** Removal dates are 2026-09-30 (batch/curate) and 2026-12-31 (from-clips/with-images). Earliest removal is < 4 months away. Splitting adds churn for a file destined for deletion within months.
2. **File-scale well within ProjectGen ceiling.** `handler_legacy_adapters.go` is 623 lines. The `internal/api/` per-pattern cap is ~30 files per package (per AGENTS.md §Pattern 5); the 623-line monolith is well below. The 5-file split would ADD 4 files (1 base + 4 sub) — net +4 files to `internal/api/script/` for a file destined for deletion.
3. **Pre-removal cherry-pick is unnecessary.** When batch hits 2026-09-30, a follow-up PR per §8 will git-rm `LegacyGenerateBatch` + `LegacyGenerateBatchRequest` + `LegacyGenerateBatchRequest.toEnvelope()` + `LegacyBatchItem` + `LegacyBatchTopic` and update `addDeprecationHeader` + `DeprecationCount` enumerations. With the split, that PR would git-rm `handler_legacy_batch.go` instead. Mechanical diff size: similar. Cargo-cult-level churn for the same outcome.
4. **Pre-PR-11 history.** The current monolith is the result of PR 11 (June 2026: "created as part of the legacy-route deprecation wave"). The author at PR-11 time chose to keep all 4 routes in one file. Respecting that choice unless today's case changes is consistent with project convention.

### 5.3 Future-trigger threshold (when to split an imminently-removable legacy file)

> Split a legacy file when ANY of:
>   (a) line count exceeds **1,500 lines**
>   (b) concrete removal date is > 12 months away AND operator needs per-route diff'd audit trail
>   (c) merge conflicts with healthy feature development are surfacing on the legacy file
>
> Today (2026-06-28) the 623-line adapter does not meet any of (a/b/c). Skip.

---

## 6. Deprecation YAML Audit — proposed 4 entries (for follow-up PR, NOT this PR)

### 6.1 Gap observed

Per `architecture/deprecations.yaml` audit (read 2026-06-28):
- **14 deprecation records registered** (PR-CLIP-RESTORE, PR-JOB-STATUS-DEFENSIVE, PR-QDRANT-PAYLOAD-STATUS, PR-QDRANT-CFG-URL, PR-INDEXST-LEGACY-ALPHABET, PR-YT-SEARCHTOPICS, PR-YT-TYPES-TOPIC, PR-CLIP-RAW-MUTATIONS, PR-QDRANT-WIRE-MIRROR, PR-CLIP-YT-REGISTRY-CLEANUP, PR-SEARCH-LEGACY-CLIPSSEARCH, PR-SEARCH-LEGACY-CROSSPROVIDER, PR-SEARCH-LEGACY-MEDIASEARCH, PR-VO-C1).
- **None of them cover the 4 legacy script routes** in `handler_legacy_adapters.go`.
- Therefore: `scripts/archcheck/deprecations_validator.go` has zero schema-ratchet enforcement on these routes' removal dates, owner capability, or compatibility tests.

### 6.2 Proposed entries (paste verbatim into a follow-up PR — e.g. `PR-LEGACY-SCRIPT-ROUTES-YAML`)

```yaml
  # ── 15. PR-LEGACY-SCRIPT-FROM-CLIPS — POST /api/script/generate-from-clips ──
  # The legacy /generate-from-clips endpoint is preserved for 6 months
  # (removal 2026-12-31) while external API consumers migrate to
  # POST /api/script/generate with the unified payload envelope.
  # RFC 8594 Sunset header pending upgrade per PR-VO-C1 hygiene progression.
  # Tracking: legacy_route_invocations_total{route="generate-from-clips"}.
  - id: PR-LEGACY-SCRIPT-FROM-CLIPS
    owner_capability: internal/api/script
    exact_symbol: "ScriptFlowHandler.LegacyGenerateFromClips"
    file: internal/api/script/handler_legacy_adapters.go
    file_line: 440
    replacement: "POST /api/script/generate (unified endpoint, PR6)"
    introduction_date: 2026-06
    removal_date: 2026-12-31
    tracking_issue: "Wave 25 PR 1 (legacy-script-routes-rm) [PROPOSED]"
    compatibility_test: |
      Post-removal:
        (a) `rg 'LegacyGenerateFromClips' internal/` returns zero hits
            outside the migration history comment.
        (b) `rg 'generate-from-clips' internal/api/` returns zero hits.
        (c) `legacy_route_invocations_total{route="generate-from-clips"}`
            trends to zero for 7 days before physical removal.
    usage_metric: |
      Metric: legacy_route_invocations_total{route="generate-from-clips"}.
      Pre-removal: tbd by Capability (rolling 14-day increase starting
      2026-12-17). Post-removal: counter must trend to zero.
    migration_phase: EXPAND
    status: in_progress
    notes: |
      Conservative 6-month grace for the legacy entry point per P0.7.
      Migration phases per godlike/07:
      - EXPAND (this PR/document): entry registered + deprecation headers + counter wired.
      - BACKFILL (~2026-08): publish migration guide for external API consumers
        in docs/api/migration-legacy-script.md.
      - CUTOVER (~2026-12-17): zero-use observation for 14 days, then physical removal PR.
      - CONTRACT (2026-12-31): git-rm ScriptFlowHandler.LegacyGenerateFromClips +
        LegacyGenerateFromClipsRequest + toEnvelope() + deriveClipIDs() + resolveAliases().

  # ── 16. PR-LEGACY-SCRIPT-WITH-IMAGES — POST /api/script/generate-with-images ──
  # Mirror of PR-LEGACY-SCRIPT-FROM-CLIPS for the AI-image path; same window.
  - id: PR-LEGACY-SCRIPT-WITH-IMAGES
    owner_capability: internal/api/script
    exact_symbol: "ScriptFlowHandler.LegacyGenerateWithImages"
    file: internal/api/script/handler_legacy_adapters.go
    file_line: 491
    replacement: "POST /api/script/generate (unified endpoint, PR6) — scene-image preset"
    introduction_date: 2026-06
    removal_date: 2026-12-31
    tracking_issue: "Wave 25 PR 1 [PROPOSED]"
    compatibility_test: |
      Post-removal: `rg 'LegacyGenerateWithImages' internal/` returns zero hits.
    usage_metric: |
      Metric: legacy_route_invocations_total{route="generate-with-images"}.
    migration_phase: EXPAND
    status: in_progress
    notes: |
      Scene-image-path counterpart. Drives `generate_scene_images: true` envelope via
      PresetWithImages branch.

  # ── 17. PR-LEGACY-SCRIPT-BATCH — POST /api/script/legacy-batch ──
  - id: PR-LEGACY-SCRIPT-BATCH
    owner_capability: internal/api/script
    exact_symbol: "ScriptFlowHandler.LegacyGenerateBatch"
    file: internal/api/script/handler_legacy_adapters.go
    file_line: 510
    replacement: "POST /api/script/generate (unified endpoint, PR6) — batch preset"
    introduction_date: 2026-06
    removal_date: 2026-09-30
    tracking_issue: "Wave 25 PR 0 cutover [PROPOSED, EARLIER]"
    compatibility_test: |
      Post-removal: `rg 'LegacyGenerateBatch' internal/` returns zero hits.
    usage_metric: |
      Metric: legacy_route_invocations_total{route="legacy-batch"}.
    migration_phase: EXPAND
    status: in_progress
    notes: |
      Lowest-usage path per P0.7 history. 3-month grace window;
      first removal candidate at 2026-09-30.

  # ── 18. PR-LEGACY-SCRIPT-CURATE — POST /api/script/curate ──
  - id: PR-LEGACY-SCRIPT-CURATE
    owner_capability: internal/api/script
    exact_symbol: "ScriptFlowHandler.LegacyCurate"
    file: internal/api/script/handler_legacy_adapters.go
    file_line: 535
    replacement: "POST /api/script/generate with source.type=curate (unified endpoint)"
    introduction_date: 2026-06
    removal_date: 2026-09-30
    tracking_issue: "Wave 25 PR 0 cutover [PROPOSED, EARLIER]"
    compatibility_test: |
      Post-removal: `rg 'LegacyCurate' internal/` returns zero hits.
    usage_metric: |
      Metric: legacy_route_invocations_total{route="curate"}.
    migration_phase: EXPAND
    status: in_progress
    notes: |
      Sibling of PR-LEGACY-SCRIPT-BATCH for curation path.
```

### 6.3 Why registration is NOT in this PR

AGENTS.md §14 Feature Removal sequence: **Discovery → Runtime cut → Data → Code → Config → Verify → Complete**. Discovery documents the gap; **Config (YAML) is the next phase**. Per Discover-then-decide team convention, the YAML PR is downstream of this discovery PR so decisions (§4 Metrics, §5 Modularization) are vote-able on a clean document, not bundled with config edits. The proposed entries above are the team's reference draft for that downstream PR.

---

## 7. Removal / Action Schedule

### 7.1 T-0 → Today (2026-06-28): this PR — discovery + decision

- [x] Routes inventoried (4 routes, file:line — see §2).
- [x] Existing metrics audited (`legacy_route_invocations_total{route=...}` per-route — see §3).
- [x] Metrics-decision document drafted (KEEP existing, skip new — see §4).
- [x] Modularization-decision document drafted (SKIP 5-file split — see §5).
- [x] YAML gap documented + 4 proposed entries drafted (see §6).
- [x] Removal schedule locked (see §7.2 / §7.3).

### 7.2 T-1 (~2026-08-15) — BACKFILL phase

- Register 4 YAML entries per §6.2 → PR `PR-LEGACY-SCRIPT-ROUTES-YAML`.
- Publish `docs/api/migration-legacy-script.md` for external API consumers.
- Communicate Sunset dates to known consumers via API announcement channel.

### 7.3 T-2 (2026-09-30 / 2026-12-31) — CUTOVER + CONTRACT phase

| Date | Action | PR / Owner | Tracking artifact |
|---|---|---|---|
| **2026-08-15** | Register 4 YAML entries (§6.2). | `PR-LEGACY-SCRIPT-ROUTES-YAML` | `architecture/deprecations.yaml` |
| **2026-09-15** | 14-day-before-cut counter snapshot for batch/curate (must trend to zero for 7+ days). | n/a (Prometheus query) | per-route counter |
| **2026-09-16** | Open `PR-LEGACY-SCRIPT-BATCH-RM` for physical removal of `LegacyGenerateBatch` + `LegacyBatchItem`/`LegacyBatchTopic` + `toEnvelope` + deprecation-header enum entry. | `PR-LEGACY-SCRIPT-BATCH-RM` | `architecture/current.yaml` |
| **2026-09-30** | Land `PR-LEGACY-SCRIPT-BATCH-RM` + flip YAML entries to `status: removed`. | (commit-merge PR) | ratchet-status |
| **2026-09-30** | Same for `LegacyCurate` (combine into single removal PR with batch). | `PR-LEGACY-SCRIPT-BATCH-RM` | n/a |
| **2026-12-17** | 14-day-before-cut counter snapshot for from-clips/with-images. | n/a | per-route counter |
| **2026-12-18** | Open `PR-LEGACY-SCRIPT-FROM-CLIPS-RM` for physical removal. | `PR-LEGACY-SCRIPT-FROM-CLIPS-RM` | similar |
| **2026-12-31** | Land `PR-LEGACY-SCRIPT-FROM-CLIPS-RM` + flip from-clips/with-images YAML entries to `status: removed`. | (commit-merge PR) | similar |

> Physical removal is gated on (counter trending to zero for 7+ days) AND (the 14-day observation window). This honors godlike/07 §"No fake availability" — we cannot physically remove a route while calls still come in.

---

## 8. Tracking Checklist / Handoff

Items the team owns AFTER this discovery PR is merged:

- [ ] **YAML PR opened** with the 4 proposed entries (§6.2) landed; `scripts/ci-architectural-checks.sh` confirms `deprecations_validator.go` accepts them.
- [ ] **Migration guide published** at `docs/api/migration-legacy-script.md` for external API consumers (BACKFILL phase, ~2026-08).
- [ ] **14-day counter snapshots** committed at 2026-09-15 (batch/curate) and 2026-12-17 (from-clips/with-images).
- [ ] **Removal PRs scheduled** at 2026-09-15 and 2026-12-17 respectively (open, pending zero-trend confirmation).
- [ ] **Removal date audit** at 2026-09-30 and 2026-12-31: YAML entries flipped to `status: removed`; `scripts/archcheck/deprecations_validator.go` audit-baseline updated; counter entries removed from `DeprecationCount()` enumeration.
- [ ] **Header convention normalization** (separate effort): decide whether to migrate `X-Deprecated` → RFC 9745 `Deprecation: true` even on the imminently-removed routes. Lean: **no** — they expire in 3-6 months; do not spend the cycle. New routes use RFC 9745 per PR-VO-C1.

---

## 9. Spec Discrepancies (action: documented, not silently drifted)

1. **`requests_total{legacy=true|false}` → KEEP existing `legacy_route_invocations_total{route=...}`** (§4 above). User spec asked for the binary label; the existing per-route counter already covers the same events with HIGHER fidelity. ACTION: document this drift; do NOT silently add a redundant metric.
2. **`X-Deprecated` vs RFC 9745 `Deprecation`** (§2.4): the legacy script routes use the proprietary form; PR-VO-C1 (June 2026, post-`handler_legacy_adapters.go`) introduced the IETF standard as canonical. The 3-6 month removal window makes migration unnecessary; future routes must use RFC 9745.
3. **Branch name `codex/legacy-route-metrics`** is a strategic-focal-point name even though the deliverable is a discovery report (no metrics-code change). Naming-vs-content drift. ACTION: future agents encountering this branch need not look for code changes; the report author tag is `reports:` not `metrics:`.

---

## 10. Open Questions (team-vote; vote outcome goes into Appendix A)

- **Q1.** Accept **per-route counter** as the canonical observation pattern and SKIP the `requests_total{legacy=true|false}` binary label entirely? **Recommendation (§4):** YES.
- **Q2.** Migrate `X-Deprecated` headers on the imminently-removed routes to RFC 9745 `Deprecation: true` for hygiene? **Recommendation (§2.4 + §8):** NO.
- **Q3.** Open the YAML registration PR (§6) immediately after this discovery PR, or batch it with the migration-guide PR? **Recommendation (§6.3 + AGENTS.md §14):** sequential — one PR per phase.

---

## Appendix A — Decision Report Reference (linked)

> The decisions in this report (KEEP / SKIP / PROPOSE) are recommended for closure on the team's canonical decision-record flow. Future agents should:
>   (a) cite this report as the basis for any sign-off in PR review.
>   (b) record team votes in `architecture/decisions/00XX-legacy-script-route-removal.md` once Q1-Q3 close.
>   (c) update `architecture/deprecations.yaml` (§6.2) in a follow-up PR after decision ratification.

---

*End of report. **Branch:** `codex/legacy-route-metrics`. **Commits:** 1 (this markdown). **Reviewers requested:** pipeline-script-capability owner for §6.2 YAML body review; observability owner for §3 metric audit; voiceover owner for §10 Q1 precedent cross-check.*

*Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>*
