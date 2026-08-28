<table>
<tr><th colspan="2">TICKET-TEST-18-WEEKLY-DASHBOARD — Test 18 Weekly founder dashboard (product validation)</th></tr>
<tr><th>Stato</th><td>OPEN (moved from Chronon3d core TICKET-128 — founder dashboard is a business metric, not engine certification)</td></tr>
<tr><th>Priorità</th><td>P1 (active; founder weekly cadence)</td></tr>
<tr><th>Problema</th><td>Test 18 (Weekly founder dashboard — 8 metriche) è un aggregatore di business metrics dal telemetry del motore. Non appartiene al core Chronon3D: il core non deve sapere cosa sia una founder dashboard. L'aggregator <code>scripts/run_weekly_scorecard.sh</code> è stato spostato in PipelineGen (questo repo) e il template dashboard vive qui sotto <code>docs/product-validation/</code>.</td></tr>
<tr><th>Evidenza</th><td>PRODUCT-VALIDATION-AGGREGATOR row 18. Script migrato da Chronon3d core <code>tools/run_weekly_scorecard.sh</code> → <code>scripts/run_weekly_scorecard.sh</code> (bash -n PASS). Template <code>docs/product-validation/TEST-18-WEEKLY-DASHBOARD.md</code> da creare.</td></tr>
<tr><th>Impatto</th><td>Senza un aggregator canonico, il founder non ha una cadenza settimanale osservabile. Il core Chronon3D resta pulito: telemetry SQLite esiste nel motore, ma l'aggregazione/lettura è responsabilità di PipelineGen.</td></tr>
<tr><th>Confine</th><td>Pure <code>scripts/</code> + <code>docs/product-validation/</code> artifacts in PipelineGen (no Chronon3d SDK API surface; no <code>include/chronon3d/</code> edits). L'aggregator interroga il telemetry SQLite esistente (<code>~/.chronon3d/telemetry/telemetry.sqlite</code>) — non è un gate pre-push del core.</td></tr>
<tr><th>Soluzione accettabile</th><td><strong>SPEC CANONICAL (Test 18 = weekly founder dashboard)</strong>. <strong>8 metrics</strong>: (1) <code>videos_completed</code> count over 7d window; (2) <code>failure_rate</code> FAILED/total over 7d; (3) <code>manual_touches_per_video</code> avg over 7d (Test 8 wire-up); (4) <code>cost_per_finished_minute</code> requires <code>WEEKLY_COST_HOURLY_RATE</code> env var (no hardcoded fallback per §honesty); (5) <code>p95_render_time</code> percentile 95 of render_ms over 7d; (6) <code>peak_memory</code> MAX(framebuffer_bytes_peak) over 7d; (7) <code>deterministic_hash_failures</code> GATE_FAIL count from selftest logs; (8) <code>bbox_contract_violations</code> SUM from FU01 counter over 7d. <strong>2 artifacts</strong>: aggregator <code>scripts/run_weekly_scorecard.sh</code> (163 LoC bash+sqlite3+awk+date, exits 0 on populated telemetry SQLite / 2 INTERNAL on missing tools or DB) + dashboard template <code>docs/product-validation/TEST-18-WEEKLY-DASHBOARD.md</code> (sample weekly entry, 7 narrative lines, §honesty cert). <strong>Cadence</strong>: weekly (founder runs aggregator on Friday afternoon).</td></tr>
<tr><th>Criteri di accettazione</th><td>(1) <code>scripts/run_weekly_scorecard.sh</code> exists + is executable + bash syntax-check PASS (<code>bash -n</code>); (2) <code>docs/product-validation/TEST-18-WEEKLY-DASHBOARD.md</code> exists with sample weekly entry + 7 narrative lines + §honesty cert + cross-link; (3) PRODUCT-VALIDATION-AGGREGATOR row 18 refactored to <code>OPEN (Weekly founder dashboard)</code> with cross-link; (4) per-row PASS criterion eseguibile come <code>bash scripts/run_weekly_scorecard.sh &gt;&gt; /tmp/weekly.md</code> (exit 0 on populated telemetry SQLite per §honesty).</td></tr>
</table>

# Cross-link

- PRODUCT-VALIDATION-AGGREGATOR row 18
- Moved from Chronon3d core `docs/tickets/TICKET-128-test-18-long-form-content.md` (deleted) — dashboard = business metric, vive in PipelineGen
- `scripts/run_weekly_scorecard.sh` (migrato da core `tools/`)
- §honesty "non inventare"

# §honesty dynamic cert

- Aggregator script queries OBSERVABLE telemetry surfaces; macchina-verifica end-to-end è PARTIAL (telemetry SQLite may be empty — the script returns exit 2 on missing DB; <code>bash -n</code> syntax-check PASS).
- <code>WEEKLY_COST_HOURLY_RATE</code> env var is REQUIRED for metric 4 per §honesty "non inventare"; unset = <code>[UNSET-rate]</code> placeholder, NEVER a fabricated spot rate.
- First-run caveat: the founder MUST run the aggregator at least once to seed the SQLite cache; sample entries in the dashboard template are FILL-IN-THE-BLANK commitments, not synthetic real-data.
- macchina-verifica of the 8 metrics end-to-end with a populated DB is DEFERRED to a working build host.