# TICKET — Media eligibility: wire the SSOT port or retire the second implementation (2026-11-19)

- **Status:** CLOSED (2026-11-19) — chose retire: the live reindex path already applies the shared rule; the duplicate port and its PostgreSQL reader are deleted
- **Severity:** P2 — two implementations of one searchability rule, one of them without a caller
- **Owner:** media search / index owner (MEDIA-SSOT P2-9)
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

`ResolveIndexEligibility` has no caller. Its consumer-owned port
(`AssetEligibilityReader`, `ErrTaxonomySchemaUnavailable`) exists only for it, and
`internal/platform/postgres/media/eligibility_reader.go` is the only
implementation. Meanwhile `internal/platform/postgres/media/reindex_requester.go`
contains the same decision natively and its comment says it "mirrors
mediaregistry.ResolveIndexEligibility".

The design is documented as active (MEDIA-SSOT P2-9) but the code is not on a
path: there are two copies of the rule and neither test compares them. The
ticket is a decision, not a deletion: either the port is the single owner and
`reindex_requester` consumes it, or the port is the duplicate and goes.

## Scope (measured)

| File | # |
|---|---:|
| `internal/platform/postgres/media/eligibility_reader.go` | 3 |
| `internal/capabilities/mediaregistry/index_eligibility_resolver.go` | 1 |
| **Total** | **4** |

Key symbols: `ResolveIndexEligibility`, `NewMediaEligibilityReader`, `MediaEligibilityReader.AssetTaxonomy`

## Plan

1. Read `reindex_requester.go` and `index_eligibility_resolver.go` side by side
   and list every divergence (nil reader, missing taxonomy schema, default
   eligibility).
2. Option A — wire: make `reindex_requester` call the port, add a parity test
   covering the nil-reader and missing-schema branches, and keep
   `eligibility_reader_test.go`.
3. Option B — retire: delete `index_eligibility_resolver.go`,
   `eligibility_reader.go` and its test, and record that `reindex_requester` is
   the single owner of the rule; update the archcheck comment that names the
   port in `percheck_sqlite_media_reader_ban.go`.
4. Record the dated decision in this ticket.

## Acceptance criteria

- [ ] One implementation of eligibility remains, or the two are proven
      equivalent by a test that exercises both.
- [ ] No comment claims a port that nothing calls.
- [ ] `go build ./...`, `go vet` on the touched packages and their tests are green.
- [ ] The global deadcode guard records the new count.

## Closeout (2026-11-19)

**Decision: option B — retire the port.** The searchability rule is live in
`mediaregistry.AssetTaxonomy.IndexEligibility` and is already applied inside the
PostgreSQL transaction by `internal/platform/postgres/media/reindex_requester.go`.
The deleted resolver was a second copy of the same decision, and the archcheck
boundary note already described it as retired, so the file was the leftover.

- Deleted `internal/capabilities/mediaregistry/index_eligibility_resolver.go`
  (`AssetEligibilityReader`, `ErrTaxonomySchemaUnavailable`,
  `ResolveIndexEligibility`) and
  `internal/platform/postgres/media/eligibility_reader.go` with its test.
- Updated the two comments that pointed at the port: the reindex requester now
  names the shared taxonomy rule, and the archcheck note records the deletion
  and why the port could not prove parity with the live path.
- `IndexEligibility` and `AssetTaxonomy.IndexEligibility()` stay: the reindex
  requester calls them, so the rule has one owner and one consumer.

## Verification

```sh
rg -n 'ResolveIndexEligibility|AssetEligibilityReader' --glob '*.go' --glob '*.md'
go test ./internal/platform/postgres/media ./internal/capabilities/mediaregistry -count=1
```
