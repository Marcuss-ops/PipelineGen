# PipelineGen — Product Validation Aggregator (Tests 9, 15, 17, 18)

> **Home**: questo aggregator vive nel repo operativo PipelineGen, NON nel core
> Chronon3d. Il core non deve sapere cosa sia una founder dashboard: la
> certificazione del motore (correctness, determinism, performance, memory,
> GPU, ABI) è in `Chronon3d/docs/tickets/TICKET-125-test-aggregator.md`.
>
> **Split 2026-08-28**: i Test 8, 9, 15, 17, 18 sono stati spostati qui dal
> TICKET-125 core per separare ENGINE CERTIFICATION da PRODUCT VALIDATION.

| **PV-AGG — Product Validation aggregator (Tests 9, 15, 17, 18)** | |
| --- | --- |
| **Stato** | PARTIAL (catalog landed; per-test runtime richiede esecuzione umana e/o working build host) |
| **Priorità** | P1 (product evidence; non bloccante per la certificazione del motore) |
| **Problema** | 5 deliverable di validazione prodotto (touchpoint manuali, pilot cliente, test prodotto, confronto Remotion, founder dashboard) erano aggregati nel TICKET-125 del core. Sono metriche di business/prodotto, non proprietà del motore: il core non deve sapere cosa sia una founder dashboard. |
| **Confine** | Docs + script operativi in questo repo. Nessuna API SDK Chronon3d. I tool leggono le superfici di telemetria esistenti (SQLite `~/.chronon3d/telemetry/telemetry.sqlite`, JSONL touchpoints, selftest logs) — nessun nuovo gate nel core. |
| **Criteri di accettazione** | 1) 5 righe presenti; 2) ogni riga ha un PASS criterion osservabile; 3) ogni riga ha uno stato onesto (no fabrication per §honesty); 4) §Product validation pointer nel TICKET-125 core aggiornato; 5) nessun riferimento core a founder dashboard. |

# Index — 5 deliverable

| # | Test scope | Deliverable | PASS criterion (osservabile) | Stato corrente |
|---|---|---|---|---|
| 8 | manual_touches_per_video counter | `apps/chronon3d_cli/utils/touchpoint/manual_touchpoint_log.{hpp,cpp}` + `--touchpoint <kind>` CLI flag + `~/.chronon3d/telemetry/touchpoints.jsonl` (nel core — il counter nasce lì, la metrica è consumata qui) | `chronon3d_cli telemetry query --metric manual_touches_per_video --last 1` exits 0 + counter non-null | DONE (counter wired nel core; consumo metrico qui) |
| 9 | Pilota cliente reale (7gg) | [`TEST-9-pilot-protocol.md`](TEST-9-pilot-protocol.md) + [`TEST-9-feedback-form.md`](TEST-9-feedback-form.md) + [`TEST-9-transcript-7gg.md`](TEST-9-transcript-7gg.md) | Transcript aggregate `transcripts/aggregate.md` con median Q1 ≥ +1 across ≥5 soggetti | **HARNESS-COMPLETE** (3 doc landed 2026-10-26) — pilot-runtime **DEFERRED** (CAT-9 richiede ≥5 clienti reali su 7 giorni reali, non machine-verifiable) |
| 15 | Test del prodotto (non del motore) | `TEST-15-PRODUCT-VALIDATION.md` + `fixtures/test-15-feedback.schema.json` + validator `scripts/product_validation.py` (migrato da `tools/`) | validator exit 0: ≥5 soggetti, median Q1/Q2 ≥1, median Q3 ≥10 min, ≥3 Q1 positivi, nessun Q3=0 | DOCS-READY + **VALIDATOR LANDED** (2026-10-26: exit 0/1/2 verificato su pass/fail/malformed) — evidenza reale ≥5 soggetti DEFERRED (§honesty) |
| 17 | Confronto diretto (Chronon3D / pipeline precedente / Remotion v4) | `TEST-17-COMPARISON.md` + `fixtures/test-17-comparison.schema.json` + validator `scripts/product_validation.py` (migrato da `tools/`) | validator exit 0: 24 celle, tier validi, 2 `[RADICAL W]`, 1 `[HONEST L]` | DOCS-READY + **VALIDATOR LANDED** (2026-10-26: exit 0/1/2 verificato; celle vuote = BLOCKED) — confronto reale 24 celle DEFERRED (§honesty) |
| 18 | Weekly founder dashboard (8 metriche) | [`TEST-18-WEEKLY-DASHBOARD.md`](TEST-18-WEEKLY-DASHBOARD.md) + `scripts/run_weekly_scorecard.sh` (migrato da `tools/`) | script exits 0 with tabella 8-row su telemetry SQLite/JSONL; rate costo esplicito via env | **OPEN (Weekly founder dashboard)** — aggregator + template landed 2026-10-26: `bash -n` PASS, selftest su DB seminato exit 0 con 8/8 metriche, contratto exit 2 su tool/DB assenti verificato. Prima run su telemetria reale = baseline settimanale (deferred al founder, §honesty) |

# Ticket operativi

| Forward-point | File | Stato |
|---|---|---|
| Test 9 pilota 7gg | [`TEST-9-pilot-protocol.md`](TEST-9-pilot-protocol.md) + [`TEST-9-feedback-form.md`](TEST-9-feedback-form.md) + [`TEST-9-transcript-7gg.md`](TEST-9-transcript-7gg.md) | HARNESS-COMPLETE; pilot-runtime OPEN/DEFERRED |
| Test 18 weekly dashboard | [`TEST-18-WEEKLY-DASHBOARD.md`](TEST-18-WEEKLY-DASHBOARD.md) + `scripts/run_weekly_scorecard.sh` | OPEN (Weekly founder dashboard) |

(gli ex `TICKET-TEST-9-PILOT-7GG.md` / `TICKET-TEST-18-WEEKLY-DASHBOARD.md` sono
stati assorbiti: le specifiche canoniche vivono nei doc sopra e in
`docs/stato/refactored.md` §4, workspace root.)

# Confine con il core

- Il core Chronon3d espone solo le superfici di telemetria (SQLite, JSONL,
  counter). Questo repo consuma quelle superfici per metriche di business.
- Il TICKET-125 core (ENGINE CERTIFICATION) non contiene righe di prodotto:
  punta a questo aggregator.
- Il core `tools/run_weekly_scorecard.sh` è stato migrato qui in
  `scripts/run_weekly_scorecard.sh` (2026-10-26; la directory `tools/` era
  stata eliminata dal commit `7e6965aab`). Anche il validator
  `tools/product_validation.py` vive ora in `scripts/product_validation.py`.

# §honesty cert

- Tutti i PASS criterion che richiedono soggetti umani (Test 9, 15) sono
  DEFERRED all'esecuzione reale — nessun dato sintetico spacciato per reale.
- `WEEKLY_COST_HOURLY_RATE` env var REQUIRED per la metrica 4 del Test 18;
  unset = `[UNSET-rate]` placeholder, mai un rate inventato.
- Test 17: cert tier `[EVIDENCED/SOURCED/ESTIMATED]` preservato; nessuna cella
  mascherata come evidenza.