# Architectural violations report

This file is the stable index for the production-code architecture heatmap.
The previous 950-line snapshot mixed navigation, totals and volatile file-line
evidence in one document. The report is now split by ownership area.

## Scope

Migration audit scope: production Go files under the migration-only
`internal/api` and `internal/application` roots; tests are excluded. The
tracked boundary leaks are `*sql.DB`, `net/http`, `os`, and
`RootFolderOverride`. New capabilities belong under the target roots listed in
`ARCHITECTURE.md`, not in this audit surface.

## Severity

- **3 — structural/field:** an infrastructure type is embedded in a struct or global state.
- **2 — function signature:** an infrastructure type leaks through an application-layer signature.
- **1 — business logic:** direct infrastructure usage appears in application logic.

## Reports

- [API](docs/architecture/reports/violations/api.md) — weighted severity **38**.
- [Asset application](docs/architecture/reports/violations/assets.md) — weighted severity **78**.
- [Jobs](docs/architecture/reports/violations/jobs.md) — weighted severity **69**.
- [Other application](docs/architecture/reports/violations/other-application.md) — weighted severity **63**.

**Total packages:** 35  
**Total weighted severity:** 248  
**Historical total references:** 215

## Evidence policy

Exact file and line evidence must come from the architecture scanner output.
Line-number snapshots are intentionally not maintained in these stable documents:
a successful refactor makes them stale even when the underlying violation count
is unchanged. Git history retains the previous detailed snapshot for audits.
