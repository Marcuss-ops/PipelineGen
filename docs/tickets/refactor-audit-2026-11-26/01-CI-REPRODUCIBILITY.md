# 01 — CI riproducibile: Queue, BrainRust, mediaexec

**Data:** 2026-11-26
**Priorità:** P0
**Stato:** **DONE** (codice e gate locali) — la prova su GitHub Actions richiede
una push
**Area:** `.github/`, `architecture/`, `make/`, `Dockerfile`, `scripts/ci/`

## 1. Sintomo e causa

I tre workflow rossi su `main` avevano tre cause distinte, tutte di
risoluzione delle dipendenze esterne, non di codice Go.

| Sintomo CI | Causa reale |
| --- | --- |
| Checkout del modulo Queue fallito | l'action clonava `Marcuss-ops/RenderingGen.git`, che non esiste: il repository pubblicato è `Marcuss-ops/RenderginGen.git` |
| `bin/BRAINRUST_RELEASE.json` mancante | `bin/` è in `.gitignore` (riga 86): il file non è mai stato in un checkout fresco |
| contratto `mediaexec.v2` fallito | `RUST_CONTRACT_COMMIT` veniva da `git ls-tree HEAD rust`, ma il repository non ha gitlink |
| annotazione release ICU fallita | il job leggeva `BrainRust/ICU_MAJOR` senza aver mai fatto il checkout di BrainRust |
| lint / vuln / verify-main non eseguiti | conseguenza dei passi precedenti |

Prova del nome del repository:

```text
$ git ls-remote https://github.com/Marcuss-ops/RenderingGen.git HEAD
fatal: could not read Username for 'https://github.com': No such device or address

$ git ls-remote https://github.com/Marcuss-ops/RenderginGen.git HEAD
e8fec015d28167b84ccd94ba6e002de43518fc6f	HEAD
```

## 2. Checkout Queue: nome giusto e revisione fissata

`.github/actions/checkout-renderinggen/action.yml`:

- repository corretto (`RenderginGen`), con override `RENDERINGGEN_REPO`;
- la revisione viene da `architecture/releases.json` (`renderinggen.git_sha`),
  non da un branch mobile;
- fetch per SHA con fallback alla storia completa, `checkout --detach`, e
  **verifica** che l'HEAD ottenuto sia esattamente lo SHA richiesto (fail
  closed);
- il commento in `go.mod` nomina ora il repository reale, così il prossimo
  lettore non ricade nell'errore.

La revisione fissata è `e8fec015`, l'ultima **pubblicata**. Il sibling locale è
avanti di due commit non pubblicati (`df9f9f7`, `b9135bf`) e un fetch per SHA di
`b9135bf` risponde `not our ref`: fissarlo avrebbe reso la CI rossa in modo
deterministico.

Compatibilità verificata contro la revisione pubblicata:

```text
$ go mod edit -replace github.com/Marcuss-ops/RenderingGen/queue=<e8fec015>
$ go build ./...   → exit 0
$ go vet ./...     → exit 0
$ go mod tidy      → nessun diff su go.mod/go.sum
```

## 3. Pin BrainRust unico e fetchabile

Nuovo file tracciato `architecture/releases.json`:

```json
"brainrust": {
  "version": "v0.3.8",
  "git_sha": "a4ab4cb65af0fffd93966f27738ae58626fce2a9",
  "icu_major": 72,
  "mediaexec_schema": "mediaexec.v2",
  "semantic_schema": "semantic_manifest.v3"
}
```

`a4ab4cb` è il commit del tag pubblicato `v0.3.8`; il manifest locale
`bin/BRAINRUST_RELEASE.json` dichiarava `644f1ce` con `"source": "worktree"` —
un build locale non pubblicato, che nessun runner può fetchare.

Un solo lettore per tutti i consumatori: `scripts/ci/release_pin.py`.
Consumatori agganciati:

| Consumatore | Prima | Ora |
| --- | --- | --- |
| `make/build.mk` `BRAINRUST_VERSION` | literal `v0.3.8` | pin |
| `make/build.mk` `ICU_MAJOR` | `../BrainRust/ICU_MAJOR`, poi pkg-config | pin |
| `Dockerfile` `ARG BRAINRUST_VERSION` | **`v0.3.0`** | `v0.3.8` + guard |
| `make verify-rust-contract` | `git ls-tree HEAD rust` | `brainrust.git_sha` |
| `ci.yml` `lexicon-parity` | `refactored/bin/BRAINRUST_RELEASE.json` | `refactored/architecture/releases.json` |
| `icu-runtime.yml` | `BrainRust/ICU_MAJOR` non presente | checkout BrainRust al pin + confronto |

Il `Dockerfile` dichiarava `v0.3.0`: un container build avrebbe scaricato un
release che il contratto corrente rifiuta (il commento di `rust-fetch.sh` lo
dice esplicitamente). Ora il default è allineato e `make verify-release-pin`
fallisce se Dockerfile e pin divergono.

Comandi:

```text
$ make verify-release-pin
✅ Release pin verified (v0.3.8 @ a4ab4cb65af0fffd93966f27738ae58626fce2a9, ICU 72)

$ make verify-rust-contract
✅ Release pin verified (...)
✅ mediaexec.v2 contract: 4 shared goldens validated
✅ consumer goldens match contract/mediaexec.v2/goldens
ok  github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec
```

`icu-runtime.yml` ora: risolve il pin → checkout `Marcuss-ops/BrainRust` allo
SHA → installa l'ICU → self-test `rust-fetch` → verifica che gli `ARG` del
Dockerfile siano uguali al pin → verifica che `BrainRust/ICU_MAJOR` sia uguale
al pin.

## 4. Acceptance

1. [x] Il checkout Queue usa il nome reale del repository.
2. [x] Il checkout Queue è fissato a una revisione **pubblicata** e fallisce se
       l'HEAD non coincide.
3. [x] `go build`, `go vet`, `go mod tidy` sono verdi contro la revisione
       fissata.
4. [x] Esiste un solo owner dei pin esterni, tracciato in Git, e tutti i
       consumatori lo leggono.
5. [x] `verify-rust-contract` non legge più un gitlink inesistente e passa
       end-to-end (network incluso).
6. [x] Il job ICU è autoconsistente: nessun path che il job non ha creato.
7. [x] `Dockerfile` e pin non possono divergere senza un gate rosso.
8. [ ] **La CI GitHub è verde.** Non verificabile senza push: nessun commit è
       stato pushato in questa sessione.

## 5. Residui

- **Push e primo run.** Il pin `e8fec015` e il pin `v0.3.8` sono scelti per
  essere fetchabili; il primo run è la prova.
- **I sibling locali sono avanti.** Quando `RenderingGen` pubblica `df9f9f7` e
  `b9135bf`, e `BrainRust` pubblica il proprio worktree, i pin vanno spostati
  con `make verify-release-pin` + `verify-rust-contract` nello stesso cambio.
- **`bin/BRAINRUST_RELEASE.json` resta un artefatto** (non tracciato). È
  corretto: registra i binari installati su questo host, non la revisione del
  contratto.
