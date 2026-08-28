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
| 9 | Pilota cliente reale (7gg) | `docs/product-validation/TEST-9-pilot-protocol.md` + `TEST-9-feedback-form.md` + `TEST-9-transcript-7gg.md` (harness da creare) | Transcript aggregate `transcripts/aggregate.md` con median Q1 ≥ +1 across ≥5 soggetti | HARNESS-MISSING → [TICKET-TEST-9-PILOT-7GG](TICKET-TEST-9-PILOT-7GG.md) |
| 15 | Test del prodotto (non del motore) | `TEST-15-PRODUCT-VALIDATION.md` + `fixtures/test-15-feedback.schema.json` + `tools/product_validation.py` | validator exit 0: ≥5 soggetti, median Q1/Q2 ≥1, median Q3 ≥10 min, ≥3 Q1 positivi, nessun Q3=0 | READY (evidence input required) |
| 17 | Confronto diretto (Chronon3D / pipeline precedente / Remotion v4) | `TEST-17-COMPARISON.md` + `fixtures/test-17-comparison.schema.json` + `tools/product_validation.py` | validator exit 0: 24 celle, tier validi, 2 `[RADICAL W]`, 1 `[HONEST L]` | READY (evidence input required) |
| 18 | Weekly founder dashboard (8 metriche) | `scripts/run_weekly_scorecard.sh` + `TICKET-TEST-18-WEEKLY-DASHBOARD.md` | script exits 0 with tabella 8-row su telemetry SQLite/JSONL; rate costo esplicito via env | READY (live telemetry required) |

# Ticket operativi

| Forward-point slug | File | Stato |
|---|---|---|
| TICKET-TEST-9-PILOT-7GG | `docs/product-validation/TICKET-TEST-9-PILOT-7GG.md` | OPEN |
| TICKET-TEST-18-WEEKLY-DASHBOARD | `docs/product-validation/TICKET-TEST-18-WEEKLY-DASHBOARD.md` | OPEN |

# Confine con il core

- Il core Chronon3d espone solo le superfici di telemetria (SQLite, JSONL,
  counter). Questo repo consuma quelle superfici per metriche di business.
- Il TICKET-125 core (ENGINE CERTIFICATION) non contiene righe di prodotto:
  punta a questo aggregator.
- `tools/run_weekly_scorecard.sh` è stato spostato qui in `scripts/`
  (2026-08-28) — il core non deve sapere cosa sia una founder dashboard.

# §honesty cert

- Tutti i PASS criterion che richiedono soggetti umani (Test 9, 15) sono
  DEFERRED all'esecuzione reale — nessun dato sintetico spacciato per reale.
- `WEEKLY_COST_HOURLY_RATE` env var REQUIRED per la metrica 4 del Test 18;
  unset = `[UNSET-rate]` placeholder, mai un rate inventato.
- Test 17: cert tier `[EVIDENCED/SOURCED/ESTIMATED]` preservato; nessuna cella
  mascherata come evidenza.