# Fullimages Migration Runbook (Pointer)

> **Canonical runbook location**: [`docs/operations/fullimages-migration-runbook.md`](../docs/operations/fullimages-migration-runbook.md)
>
> This file is a thin pointer to satisfy the literal spec path. The canonical
> runbook (with full CLI before/after examples, migration table, canonical rg
> queries, and FAQ) lives at the path above per the established codebase
> convention (artlist-runbook.md, stock-e2e-runbook.md, qdrant-verification-runbook.md).

## Quick Reference

For operators who only need the CLI invocation:

```bash
# Build the admin CLI (one-time)
go build -o admin ./cmd/admin/

# Dry-run (default — no files modified)
./admin fullimages-migrate --target-dir /path/to/operator/repo

# Apply the text replacements
./admin fullimages-migrate --target-dir /path/to/operator/repo --apply

# Custom file extensions
./admin fullimages-migrate --exts .sh,.py,.md

# JSON output (for automation harnesses / jq / CI pipelines / monitoring scrapers)
./admin fullimages-migrate --target-dir /path/to/operator/repo --json
./admin fullimages-migrate --target-dir /path/to/operator/repo --json --apply | tee migration-report.json
```

## JSON Output Mode (`--json`)

For CI pipelines, monitoring scrapers, or any automation that needs to
parse the report programmatically, pass `--json` to get a stable
structured JSON envelope to stdout (instead of the human-readable
table). Per godlike/07 NO-FAKE-AVAILABILITY, `--json`:

- Suppresses the `NOTICE:` banner (keeps stdout machine-parseable)
- Suppresses per-file `WARN:` lines on stderr (collected in the JSON
  `warnings` field instead)
- Emits one `{"meta": ..., "patterns": ..., "totals": ..., "files": ..., "warnings": ...}`
  object with a **stable, additive-only** schema (godlike/06 SSOT)

### Canonical schema (godlike/06 SSOT — automation-harness surface)

```json
{
  "meta": {
    "target_dir": "/path/to/operator/repo",
    "exts": [".sh", ".py", ".md"],
    "mode": "dry-run",
    "timestamp": "2026-07-10T15:32:18.514Z"
  },
  "patterns": [
    {"class": "URL", "old": "api/fullimages/video/generate", "new": "api/fullimages/image/generate", "description": "REST endpoint path"},
    ... (all 9 pattern classes surfaced verbatim)
  ],
  "totals": {"files_with_hits": 3, "total_matches": 7},
  "per_class_totals": {"URL": 2, "JSON-bracket": 3, "Go-type": 1, "Go-field": 1},
  "files": [
    {"path": "/path/to/operator/repo/scripts/build.sh", "total_matches": 3, "hits": {"URL": 1, "JSON-bracket": 1, "Go-type": 1}},
    ... (sorted by path)
  ],
  "warnings": ["cannot access /path/to/locked: permission denied (skipped)"],
  "applied_files_count": 0
}
```

### Example: pipe to `jq` for a one-line summary

```bash
./admin fullimages-migrate --target-dir . --json | jq '.totals'
# → {"files_with_hits": 3, "total_matches": 7}

./admin fullimages-migrate --target-dir . --json | jq '.files[] | .path'
# → "/abs/path/to/script-a.sh"
# → "/abs/path/to/script-b.md"
# → "/abs/path/to/main.go"

./admin fullimages-migrate --target-dir . --json --apply | jq '.applied_files_count'
# → 3   (use as a CI gate: must equal expected migration scope)
```

### Example: gate a CI job on zero hits (post-migration)

```bash
hits=$(./admin fullimages-migrate --target-dir . --json | jq '.totals.total_matches')
if [ "$hits" -gt 0 ]; then
  echo "Migration incomplete: $hits old patterns remain" >&2
  ./admin fullimages-migrate --target-dir . --json | jq '.files[]'
  exit 1
fi
```

The full JSON schema is documented in §2.6 of the canonical runbook.

## CLI Before/After Example (condensed)

The full before/after walkthrough lives in
[§2.5 of the canonical runbook](../docs/operations/fullimages-migration-runbook.md#25--beforeafter-concrete-example).
Here's a condensed 3-line diff showing the canonical rename pattern:

**Before** (operator script using the old `/generate-video` mental model):

```bash
curl -X POST http://localhost:8080/api/fullimages/video/generate -d '{"topic":"boxing"}'
echo "$response" | jq '.videos[] | .VideoPath'
```

**After** (post-`--apply`, byte-equivalent to the closure commits):

```bash
curl -X POST http://localhost:8080/api/fullimages/image/generate -d '{"topic":"boxing"}'
echo "$response" | jq '.images[] | .ImagePath'
```

_(The CLI handles all 9 pattern classes automatically — see table below.)_

The 9 pattern classes the CLI scans for:

| Class        | Old literal → New literal                          |
|--------------|----------------------------------------------------|
| `URL`        | `api/fullimages/video/generate` → `api/fullimages/image/generate` |
| `URL-partial`| `fullimages/video/generate` → `fullimages/image/generate` |
| `JSON-bracket` | `.videos[` → `.images[`                          |
| `JSON-dquote` | `["videos"]` → `["images"]`                      |
| `JSON-name`  | `"videos"` → `"images"`                           |
| `JSON-legacy`| `videos:` → `images:`                              |
| `Go-type`    | `SectionVideo` → `SectionImage`                    |
| `Go-field`   | `VideoPath` → `ImagePath`                          |
| `Go-method`  | `generateOneVideo` → `generateOneImage`            |

## Why this file exists

The user spec for `PR-LEGACY-CLEANUP-FULLIMAGES-DOWNSTREAM-MIGRATION` literally
asked for `commands/runbook.md`. The canonical codebase convention places
operator-facing runbooks under `docs/operations/`. Rather than duplicate the
~300 LoC runbook content, this file serves as a canonical pointer with a
condensed before/after example inline (per the literal spec's "CLI
prima/dopo" requirement).

For the full migration context (full §2.5 walkthrough, §3 migration table,
§4 failure modes, §5 self-skipping, §6 file mode preservation, §7
cross-references, §8 related CLIs, §9 when to run, §10 verification), see
the [canonical runbook](../docs/operations/fullimages-migration-runbook.md).
