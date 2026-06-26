# Feature Removal Checklist

> **Status**: **canonical** (promoted June 2026 from godlike/ design-state).
> This document is the single source of truth for the complete teardown
> sequence of a superseded feature. Working with `godlike/07`'s zero-
> legacy policy and `godlike/09`'s migration method, it covers
> Discovery → Runtime cut → Data handling → Code removal →
> Configuration and operations → Verification → Completion as
> distinct phases, with the removal property answerable via a single
> delegation: "The feature is removed when there are zero active
> references and no compatibility path can reactivate it."

## Purpose

Removing a package is not enough. A feature is gone only when its runtime surface, durable state, configuration, operational tooling, and documentation are gone or intentionally migrated.

## Discovery

Identify:

- owner package and constructors;
- routes and middleware;
- job types, codecs, handlers, and worker registration;
- provider/resolver/sampler entries;
- database tables, columns, indexes, and migrations;
- config fields and environment mappings;
- metrics, alerts, dashboards, and health checks;
- scripts, admin commands, cron/systemd references;
- tests, fixtures, mocks, generated manifests, and docs;
- downstream callers and event consumers.

## Runtime cut

- Stop new writes through the feature.
- Move required callers to the canonical replacement.
- Remove route and job registration.
- Remove lifecycle and background hooks.
- Remove provider, resolver, sampler, and health entries.
- Confirm startup no longer constructs the service.

## Data handling

Choose and document one outcome:

- migrate durable data to the canonical owner;
- archive data outside active runtime;
- retain the table temporarily as read-only with a removal deadline;
- remove obsolete schema in a later safe migration.

Do not leave two writable authorities.

## Code removal

Remove:

- service and handler code;
- DTOs and copied local replacement types;
- ports used only by the removed feature;
- adapters and repository methods;
- compatibility aliases and forwarding wrappers;
- dependency fields in composition bundles;
- optional branches that can now never execute;
- imports kept alive with blank assignments.

## Configuration and operations

Remove obsolete config keys, defaults, validation, examples, admin commands, scripts, service units, alerts, metrics, and runbooks. A temporary config alias requires a deprecation deadline.

## Verification

Search the repository for feature names, route paths, job types, config keys, table names, metrics, and old package paths.

Verify:

- build and tests pass;
- architecture checks pass;
- generated manifests no longer list the feature;
- no route returns a placeholder response for the removed operation;
- no background worker expects the removed job;
- no raw data migration dependency was broken;
- recent commit history contains the intended removal.

## Completion

The feature is removed when there are zero active references and no compatibility path can reactivate it accidentally.