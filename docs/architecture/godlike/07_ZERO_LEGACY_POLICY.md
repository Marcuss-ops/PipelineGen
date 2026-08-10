# Zero Legacy Policy

PipelineGen maintains a zero-legacy baseline: stale code, comments, and indirections are removed when they no longer carry runtime behavior.

## Goal

Keep the codebase free of accumulated legacy that obscures the current architecture and slows development.

## What counts as legacy

- Dead code paths that are no longer reachable.
- Stale comments describing retired behavior.
- Deprecated flags and fields with no remaining callers.
- Compatibility shims past their migration deadline.

## Default rule

When a feature or shim is no longer needed, remove it. Do not keep it "just in case."

The internal tree has one binding target layout: `app`, `kernel`,
`capabilities`, and `platform`. The existing `application`, `api`,
`infrastructure`, and `domain` roots are migration-only zones, not a second
architecture. They must receive **no new capabilities, public contracts,
providers, routes, files, or packages**. A change in a legacy zone is allowed
only for migration, removal, or a correctness/security fix and must map to an
owner, deadline, and target in `architecture/package_hotspots.json`.

## Temporary deprecation record

A deprecation may be temporary only if it has an owner, a deadline, and a tracked migration issue.

## Forbidden compatibility techniques

- Indefinite backward compatibility layers.
- Copy-paste fallback code that duplicates current logic.
- Feature flags that outlive their migration.

## Migration sequence

For compatibility changes, prefer: expand, backfill, cutover, contract.

## No fake availability

Never represent an unavailable backend as a successful no-op. Fail closed with typed errors or do not register the capability.

## Historical information

Git history is the archive. The working tree contains only current operational or machine-consumed documentation.
