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
