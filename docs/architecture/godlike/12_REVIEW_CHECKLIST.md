# Strict Review Checklist

## Ownership

- One owner for the changed responsibility.
- Canonical package extended instead of a sibling implementation.
- Route, job, provider, resolver, sampler, and table ownership are explicit.

## Execution

- One normalized command.
- One enqueue path and one handler per job type.
- One writer for every durable fact.
- Superseded readers and writers removed after cutover.
- No silent fallback.

## Boundaries

- Transport remains thin.
- Infrastructure stays behind narrow ports.
- The consumer owns the interface.
- Cross-capability imports are avoided outside composition adapters.
- Contracts contain no raw database handles or transport-framework types.

## Construction

- Mandatory dependencies are validated during composition.
- Services are complete immediately after construction.
- No dependency setter or late binding was added.
- Optional dependencies represent real optional behavior.

## Registries

- Registration uses the canonical registry.
- Duplicate keys fail.
- No new switch or local map duplicates registry dispatch.
- Registry freezes before runtime work starts.

## Data

- SQLite remains canonical for metadata.
- Qdrant remains a rebuildable projection.
- Each affected table has one owner.
- Released migrations are unchanged.
- Data migration has measurable gates.

## Legacy

- Old aliases, wrappers, routes, fields, config, tests, docs, metrics, and allowlists are removed.
- Temporary compatibility has an owner and deadline.
- CONTRACT removes the superseded path.

## Verification

- Focused tests cover the change.
- Failure and idempotency behavior are covered where relevant.
- Generated manifests are current.
- The diff stays within declared scope.

Approve only when the system becomes simpler or more explicit. Reject changes that only hide duplication behind another interface.