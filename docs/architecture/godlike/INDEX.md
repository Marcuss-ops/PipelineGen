# Canonical Documentation Index

This is the central navigation for the canonical documentation chain in
PipelineGen. It does NOT duplicate content from the 5 godlike docs — it
maps the SSOT surface so operators can find the single source of truth for
any architectural concern in one place.

Per `godlike/06 "One owner per fact"` and `AGENTS.md "Do not add duplicate
source-of-truth documents"`, every fact below has exactly one canonical
owner. This index is the navigational bridge; the docs it points to are
the SSOT.

## The 5 godlike docs (SSOT map)

| # | Doc | Purpose | Hard-gated by |
|---|-----|---------|---------------|
| 06 | `docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md` | Durable authority + one-owner-per-fact + DB rules + Qdrant projection + Drive + Configuration | `data_ownership_doc_missing` + `_doc_incomplete` |
| 07 | `docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md` | Zero-legacy baseline + deprecation records + migration sequence (expand, backfill, cutover, contract) + NO-FAKE-AVAILABILITY | `legacy_policy_doc_missing` + `_doc_incomplete` |
| 08 | `docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md` | Mandatory checks + boundary checks + registry checks + complexity budgets + zero-baseline rule | `ci_gates_doc_missing` + `_doc_incomplete` |
| 11 | `docs/architecture/godlike/11_AGENT_EXECUTION_PLAYBOOK.md` | Agent scope + forbidden additions + testing protocol + migration method + final verification | `agent_playbook_doc_missing` + `_doc_incomplete` |
| 13 | `docs/architecture/godlike/13_FEATURE_REMOVAL_CHECKLIST.md` | 7-phase teardown sequence + discovery + runtime cut + data handling + code removal + verification + completion | `removal_doc_missing` + `_doc_incomplete` |

All 5 docs are enforced as hard gates by `architecture/policy.yaml::hard_gates`
(per `godlike/08 "Mandatory checks"`). Deleting any of them — or removing
an H2 section that the binary considers required — surfaces as a CI build
failure on the next push.

## The ownership surface

The ownership ledger is split into 6 shard files under `architecture/ownership/`
(per `architecture/ownership/README.md`). The aggregate
`architecture/ownership.generated.yaml` is a compatibility artifact and MUST
NOT be edited manually.

| Shard | Owner surface |
|-------|---------------|
| `architecture/ownership/modules.yaml` | Top-level modules (api, app, application, domain, infrastructure, ...) |
| `architecture/ownership/jobs.yaml` | Job types + handler bindings |
| `architecture/ownership/services.yaml` | Service-layer entries |
| `architecture/ownership/application.yaml` | Application-layer owned surfaces |
| `architecture/ownership/infrastructure.yaml` | Infrastructure adapters |
| `architecture/ownership/packages.yaml` | Package-level ownership |

Regenerate the aggregate with `go run ./cmd/architecture-aggregate`. Validate
with `go run ./cmd/architecture-aggregate --dry-run`.

## Policy enforcement

`architecture/policy.yaml` is the executable source of truth for the
architecture. The 5 godlike docs above are its `data_ownership_doc`,
`legacy_policy_doc`, `ci_gates_doc`, `agent_playbook_doc`, `removal_doc`
fields. The `hard_gates` block enforces their presence + completeness at
CI time (see `godlike/08 "Mandatory checks"`).

The on-disk enforcement lives in two places:

1. `cmd/archcheck/main.go::scanCIGatesDoc` validates the 5 docs are
   present + contain the required H2 sections.
2. `scripts/ci-architectural-checks.sh` is the legacy ratchet that
   runs alongside `cmd/archcheck` per the Wave-22 hard-gate promotion.

The `lint_gates` block in `policy.yaml` lists the canonical owner +
allowlist for each script-side check, with the cross-reference to the
godlike doc that defines the rule.

## The 8-domain ownership table (godlike/06 SSOT)

Per `godlike/06 "One owner per fact"`, every fact has exactly one canonical
owner. The 8 top-level ownership domains are defined canonically at
`architecture/ownership/modules.yaml` — that shard is the SSOT and
this index does NOT duplicate the table here (per `godlike/07 +
AGENTS.md "Do not add duplicate source-of-truth documents"`).

Phase 0 lets the table drift; Phase 1+ gates the binary enforcement per
`percheck_cross_package_ownership` rule. The canonical single source for
the 8-domain ownership is:

- `architecture/ownership/modules.yaml` — top-level modules
- `architecture/ownership/application.yaml` — application-layer owned surfaces
- `architecture/ownership/infrastructure.yaml` — infrastructure adapters

Regenerate the aggregate via `go run ./cmd/architecture-aggregate`.

## Cross-references

- `AGENTS.md` — engineering rules + Git workflow + Velox-SSOT contract.
- `ARCHITECTURE.md` — current system design (system model, dependency
  zones, request + job flow, transactional outbox, media indexing).
- `CANONICAL.md` — authoritative source map for documentation.
- `architecture/policy.yaml` — executable architecture rules.
- `architecture/policy.history.yaml` — human-readable archive of past
  policy changes (the appendix to `policy.yaml`).

## Maintenance

This index is intentional — it does NOT duplicate the 5 godlike docs.
When a godlike doc changes:

1. Update the doc.
2. Update the SSOT map table above if the doc's purpose or hard-gate
   changes.
3. Run `cmd/archcheck --strict` to verify the policy still acknowledges
   the change.

The index is regenerated by hand; there is no auto-generate. Drift
between the index and the 5 docs is a known failure mode (caught by
manual review + the `cmd/archcheck --strict` exit semantics).
