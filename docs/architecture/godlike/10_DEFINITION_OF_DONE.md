# Definition of Done

## New or migrated capability

A capability is complete only when:

- it has one owner package and one module descriptor;
- required dependencies are typed and validated during composition;
- configuration is narrow, validated, and immutable;
- HTTP handlers are thin and contain no infrastructure logic;
- commands and queries are typed and normalized once;
- every job type has one codec, one handler, and one registry binding;
- repositories expose narrow ports and no raw database handle;
- table ownership is declared;
- external dependencies have health checks;
- lifecycle hooks are registered in the descriptor;
- metrics use one capability namespace;
- unit and integration tests cover success, failure, idempotency, and cancellation where applicable;
- generated manifests and docs are updated;
- no old route, job, type, config key, writer, reader, test, or comment remains.

## Data migration

A data migration is complete only when:

- EXPAND schema is deployed safely;
- BACKFILL is resumable and measurable;
- invariants are verified before cutover;
- all writers use the new model;
- all readers use the new model;
- old fields/tables are no longer read;
- CONTRACT removes old code and schema at the planned safe point;
- rollback and recovery behavior is documented;
- released migrations were not rewritten.

## Registry migration

A registry migration is complete only when:

- all registrations pass through one canonical registry;
- duplicate keys fail fast;
- registry freezes before runtime serving;
- generated manifests are built from the registry;
- old registration loops and manual inventories are removed;
- tests prove no registration after freeze.

## Legacy removal

A removal is complete only when searches find zero references across:

- production code;
- tests and fixtures;
- route and job registries;
- config structs and environment mapping;
- migrations and schema bootstrap;
- scripts and operational tooling;
- metrics and alerts;
- generated manifests;
- documentation and comments.

## Pull request or direct-main change quality

Regardless of delivery method, the change must:

- start from current `main`;
- solve one bounded responsibility;
- avoid unrelated refactors;
- include focused tests or document why a documentation-only change needs none;
- leave formatting, build, tests, architecture checks, and generated files clean;
- inspect the final commit history and diff;
- avoid generated artifacts not intended for source control.

## Completion statement

A task is not done because the new path works. It is done when the new path is the only path.

## Actions to execute

- Add this Definition of Done to the repository contribution workflow, agent instructions, task templates, review checklist, and release process.
- Require every implementation task to identify which DoD categories apply before work starts.
- Require evidence for every checked item: test command, generated report, repository search, migration result, runtime metric, or commit reference.
- Add automated checks for all criteria that can be enforced mechanically.
- Require an explicit reviewer or owner sign-off for criteria that need architectural judgment.
- Block completion when any applicable item is marked not applicable without a written reason.
- Add a post-publication verification step confirming the expected commit is on the intended branch and the latest CI result is green.
- Audit completed migrations periodically and reopen any task whose old path, config, data access, or registration remains.

## Final DONE check

This Definition of Done is itself DONE only when:

- [ ] It is referenced by `AGENTS.md`, implementation task templates, and review guidance.
- [ ] Every new capability, migration, registry change, removal, and direct-main change records applicable DoD evidence.
- [ ] Mechanical criteria are enforced by tests, `archcheck`, generation checks, or CI.
- [ ] Non-mechanical criteria have a named reviewer or owner sign-off.
- [ ] No task is closed with an active old route, job, writer, reader, type, config key, test, metric, or documentation path.
- [ ] Every data migration includes EXPAND, BACKFILL, CUTOVER, CONTRACT, integrity evidence, and recovery notes when applicable.
- [ ] Every registry migration proves uniqueness, freeze behavior, parity, and removal of old registration paths.
- [ ] Documentation-only changes prove link integrity and consistency with current code.
- [ ] The final diff and recent commit history are inspected after publication.
- [ ] The target branch is green and generated files are current.
- [ ] A task can be declared DONE only when the canonical path is the only active path.