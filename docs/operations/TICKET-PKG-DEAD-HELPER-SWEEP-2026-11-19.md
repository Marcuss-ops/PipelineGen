# TICKET — Shared packages: dead defaults, guards and small helpers (2026-11-19)

- **Status:** OPEN (2026-11-19) — 27 unreachable functions across `pkg/`
- **Severity:** P2 — two of the groups are guards (HMAC verification, URL validation) with no caller
- **Owner:** platform library owner
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

`pkg/defaults` contributes 7 unused defaults (`Float64`, `Duration`, `Truthy`,
`Validate`, `validateSSOTs`, `DefaultMediaConfig`, `DefaultYouTubeConfig`), the
residue of an SSOT-defaults layer whose consumers build their own values;
`DefaultVoiceoverConfig` is counted in the voiceover ticket.

`pkg/hmacsign` contributes the entire verification side: `Verify`,
`VerifyMultipleSecrets`, `VerifyWithReplayWindow`, `subtleEqual`. `pkg/security`
contributes `ValidateVideoID` and `AddAllowedHost`. Both are guards: deleting
them removes a check that something signed or a URL is acceptable. They need an
explicit owner decision, exactly like the media-registry hashes.

The rest are ordinary leftovers: `pkg/retry` (3), `pkg/termutil` (4),
`pkg/localeutil` (2), `pkg/veloxclient` (2), `pkg/background`,
`pkg/immutability`, `pkg/textutil` (one each).

## Scope (measured)

| File | # |
|---|---:|
| `pkg/defaults/defaults.go` | 5 |
| `pkg/hmacsign/signer.go` | 4 |
| `pkg/termutil/terms_from_text.go` | 3 |
| `pkg/localeutil/locale.go` | 2 |
| `pkg/security/url.go` | 2 |
| `pkg/veloxclient/routes.go` | 2 |
| `pkg/background/detach.go` | 1 |
| `pkg/defaults/media.go` | 1 |
| `pkg/defaults/youtube.go` | 1 |
| `pkg/immutability/copy.go` | 1 |
| `pkg/retry/decision.go` | 1 |
| `pkg/retry/errors.go` | 1 |
| `pkg/retry/options.go` | 1 |
| `pkg/termutil/extract_likely_names.go` | 1 |
| `pkg/textutil/textutil.go` | 1 |
| **Total** | **27** |

Key symbols: `hmacsign.Verify*` (guard), `security.ValidateVideoID` (guard), `defaults.Duration`

## Plan

1. Decide the two guard groups first: wire them at the ingress that receives
   signed payloads / video URLs, or record that verification lives in the Rust
   worker or the edge and delete them.
2. Delete the unused `pkg/defaults` values with `defaults_test.go` cases that
   only cover them; keep the SSOT validation if `Validate` is called anywhere.
3. Sweep `retry`, `termutil`, `localeutil`, `veloxclient`, `background`,
   `immutability`, `textutil` one package per commit.
4. Re-run `go test ./pkg/...` and lower the guard.

## Acceptance criteria

- [ ] The HMAC and URL-guard decisions are dated in this ticket.
- [ ] 0 unreachable functions under `pkg/`.
- [ ] `go build ./...` and `go test ./pkg/... -count=1` are green.
- [ ] The global deadcode guard records the new count.

## Verification

```sh
deadcode -test=false ./... | grep '^pkg/'
go test ./pkg/... -count=1
```
