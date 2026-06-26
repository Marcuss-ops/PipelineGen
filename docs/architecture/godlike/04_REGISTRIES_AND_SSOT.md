# Registries and Single Source of Truth

## Principle

A registry is the only dispatch point for a family of implementations. Providers, routes, jobs, resolvers, samplers, codecs, and health checks are registered once instead of being repeated in switches or unrelated maps.

## Canonical registries

PipelineGen must converge on:

- Capability registry for module descriptors and lifecycle;
- route registry derived from module descriptors;
- job registry with one handler per job type;
- provider registry for media sources;
- resolver registry for destinations and references;
- sampler registry for selection strategies;
- codec registry for typed payload versions;
- health registry for capability-owned checks.

These can be read-only views of one capability registry.

## Lifecycle

1. Create registries during composition.
2. Register all capability descriptors.
3. Validate uniqueness and completeness.
4. Freeze the registries.
5. Use read-only lookup at runtime.

Registration after freeze is invalid.

## Uniqueness

Composition fails on duplicate:

- capability names;
- HTTP method and path pairs;
- job types;
- provider names;
- resolver kinds;
- sampler names;
- health-check names.

## Source-of-truth matrix

| Concern | Authority | Derived output |
|---|---|---|
| HTTP surface | Capability descriptors | Gin routes and API docs |
| Background execution | Job registry | Worker routing and job docs |
| Provider availability | Provider registry | Diagnostics and provider manifest |
| Asset metadata | Primary SQLite | Qdrant payload and API output |
| Semantic retrieval | Qdrant projection | Ranked search results |
| Runtime config | Validated immutable config | Capability config slices |
| Table ownership | Migration metadata and policy | Ownership report |

## Generated manifests

The architecture generator should produce:

```text
architecture/generated/capabilities.json
architecture/generated/routes.json
architecture/generated/jobs.json
architecture/generated/providers.json
architecture/generated/dependencies.json
architecture/generated/health.json
```

Generated files are audit artifacts. They are never edited by hand.

## Anti-duplication rule

A capability must use the canonical registry instead of adding a second dispatch map. Human documentation explains policy and decisions; it does not manually repeat the live route, job, provider, or service inventory.

## Actions to execute

- Implement one typed registry API with unique-key validation, deterministic ordering, lookup, enumeration, freeze, and post-freeze mutation rejection.
- Make capability descriptors the only writable input for route, job, provider, resolver, sampler, codec, health, and lifecycle registration.
- Adapt existing registration code incrementally to the canonical registry without leaving a permanent synchronization layer.
- Add composition-time validation for duplicate keys, missing handlers, missing owners, invalid dependencies, and conflicting route method/path pairs.
- Replace provider/source/job/resolver switch dispatch and private global maps with canonical registry lookup.
- Build `archgen` from the frozen registry and emit capability, route, job, provider, dependency, health, and ownership manifests.
- Add parity tests comparing generated manifests, mounted Gin routes, registered job handlers, and runtime registry enumeration.
- Remove manually maintained live inventories and old registration loops after generated parity reaches 100 percent.
- Add CI checks that reject registration outside approved descriptor/registry files and mutation after freeze.

## Final DONE check

The registry and SSOT model is DONE only when:

- [ ] Every active capability contributes exactly one descriptor to one canonical registry.
- [ ] Duplicate capability, route, job, provider, resolver, sampler, codec, and health keys fail composition.
- [ ] Registries freeze before HTTP serving, workers, or background hooks start.
- [ ] Tests prove lookup works and mutation after freeze fails.
- [ ] Gin routes, worker job routing, providers, resolvers, samplers, health checks, and lifecycle hooks are derived from registry data.
- [ ] Generated manifests match the runtime registry and mounted routes exactly.
- [ ] No independent dispatch switch, private global registration map, or duplicate registration loop remains.
- [ ] Human-maintained documents no longer duplicate live route, job, provider, service, or ownership inventories.
- [ ] Architecture CI rejects any new registration outside the canonical path.
- [ ] `go test ./...`, registry parity tests, generation checks, and architecture checks pass on `main`.