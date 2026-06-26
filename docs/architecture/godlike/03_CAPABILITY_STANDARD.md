# Capability Standard

## Definition

A capability is a complete vertical business slice. It owns its contracts, use cases, HTTP transport, jobs, events, persistence ports, health checks, and module descriptor.

## Single build entrypoint

Every capability exposes exactly one build function:

```go
func Build(deps Dependencies) (module.Descriptor, error)
```

The result must be complete. Missing mandatory dependencies return an error during composition; they must not create partially initialized services.

## Canonical descriptor

Each capability contributes one descriptor containing its unique name, routes, job handlers, providers, event consumers, health checks, and lifecycle hooks. Runtime registration and generated documentation must be derived from this descriptor.

## Required files

A normal capability may contain:

- `module.go`: build function and descriptor;
- `contract.go`: typed commands, queries, results, and public ports;
- `service.go`: orchestration and business use cases;
- `http.go`: thin transport;
- `jobs.go`: job constants, codec, and handlers;
- `events.go`: produced and consumed events;
- `repository.go`: narrow persistence ports;
- `adapters.go`: capability-specific adapters;
- focused tests.

## HTTP rules

Handlers may bind input, validate transport syntax, translate to a command/query, invoke one use case, map typed errors, and serialize output.

Handlers may not execute SQL, call Drive/Qdrant/FFmpeg/processes directly, manage job state, contain provider switches, or choose a legacy fallback.

A route exists only when the capability has been built successfully.

## One normalized command

HTTP, jobs, CLI, and internal callers use the same typed normalized command. Defaults and normalization happen once. Deduplication keys and job payloads are produced from the normalized command, never from raw input.

## Job rules

Every job type has exactly one constant, one codec, one handler, one owner capability, and one registry binding. HTTP enqueues through the common jobs service. Workers enter through the registered handler only.

## Repository rules

The capability declares narrow consumer-owned ports. Repositories must not expose raw database handles, generic update maps, or table-agnostic escape hatches. Transaction access is provided through typed transaction boundaries.

## Construction rules

Required dependencies are passed to constructors. After construction there are no dependency setters, late binding, or service replacement. Optional dependencies represent real optional product behavior, never removed packages or incomplete migrations.

## Removal property

A well-formed capability can be retired by removing its vertical package, its single registry entry, its active config section, owned future migrations, and regenerated manifests. No copied DTOs or hidden route registrations may remain elsewhere.

## Actions to execute

- Inventory every current feature and assign it to exactly one capability owner.
- Introduce the shared `module.Descriptor` contract, validation rules, and one `Build` entrypoint per migrated capability.
- Define typed capability commands, queries, results, events, job payloads, and consumer-owned ports.
- Move HTTP behavior to thin transport that invokes one use case and contains no infrastructure access or provider dispatch.
- Consolidate defaults and normalization into one typed function shared by HTTP, jobs, CLI, and internal callers.
- Consolidate each job type to one constant, codec, handler, owner, and registry binding.
- Replace broad repositories and raw database exposure with narrow owned repository methods and typed transaction boundaries.
- Replace dependency setters and late binding with complete constructor dependency structs validated during `Build`.
- Add focused unit, integration, route, job-codec, idempotency, failure, and removal tests.
- Delete the superseded package layout after the migrated capability has zero callers outside its canonical contracts.

## Final DONE check

A capability is DONE under this standard only when:

- [ ] It has one owner directory, one `Build` function, and one validated descriptor.
- [ ] All mandatory dependencies are typed and non-nil before the descriptor is registered.
- [ ] HTTP, job, CLI, and internal execution use the same normalized command/query.
- [ ] Every job type has one codec, one handler, and one registry binding.
- [ ] Transport contains no SQL, Drive, Qdrant, FFmpeg, process, retry, or job-state logic.
- [ ] Ports are narrow and consumer-owned; no raw database handle or generic update map crosses the boundary.
- [ ] No dependency setter, runtime type assertion, copied DTO, compatibility alias, or pass-through wrapper is required.
- [ ] Routes, jobs, providers, lifecycle hooks, and health checks appear in generated manifests from the descriptor.
- [ ] Focused tests and affected integration tests pass.
- [ ] Removing the capability would require deleting only its vertical package, registry contribution, config, and owned migrations, with no hidden registration elsewhere.