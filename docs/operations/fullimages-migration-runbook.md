# Fullimages Migration Runbook

> **Status:** SHIPPED 2026-07-10 via `PR-LEGACY-CLEANUP-FULLIMAGES-DOWNSTREAM-MIGRATION`.
> **Closes:** §7.1 forward-pointer `PR-LEGACY-CLEANUP-FULLIMAGES-DOWNSTREAM-MIGRATION`
> of `architecture/action-plans/2026-07-10-legacy-cleanup-5-item-orchestration.md`.

This runbook documents the operator-facing migration CLI for the
`fullimages` surface rename shipped in **Item 5 (Option B verdict)**
of the `LEGACY-CLEANUP-5-ITEM-ORCHESTRATION-2026-07-10` wave.

---

## §0 — Why this migration exists

The pre-closure `fullimages` service generated MP4s via a Ken Burns
effect (the "video" mental model). The Option B verdict shipped
2026-07-10 collapsed the MP4 pipeline to a still-image pipeline because:

- The Ken Burns MP4 path was orphaned (no live callers).
- The wire shape conflated `SectionVideo` (a still-image record) with
  the generated MP4 (a media asset) — a godlike/07 silent-success class.
- The cache sidecar (`cacheMeta`, `saveCacheSidecar`,
  `loadCacheSidecar`) doubled the per-image cost with no observable
  benefit.

The rename is purely a **wire-shape + Go identifier** migration. No
**runtime behavior** changes: the still-image is uploaded to Drive
exactly as before; the `images` array on the response carries the
canonical `SectionImage` records.

---

## §1 — What changed (canonical before/after)

| Surface                    | Before                              | After                                |
|----------------------------|-------------------------------------|--------------------------------------|
| **REST endpoint**          | `POST /api/fullimages/video/generate` | `POST /api/fullimages/image/generate` |
| **Response field**         | `videos`                            | `images`                             |
| **Response element type**  | `SectionVideo`                      | `SectionImage`                       |
| **Go struct field**        | `VideoPath`                         | `ImagePath`                          |
| **Go method**              | `generateOneVideo`                  | `generateOneImage`                   |
| **Go service struct**      | `imgService` + `ffmpegProc` + `publisher` + `imagesDir` + `log` | `imgService` + `imagesDir` + `log` (3 fields; orphan ffmpegProc + publisher removed) |
| **Removed functions**      | `processGeneratedVideo`, `uploadAndFinish`, `publishToDrive` | — (deleted)                           |
| **Removed constants**      | `videoOutWidth`, `videoOutHeight`, `videoDuration` | — (deleted)                           |
| **Removed cache sidecar**  | `cacheMeta`, `cachePath`, `saveCacheSidecar`, `loadCacheSidecar` | — (deleted)                           |

The wire-shape transition is **byte-equivalent** to the closure
commits — no manual migration of any data is needed. The
`fullimages-migrate` CLI helps operators find and update any
operator-side references (scripts, docs, configs) to the old names.

---

## §2 — CLI usage

### §2.1 — Dry-run (the canonical default)

```bash
./admin fullimages-migrate \
  --target-dir ~/ops/scripts \
  --exts .sh,.py,.md,.yaml
```

**Output**:

```
NOTICE: fullimages video→image migration (LEGACY-CLEANUP-5-ITEM-ORCHESTRATION Item 5, Option B)
NOTICE: shipped 2026-07-10; CLI: `fullimages-migrate [--apply] [--target-dir DIR] [--exts CSV]`
NOTICE: default = dry-run (no writes); --apply writes text replacements to disk

Target directory: /home/operator/ops/scripts
Patterns scanned: 9

Files with old patterns: 3
Total pattern matches:    7

Per-class summary:
  URL            REST endpoint path                  2 match(es)
  JSON-bracket   jq field access                     1 match(es)
  Go-type        Go type rename                      2 match(es)
  ...

Per-file details:
  /home/operator/ops/scripts/legacy_video_gen.sh
    URL: 1  ("api/fullimages/video/generate" → "api/fullimages/image/generate")
    Go-type: 1  ("SectionVideo" → "SectionImage")
  /home/operator/ops/scripts/parse_videos.py
    JSON-bracket: 2  (".videos[" → ".images[")
  /home/operator/ops/scripts/runbook.md
    URL: 1  ("api/fullimages/video/generate" → "api/fullimages/image/generate")
    Go-type: 1  ("SectionVideo" → "SectionImage")

DRY-RUN: no files were modified. Re-run with --apply to write the changes.
```

### §2.2 — Apply (operator-explicit, opt-in)

```bash
./admin fullimages-migrate \
  --target-dir ~/ops/scripts \
  --exts .sh,.py,.md,.yaml \
  --apply
```

**Output**:

```
... (dry-run report) ...

APPLIED: 3 file(s) updated
NOTICE: please re-run your downstream tests to confirm the migration.
```

