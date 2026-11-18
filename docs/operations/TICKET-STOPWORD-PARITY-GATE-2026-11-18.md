# TICKET — Stopword parity gate is broken and `config/stopwords` is orphaned (2026-11-18)

- **Status:** CLOSED (2026-11-18) — gate repointed to the Go projection, orphan data deleted, wired into `verify-agent`/`verify-release` and into the `lexicon-parity` CI job at the pinned BrainRust SHA
- **Severity:** P1 — a documented PASS is false and the remaining Go stopword data is unreferenced
- **Owner:** linguistics / lexicon
- **Source:** audit 2026-11-18; `BrainRust/lexicon/tools/check_go_stopword_parity.py`; `docs/stato/brainrust.md:93`
- **Related:** BrainRust TICKET-18 (linguistics Go demolition), TICKET-13 (Go demolition closeout)
- **Rule:** a gate that cannot pass is either repaired against the new authority or retired with the data it guards; it cannot stay as documentation

## Problem

The parity gate compares two paths:

```text
refactored/config/lexicons/<lang>/stopwords.txt   # Go consumer
BrainRust/lexicon/packs/<lang>.json               # Rust authority
```

The Go directory was removed during the linguistics cutover
(`refactored@eb65a6a30`, "cut over to the Rust-owned language profiles"). Only
`refactored/config/stopwords/{en,es,it,pt}.txt` remains, and no Go, Rust,
Python, Make or YAML file references it.

Running the gate today:

```text
$ python3 BrainRust/lexicon/tools/check_go_stopword_parity.py --repo-root .
FAIL: de: missing Go stopword file .../refactored/config/lexicons/de/stopwords.txt
FAIL: en: ...
FAIL: es: ...
FAIL: fr: ...
FAIL: it: ...
FAIL: pt: ...
rc=1
```

So:

- the gate is **not** wired into CI (only docs and `make`-adjacent prose mention
  it), which is why it rotted silently;
- `docs/stato/brainrust.md:93`, `BrainRust/docs/tickets/README.md:128,280` and
  `docs/stato/PIANO-CHIUSURA-TICKET-RUST-GO-2026-11-16.md:348` still claim
  "PASS, 0 drift on the six languages";
- `refactored/config/stopwords/` (20 KB, four files) is dead data that suggests
  a Go stopword authority that no longer exists;
- the real Go consumer today is the embedded projection
  `internal/capabilities/linguistics/profiles.generated.json` (53 KB,
  `//go:embed` in `lexicon_registry.go`), which declares the Rust lexicon as
  its source.

## Decision to record

**Recommended: repoint the gate, do not retire the idea.**

1. Compare `refactored/internal/capabilities/linguistics/profiles.generated.json`
   (the projection the Go binary actually embeds) against
   `BrainRust/lexicon/packs/<lang>.json` for every shared language.
2. Keep the fail-closed semantics: a word present on one side only is drift;
   a language missing from either side is a failure unless explicitly listed.
3. Delete `refactored/config/stopwords/` and the stale path in the script.
4. Wire the gate into the same lane that runs the other Rust↔Go parity gates
   (`make verify-agent` / the rust contract lane), so it cannot rot again.

Alternative, if the projection is considered generated-only and not worth
guarding: retire the gate and delete the `config/stopwords/` data, and remove
every "PASS, 0 drift" claim from the docs. This is acceptable only with a
written note that the parity responsibility moved into the generator
(`lexicon/tools/build_language_packs.py`).

## Acceptance criteria

- [x] The decision is recorded here with date and owner.
- [x] If repointed: the gate reads `profiles.generated.json` + Rust packs,
      exits 0 on the current tree, and exits 1 when one word is injected on
      either side (red test, green test).
- [x] If retired: the script is deleted, `config/stopwords/` is deleted, and no
      document claims the gate passes.
- [x] The gate (or its replacement) runs in CI; the lane and step name are
      written here.
- [x] `profile.generated.json` regeneration and the gate agree: running the
      Python generator then the gate produces no diff.
- [x] `rg -n "config/lexicons"` returns no live reference: only immutable
      historical job logs under `ops/jobs/` still mention the retired path.

## Verification

```sh
python3 BrainRust/lexicon/tools/check_go_stopword_parity.py --repo-root .
python3 BrainRust/lexicon/tools/build_language_packs.py        # regenerate
git diff --exit-code refactored/internal/capabilities/linguistics/profiles.generated.json
go test ./internal/capabilities/linguistics/... -count=1
```

## Execution log — 2026-11-18

Repointed to the Go projection (`profiles.generated.json`) and the Rust packs:

```text
de: go=75 rust=75 … it: go=129 rust=129 … tr: go=0 rust=0
fallback: go=26 (Go-only by design)
PASS: 0 drift across 11 shared languages (de, en, es, fr, id, it, nl, pl, pt, ru, tr)
```

- red test: one injected word in the `en` projection → `rc=1` with
  `Go stopwords missing from Rust pack: ['zz-gate-selftest']`;
- green test after restore → `rc=0`;
- `refactored/config/stopwords/` (20 KB, en/es/it/pt) deleted;
- `make verify-lexicon-parity` added and wired into `verify-agent` and
  `verify-release`;
- `make verify-lexicon-parity` → `✅ Lexicon parity verified`.

CI: the `lexicon-parity` job in `.github/workflows/ci.yml` checks out the Go
repo and BrainRust at the release SHA recorded in
`bin/BRAINRUST_RELEASE.json`, then runs the same script with `--repo-root .`.
The local `make verify-lexicon-parity` target still fails closed with an
explicit message when `../BrainRust/lexicon/packs` is missing (for example a
checkout with `submodules: false`).

## Notes

`config/stopwords/` covers only `en, es, it, pt`; the Rust packs cover ten
languages. Even if the old gate were pointed at it, four of the six shared
languages (`de, fr`) and the fallback set would be missing. That asymmetry is
why repointing to the generated projection is the correct repair rather than a
path change.

## CI lane — 2026-11-18

`.github/workflows/ci.yml` job `lexicon-parity`:

1. checks out `refactored` with `submodules: false`;
2. reads the BrainRust commit from `bin/BRAINRUST_RELEASE.json` (`git_sha`);
3. checks out `Marcuss-ops/BrainRust` at that SHA into `BrainRust/`;
4. runs `python3 BrainRust/lexicon/tools/check_go_stopword_parity.py --repo-root .`.

The gate therefore compares the exact pair the release binaries were built
from. Local lane remains `make verify-lexicon-parity` (wired into
`verify-agent` and `verify-release`).
