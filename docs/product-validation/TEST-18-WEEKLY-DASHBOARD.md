# Test 18 — Weekly founder dashboard (template)

Template settimanale della founder dashboard (Test 18, product validation).
L'aggregator canonico è [`scripts/run_weekly_scorecard.sh`](../../scripts/run_weekly_scorecard.sh)
(migrato dal core `tools/run_weekly_scorecard.sh`, purge `7e6965aab`).

## Uso (cadence: venerdì pomeriggio)

```bash
WEEKLY_COST_HOURLY_RATE=<rate> bash scripts/run_weekly_scorecard.sh >> /tmp/weekly.md
```

Exit `0` = tabella emessa su telemetry SQLite popolata; exit `2` = tool o DB
mancanti (bootstrap: eseguire `chronon3d_cli` una volta per creare
`telemetry.sqlite`). Ogni voce qui sotto è **FILL-IN-THE-BLANK**: va compilata
con l'output reale dell'aggregator, mai con dati sintetici.

## Sample weekly entry (da compilare ogni venerdì)

**Week**: `____-__-__` to `____-__-__`

| # | Metric | Value | Aggregation source |
|---|---|---|---|
| 1 | videos_completed | `[FILL]` | COUNT(DISTINCT composition_id) status='DONE', 7d |
| 2 | failure_rate | `[FILL]` | FAILED / total renders, 7d |
| 3 | manual_touches_per_video | `[FILL]` | touchpoints.jsonl + render_counters, 7d |
| 4 | cost_per_finished_minute | `[FILL]` | WEEKLY_COST_HOURLY_RATE × render_min / 60 |
| 5 | p95_render_time | `[FILL]` | p95 renders.render_ms, 7d |
| 6 | peak_memory | `[FILL]` | MAX(render_counters.framebuffer_bytes_peak), 7d |
| 7 | deterministic_hash_failures | `[FILL]` | GATE_FAIL in selftest logs |
| 8 | bbox_contract_violations | `[FILL]` | SUM(render_counters.text_bbox_contract_violations), 7d |

## 7 narrative lines (il grep settimanale del founder)

1. **Quanti video**: `[FILL]` video completati questa settimana.
2. **Costo**: `[FILL]` per minuto finito (rate dichiarato via `WEEKLY_COST_HOURLY_RATE`).
3. **Job falliti**: `[FILL]` di `[FILL]` totali (`[FILL]%`).
4. **Interventi manuali**: `[FILL]` per video (target golden path: < 1).
5. **Codice eliminato**: registro sunset non instrumentato in questa dashboard (forward-point; Test 16).
6. **Bug piu grave**: `Chronon3d/docs/FOLLOWUP_TICKETS.md` §Open Blockers — `[FILL: ticket id]`.
7. **Metrica migliorata**: `[FILL: week-over-week delta]` (richiede 2 settimane di dati; la prima run semina la baseline).

## Prima run — baseline settimanale (2026-10-28, source `pipelinegen`)

Comando: `WEEKLY_SCORECARD_SOURCE=pipelinegen bash scripts/run_weekly_scorecard.sh`
(exit 0, 8/8 metriche). Settimana 2026-10-21 → 2026-10-28.

| # | Metric | Value | Aggregation source |
|---|---|---|---|
| 1 | videos_completed | **95** | `run_observability`: COUNT(DISTINCT job_id) `script.generate` SUCCEEDED, 7d |
| 2 | failure_rate | **40.64%** | famiglia video (`script.generate`, `script.generate_item`) FAILED/total, 7d |
| 3 | manual_touches_per_video | 0.00 | touchpoints.jsonl (nessun touchpoint registrato) |
| 4 | cost_per_finished_minute | `[UNSET-rate]` | rate non dichiarato dal founder: placeholder onesto |
| 5 | p95_render_time | 485.561 s | `run_observability.wall_time_ms` ASC, p95 |
| 6 | peak_memory | `[no-source]` | contatore assente su questa superficie |
| 7 | deterministic_hash_failures | 0 | grep GATE_FAIL in `~/.chronon3d/selftest_log/` |
| 8 | bbox_contract_violations | `[no-source]` | contatore assente su questa superficie |

Nota fonte: lo store SQLite `~/.chronon3d/telemetry/telemetry.sqlite` della
superficie contratta **non può esistere** sulle build post Wave 1D (demolizione
dello store, guard irreversibile `Chronon3d/tools/check_no_sqlite.sh`, engine
events-only). L'aggregator supporta quindi `WEEKLY_SCORECARD_SOURCE=pipelinegen`
che legge la telemetria reale (`data/observability/api_requests.db.sqlite`:
`run_observability`, `run_resource_aggregates`); il default `chronon3d` e il
contratto exit 2 su DB assente restano invariati. Le metriche senza superficie
di contatore sono `[no-source]`, mai zeri fabbricati.

## §honesty cert

- `WEEKLY_COST_HOURLY_RATE` è **REQUIRED** per la metrica 4; unset = placeholder
  `[UNSET-rate]`, mai un rate inventato (§honesty "non inventare").
- Tutte e 8 le metriche derivano da superfici di telemetria osservabili
  (SQLite `~/.chronon3d/telemetry/telemetry.sqlite` oppure, con
  `WEEKLY_SCORECARD_SOURCE=pipelinegen`, `data/observability/api_requests.db.sqlite`;
  JSONL touchpoints; selftest logs): nessun valore sintetico passato per reale.
- Le voci di questo template sono impegni FILL-IN-THE-BLANK, non dati reali
  precompilati: la prima run dell'aggregator semina la baseline.
- macchina-verifica end-to-end su DB popolato: **DEFERRED** al primo run del
   founder (`bash -n` syntax-check PASS; il contratto exit 0/2 è documentato
  nell'header dello script).

## Cross-link

- [`PRODUCT-VALIDATION-AGGREGATOR.md`](PRODUCT-VALIDATION-AGGREGATOR.md) row 18
- [`scripts/run_weekly_scorecard.sh`](../../scripts/run_weekly_scorecard.sh) — aggregator canonico (8 metriche)
- `docs/stato/refactored.md` §4 (workspace root) — stato product validation