### §2.3 — Scoping to a single file (extension filter)

```bash
./admin fullimages-migrate \
  --target-dir ~/ops/scripts/single_legacy_script.sh \
  --exts .sh \
  --apply
```

### §2.4 — Excluding noisy directories

The CLI automatically skips `.git`, `node_modules`, and `vendor`
directories (canonical hermetic discipline; mirrors `cleanup_drive_orphans`).
For other noisy directories, scope the scan with `--target-dir`:

```bash
./admin fullimages-migrate --target-dir ~/ops/scripts/prod --exts .sh --apply
```

### §2.5 — JSON output mode (automation harnesses)

For CI pipelines, monitoring scrapers, and operator dashboards that
need to consume the migration report programmatically, add `--json`:

```bash
./admin fullimages-migrate \
  --target-dir ~/ops/scripts \
  --exts .sh,.py,.md,.yaml \
  --json
```

**Output** (valid JSON, no NOTICE banner on stdout):

```json
{
  "meta": {
    "target_dir": "/home/operator/ops/scripts",
    "exts": [".sh", ".py", ".md", ".yaml"],
    "mode": "dry-run",
    "timestamp": "2026-07-10T12:34:56.789Z"
  },
  "patterns": [
    {"class": "URL",         "old": "api/fullimages/video/generate", "new": "api/fullimages/image/generate", "description": "REST endpoint path"},
    {"class": "URL-partial", "old": "fullimages/video/generate",     "new": "fullimages/image/generate",     "description": "REST endpoint path (partial)"},
    ...
  ],
  "totals": {
    "files_with_hits": 3,
    "total_matches": 7
  },
  "per_class_totals": {
    "URL": 2,
    "JSON-bracket": 1,
    "Go-type": 2
  },
  "files": [
    {
      "path": "/home/operator/ops/scripts/legacy_video_gen.sh",
      "total_matches": 2,
      "hits": {"URL": 1, "Go-type": 1}
    }
  ],
  "warnings": [],
  "applied_files_count": 0
}
```

The `--json` mode is **mutually exclusive with the human-readable path**:
it suppresses the NOTICE banner + per-file WARN stderr lines
(collected in the `warnings` JSON field instead), so stdout is
machine-parseable end-to-end. Combine with `--apply` to get
`applied_files_count > 0` in the response.

**jq examples**:

```bash
# List every file with old patterns (sorted, diff-friendly)
./admin fullimages-migrate --target-dir . --exts .sh,.py --json | jq '.files[].path'

# Count files needing migration (for CI gate: fail if > 0)
./admin fullimages-migrate --target-dir . --exts .sh,.py --json | jq '.totals.files_with_hits'

# Per-class aggregate (for monitoring dashboard)
./admin fullimages-migrate --target-dir . --exts .sh,.py --json | jq '.per_class_totals'

# Apply + report (combined)
./admin fullimages-migrate --target-dir . --exts .sh,.py --json --apply | jq '{mode: .meta.mode, applied: .applied_files_count, total: .totals.total_matches}'
```

### §2.6 — Before/after concrete example

### §2.5 — Before/after concrete example

**Before** (`legacy_fullimages.sh`):

```bash
#!/usr/bin/env bash
# Legacy operator script
response=$(curl -X POST http://localhost:8080/api/fullimages/video/generate \
  -H "X-Velox-Admin-Token: $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"topic": "boxing gym", "count": 3}')

# Parse the response
videos=$(echo "$response" | jq '.videos[]')
for video in $videos; do
  echo "Video at: $(echo $video | jq -r .VideoPath)"
done
```

**After** (`legacy_fullimages.sh`, post-`--apply`):

```bash
#!/usr/bin/env bash
# Legacy operator script
response=$(curl -X POST http://localhost:8080/api/fullimages/image/generate \
  -H "X-Velox-Admin-Token: $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"topic": "boxing gym", "count": 3}')

# Parse the response
images=$(echo "$response" | jq '.images[]')
for image in $images; do
  echo "Image at: $(echo $image | jq -r .ImagePath)"
done
```

The migration is **byte-equivalent** to the closure commits; no
operational downtime is required.

---

## §3 — Canonical migration table

The 9 pattern classes the CLI scans for. Each is a **literal
replacement** (no regex, no whitespace normalization, no line
reordering). One file write per file.

