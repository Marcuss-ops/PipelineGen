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