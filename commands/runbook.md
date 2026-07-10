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
```

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
