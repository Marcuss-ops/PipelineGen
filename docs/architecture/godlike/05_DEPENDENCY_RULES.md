# Dependency Rules

## Allowed direction

The target import graph is:

```text
cmd -> app -> capabilities -> kernel
             capabilities -> platform through owned ports/adapters
             app -> platform for construction
platform -> kernel when value types are required
```

Imports that reverse this direction are architecture violations.

## Consumer-owned ports

The package that needs behavior owns the interface.

Example: Scripts needs media search, so Scripts defines `MediaSearchPort`. Assets or a platform-backed adapter implements it. Scripts must not import the Assets HTTP package or a broad shared service interface.

Benefits:

- interfaces stay narrow;
- implementation details remain replaceable;
- removed capabilities do not leave shared ghost contracts;
- compile errors expose drift early.

## Cross-capability communication

Preferred order:

1. typed synchronous port for immediate request/response behavior;
2. typed domain event for asynchronous reaction;
3. read model explicitly owned and documented;
4. direct cross-capability import only in composition adapters.

Capability A must not import Capability B's transport, repository implementation, or internal service concrete.

## Events

Events represent completed domain facts, not commands disguised as events.

Good examples:

- `asset.registered`;
- `script.generated`;
- `delivery.completed`;
- `asset.index.requested.v1`.

Every event family has one schema owner, explicit version, idempotency key, producer, consumers, and supersede policy.

## Forbidden dependency patterns

- API packages importing infrastructure packages;
- application logic importing Gin;
- kernel/domain importing config or loggers;
- raw `*sql.DB` crossing capability boundaries;
- capability packages importing other capability transport packages;
- shared mega-interfaces used only to avoid adapters;
- runtime type assertions replacing real contracts;
- dependency setters after construction;
- global mutable service locators.

## Adapter placement

An adapter belongs near the boundary it protects:

- capability-specific translation: inside the capability;
- reusable technical client: platform;
- composition-only bridge between two capability ports: app;
- external protocol transport: platform or the owning capability adapter.

## Dependency budget

A constructor or capability build function should not require more than eight direct dependencies. Exceeding the budget triggers design review. Grouping unrelated dependencies into a mega-struct does not solve the problem; split the use case or capability instead.

## Nil policy

Required ports cannot be nil after composition. Optional ports must have documented product semantics. A nil value may not represent removed code, a deferred migration, or a hidden fallback.