| # | Class        | Old literal                              | New literal                              | Use case                  |
|---|--------------|------------------------------------------|------------------------------------------|---------------------------|
| 1 | `URL`        | `api/fullimages/video/generate`          | `api/fullimages/image/generate`          | cURL / REST endpoint      |
| 2 | `URL-partial`| `fullimages/video/generate`              | `fullimages/image/generate`              | Partial URL (no `api/`)   |
| 3 | `JSON-bracket` | `.videos[`                             | `.images[`                               | jq field access           |
| 4 | `JSON-dquote` | `["videos"]`                             | `["images"]`                             | JSON string key (dquote)  |
| 5 | `JSON-name`  | `"videos"`                               | `"images"`                               | JSON name key             |
| 6 | `JSON-legacy`| `videos:`                                | `images:`                                | JSON legacy key (yaml)    |
| 7 | `Go-type`    | `SectionVideo`                           | `SectionImage`                           | Go type rename            |
| 8 | `Go-field`   | `VideoPath`                              | `ImagePath`                              | Go struct field rename    |
| 9 | `Go-method`  | `generateOneVideo`                       | `generateOneImage`                       | Go method rename          |

---

## §4 — Failure modes

| Exit code | Reason                          | Operator action                         |
|-----------|---------------------------------|-----------------------------------------|
| 0         | Success (dry-run or apply)      | —                                       |
| 1         | `--target-dir` not accessible   | Verify the path exists; rerun            |
| 1         | `--exts=""` (empty)             | Pass at least one extension             |
| 1         | Permission denied on a file     | Re-run as the file's owner              |
| 1         | Unknown flag (typo)             | Check `--help` for the canonical flags   |

The CLI **never** silently degrades (godlike/07 NO-FAKE-AVAILABILITY).
Every error path emits a typed message and exits non-zero.

---

## §5 — Self-skipping (the canonical safe default)

The CLI automatically **skips its own source file** (`fullimages_migrate.go`
and `fullimages_migrate_test.go`) to avoid self-matching the pattern
literals that appear as string constants. This is the canonical
godlike/07 hermetic discipline — the CLI cannot report a false-positive
match against its own code.

---

## §6 — File mode preservation

When `--apply` writes a file, it **preserves the original file mode
bits** (e.g. `0o755` for executable scripts stay executable; `0o644`
for configs stay world-readable). No `chmod` side effects.

---

## §7 — Cross-references

- **Closure commits** (the canonical SSOT for the wire-shape change):
  - `b7d73a18335234cf34d27cdaf9cac25c0d3a96bc` (Item 5 source rename, 5 fields → 3 fields)
  - `7e25e58a946c09afc9db6796c2210494184c1aee` (Item 5 lockstep docs + test)
- **Action plan**: `architecture/action-plans/2026-07-10-legacy-cleanup-5-item-orchestration.md#§7.1`
- **Wave-flip entry**: `architecture/waves/wave_p1_high.yaml#LEGACY-CLEANUP-5-ITEM-ORCHESTRATION-2026-07-10` (status: shipped, exit_signal: true)
- **Wave-tracker audit-pin**: `architecture/waves/wave_p1_high.yaml#PR-LEGACY-CLEANUP-HOTSPOT-CROSSREF` (0 NEW hotspots, 14-day cross-validation)

---

## §8 — Related CLIs (operator workflow context)

- `./admin cleanup-drive-orphans` — Drive orphan cleanup (per the
  `cleanup_drive_orphans.go` precedent)
- `./admin drive-bootstrap`, `./admin drive-doctor`, `./admin drive-reconcile` — Drive diagnostics
- `./admin qdrant-maintenance` — Qdrant diagnostic / repair (separate lifecycle)
- `./admin reindex-qdrant` — Qdrant blue-green reindex

The `fullimages-migrate` CLI is a **one-shot operator workflow**
(ships in `availableCommands`) — it is NOT a continuous lifecycle
tool, so it does not need a scheduled cron entry.

---

## §9 — When to run this

Run the dry-run **once** at the start of your operator-side migration
window (e.g. just after `git pull` to origin/main):

```bash
cd ~/ops/scripts
./admin fullimages-migrate --target-dir . --exts .sh,.py,.md,.yaml
```

If the dry-run report shows no hits, the migration is already
complete for your operator scope. If hits are found, review the
report + re-run with `--apply`.

The CLI is **idempotent**: re-running on an already-migrated codebase
returns 0 hits (the new patterns are NOT scanned; the old patterns
do not appear in post-migration files).

---

## §10 — Verification

After `--apply`:

```bash
# Re-run the dry-run; should now report "No old patterns found."
./admin fullimages-migrate --target-dir . --exts .sh,.py,.md,.yaml

# Run your downstream operator tests
./run_operator_smoke_tests.sh
```

If the dry-run still reports hits, the file likely contains a
**partial** old pattern (e.g. a documentation snippet that quotes
both old and new) — the CLI does NOT match partial patterns (godlike/07
NO-FAKE-AVAILABILITY: refuse to match partial strings). Manually
inspect the report and update the partial references by hand.

---

**Co-authored-by:** PipelineGen Agent <agent@pipelinegen.local>

**Reviewers:** See `cmd/admin/fullimages_migrate.go` and
`cmd/admin/fullimages_migrate_test.go` for the canonical implementation
+ hermetic TDD surface.
