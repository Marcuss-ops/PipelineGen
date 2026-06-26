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
- raw database handles crossing capability boundaries;
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

## Actions to execute

- Generate the current package import graph and classify every edge.
- Add automated checks for the allowed dependency direction.
- Move interfaces to the capability that consumes them and keep each interface narrow.
- Add explicit adapters for valid cross-capability communication.
- Replace concrete cross-capability imports with ports, events, or owned read models.
- Remove transport and infrastructure types from domain and application contracts.
- Replace late binding and dependency setters with complete construction.
- Split services whose constructors exceed the dependency budget.
- Define owner, version, producer, consumers, and idempotency for every event.
- Remove transitional dependency exceptions after each cutover.

## Final DONE check

- [ ] The package graph contains no forbidden dependency edge.
- [ ] Kernel and domain packages depend only on approved stable types.
- [ ] HTTP transport contains no infrastructure implementation dependency.
- [ ] No capability imports another capability's transport or repository implementation.
- [ ] Cross-capability calls use narrow consumer-owned ports or typed events.
- [ ] Contracts expose no raw storage or transport framework types.
- [ ] No late-binding setter or mutable service locator remains.
- [ ] Required ports are complete after composition.
- [ ] Constructor dependency budgets are respected.
- [ ] Architecture checks pass with zero dependency exceptions.