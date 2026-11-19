# TICKET — Shared packages: dead defaults, guards and small helpers (2026-11-19)

- **Status:** CLOSED (2026-11-19) — 0 unreachable functions under `pkg/`; guard 445 → 396
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

## Esito (2026-11-19)

**Decisione guardie HMAC (2026-11-19).** `hmacsign` è il lato *producer*: solo
`delivery.go` lo usa, e solo `Sign`. Nessun ingresso del repository legge
`X-Velox-Signature`, quindi la verifica appartiene al ricevitore esterno.
Ritirati `Verify`, `VerifyMultipleSecrets`, `VerifyWithReplayWindow`,
`subtleEqual`, `ErrInvalidSignature`, `ErrStaleTimestamp` e i relativi test; il
formato canonico (`<timestamp>.<event_id>.<body>`, prefisso `sha256=`) resta
documentato nel package per un futuro ricevitore in-process, che dovrà nascere
con test di body alterato e segreto ruotato.

**Decisione guardia URL (2026-11-19).** `ValidateVideoID` non aveva caller:
l'ingresso YouTube passa da `security.ValidateDownloadURL` + yt-dlp. `AddAllowedHost`
era un entry point usato solo dai test (violazione della regola test-support):
ritirato, i tre test interessati ora usano `SetAllowedHosts`, che è la superficie
chiamata dal composition root.

**Eccezione: `defaults.Validate` non è stato cancellato, è stato ricablato.** Il
suo doc dichiarava la chiamata da `internal/platform/config/config.go::Validate()`;
la chiamata era sparita e il validatore SSOT era decaduto a codice morto. Ora
`config.Validate()` lo invoca come primo controllo, quindi `Validate`,
`validateSSOTs`, `DefaultMediaConfig`, `DefaultYouTubeConfig` e
`DefaultVoiceoverConfig` sono di nuovo raggiungibili e il boot fallisce su un
SSOT rotto invece di degradare in silenzio.

**Ritirati (21):** `defaults.Float64/Duration/Truthy`, `hmacsign.Verify*` (4),
`security.ValidateVideoID/AddAllowedHost`, `retry.asAdapter/Retryable/DefaultOptions`,
`termutil.defaultOpts/TermsFromFields/CleanTerms/LooksLikePersonName`,
`localeutil.ParsedLocale.String/IsValid`, `veloxclient.RouteMediaClipsFor/RouteMediaResolve`,
`background.InFlight` (con il contatore write-only `inFlightCount`),
`textutil.TokenizeWithStopWords`.

**Catena immutability (ondata 6).** `pkg/immutability.CloneWith` era raggiungibile
solo da `voiceover/service/types_helpers.go::normalizeBatchRequest`, a sua volta
morto (nessun handler costruisce più `BatchRequest`: la pipeline è port-driven).
Rimossi insieme: i due normalizzatori, `voiceOverridesFromMetadata`, i due test
`TestNormalizeBatchRequest_*` e l'intero `pkg/immutability/`.

Test riscritti senza indebolire le asserzioni: i test `retry` usano `errors.As`
diretto, i test `background` leggono il registry invece del contatore esportato,
`localeutil` asserisce `BCP47` al posto del metodo `String()` rimosso.

**Verifica:** `grep '^pkg/' deadcode -test=false ./...` → vuoto; `go build ./...`
OK; `go test ./pkg/...` OK; guard globale 445 → 396.
