# Feature Removal Checklist

Use this checklist when removing a feature.

## Purpose

Ensure a feature is removed completely and safely without leaving stale code, data, or documentation.

## Discovery

- Identify all callers and consumers of the feature.
- Identify all database tables, columns, and indexes owned by the feature.
- Identify all configuration keys and environment variables.

## Runtime cut

- Remove feature flags and toggles.
- Remove HTTP routes and handlers.
- Remove job handlers and workers.

## Data handling

- Archive or migrate data as required.
- Remove database tables and columns only after the migration deadline.
- Clean up outbox events and derived projections.

## Code removal

- Delete implementation files.
- Delete tests that only cover the removed feature.
- Remove types and constants that are no longer used.

## Configuration and operations

- Remove environment variables and configuration keys.
- Update deployment manifests and runbooks.

## Verification

- Run `make verify-main`.
- Run integration tests for affected flows.
- Confirm no stale references remain.

## Completion

- Update architecture docs and changelogs.
- Commit with a clear message describing the removal.
