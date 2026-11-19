# TICKET — Retire or wire the dead media-registry surface (2026-11-19)

- **Status:** OPEN (2026-11-19) — 12 unreachable functions in `internal/capabilities/mediaregistry`; none has a production caller
- **Severity:** P2 — the validation and hashing guards ship in the binary but no request can reach them
- **Owner:** media registry / catalog owner
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

The media registry package still declares the projection contract guards
(`ValidateProjectionTransition`, `ValidateProjectionSequence` and their error
formatters), the editing-assets policy parser (`ParseEditingAssetsPolicy`,
`EditingAssetsPolicy.SelectTransitionSFX`), the editorial audio catalog helpers
(`EditorialTransitionAliases`, `ValidateEditorialAudioCatalog`), three content
hashing helpers (`Fingerprint`, `NormalizeContentSHA256`, `ValidateContentSHA256`)
and `DeriveAssetSourceID`.

They are not equivalent. The projection guards and the policy/catalog helpers
are leftovers of a state machine whose ledger was retired on 2026-11-18 with the
SQLite `mediaregistry` package. The hash helpers are different: together with
`ErrInvalidContentSHA256` they are a fail-closed guard that no write path calls,
so deleting them removes the only check that a content address is canonical.
That guard needs a written decision, not a deletion driven by a count.

## Scope (measured)

| File | # |
|---|---:|
| `internal/capabilities/mediaregistry/contract.go` | 4 |
| `internal/capabilities/mediaregistry/hashes.go` | 3 |
| `internal/capabilities/mediaregistry/editing_assets_policy.go` | 2 |
| `internal/capabilities/mediaregistry/editorial_catalog.go` | 2 |
| `internal/capabilities/mediaregistry/canonical_identity.go` | 1 |
| **Total** | **12** |

Key symbols: `ValidateContentSHA256` + `ErrInvalidContentSHA256` (guard, decide), `DeriveAssetSourceID`, `Fingerprint`

## Plan

1. Confirm each symbol with `rg -w <symbol>` across the module, including
   interface satisfaction and `MethodByName`.
2. Retire the projection guards with `projection_test.go`, and the policy and
   catalog helpers with `editing_assets_policy_test.go` and
   `editorial_catalog_test.go`; trim `canonical_resolution_test.go` if it covers
   `DeriveAssetSourceID`.
3. Decide the hashes explicitly. Either wire `ValidateContentSHA256` where
   content links are written (with a test that stores a non-canonical digest and
   fails closed) or record in this ticket that the SQL column is the only owner
   and delete `hashes.go` with `hashes_test.go`.
4. Re-measure and lower the guard in `DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md`
   and `TICKET-DEAD-CODE-SWEEP-2026-11-18.md` by exactly the deleted count.

## Acceptance criteria

- [ ] Every symbol above is either deleted with its dedicated test or has a
      written reason and an owner to stay.
- [ ] The hashes decision is dated in this ticket (wire / column owner).
- [ ] `go build ./...`, `go vet ./internal/capabilities/mediaregistry` and
      `go test ./internal/capabilities/mediaregistry -count=1` are green.
- [ ] The global deadcode guard records the new count.

## Verification

```sh
rg -w 'ValidateProjectionTransition|SelectTransitionSFX|ValidateContentSHA256|DeriveAssetSourceID' --glob '*.go'
deadcode -test=false ./... | grep '^internal/capabilities/mediaregistry/'
go test ./internal/capabilities/mediaregistry -count=1
```
