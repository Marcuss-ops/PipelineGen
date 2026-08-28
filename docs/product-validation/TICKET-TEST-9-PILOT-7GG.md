<table>
<tr><th colspan="2">TICKET-TEST-9-PILOT-7GG — Test 9 Pilot protocol 7gg (product validation)</th></tr>
<tr><th>Stato</th><td>OPEN (moved from Chronon3d core TICKET-126 — product validation, not engine certification)</td></tr>
<tr><th>Priorità</th><td>P1 (parallel precedent to Test 15 HARNESS-COMPLETE row; pilot-runtime requires human execution, non-VPS-deferrable)</td></tr>
<tr><th>Problema</th><td>Test 9 (Pilota cliente reale 7gg) richiede un harness canonico (<code>docs/product-validation/TEST-9-pilot-protocol.md</code> + <code>docs/product-validation/TEST-9-feedback-form.md</code>) per catturare evidenza non-developer che il prodotto Chronon3D è utile senza spiegare l'architettura (parallel precedent a Test 15). L'harness attualmente è MISSING — la riga Test 9 nell'aggregator porta HARNESS-MISSING marker onesto (no fabrication).</td></tr>
<tr><th>Evidenza</th><td>PRODUCT-VALIDATION-AGGREGATOR row 9 + Test 15 row (HARNESS-COMPLETE) come parallel precedent: 4 docs <code>docs/product-validation/TEST-15-{one-pager,feedback-form,pilot-protocol,transcript-template}.md</code> landed 2026-07-12 commit <code>16855f33</code> (Chronon3d core). Test 9 mirrors the same harness shape but with a 7-day timeline (vs Test 15's 10-min per-subject).</td></tr>
<tr><th>Impatto</th><td>Senza un harness canonico Test 9, il pilota 7gg non ha un deliverable canonico per la raccolta evidenza (<code>chronon3d_cli render</code> evidence). Forward-point cat-4 ancillary: <code>docs/product-validation/TEST-9-pilot-protocol.md</code> + <code>docs/product-validation/TEST-9-feedback-form.md</code> + <code>docs/product-validation/TEST-9-transcript-7gg.md</code>.</td></tr>
<tr><th>Confine</th><td>Pure <code>docs/product-validation/</code> artifact in PipelineGen (no Chronon3d SDK API surface; no <code>include/chronon3d/</code> edits). Harness + protocol = HARNESS-COMPLETE; pilot-runtime requires a real user + 7 real clients → DEFERRED (human-execution-required, cannot be machine-verified).</td></tr>
<tr><th>Soluzione accettabile</th><td>3 NEW docs under <code>docs/product-validation/</code>: (1) <code>TEST-9-pilot-protocol.md</code> — 7-day timeline SOP with role-balanced cohort (founder 2 + PM 2 + designer 2 + operator 2); (2) <code>TEST-9-feedback-form.md</code> — Q1 better-than-previous + Q2 would-you-use-it + Q3 time-saved-per-video-7gg + verbatim quote + NPS 0-10 + meta data; (3) <code>TEST-9-transcript-7gg.md</code> — 7-day daily diary template that preserves verbatim client responses (roughness is signal). Sharded from Test 15 (10-min per-subject) on the timeline axis ONLY; the verbatim-question shape is identical (CAT-9 mirror = median Q1 ≥ +1 across ≥5 clients).</td></tr>
<tr><th>Criteri di accettazione</th><td>(1) 3 NEW files present in <code>docs/product-validation/</code>; (2) CAT-9 PASS criterion sharpened per Test 15 CAT-15 pattern (median Q1 ≥ +1 across ≥5 clients + median Q2 ≥ +1 + median Q3 ≥ 30 min/video); (3) §honesty PARTIAL cert on pilot-runtime phase (deferred to user); (4) aggregator row updated on close.</td></tr>
</table>

# Cross-link

- PRODUCT-VALIDATION-AGGREGATOR row 9 (HARNESS-MISSING marker onesto)
- Test 15 row in PRODUCT-VALIDATION-AGGREGATOR (HARNESS-COMPLETE parallel precedent — 4 docs landed commit `16855f33`)
- Moved from Chronon3d core `docs/tickets/TICKET-126-test-9-pilot-7gg.md` (deleted) — product validation lives in PipelineGen, not engine core
- §honesty "non inventare" (harness + protocol = HARNESS-COMPLETE on disk; pilot-runtime = user-execution-required)

# §honesty stub-cert

- File scritto come canonical stub per evitare phantom reference (this ticket filename viene referenziato nel PRODUCT-VALIDATION-AGGREGATOR).
- Tutta la substance (3 NEW docs + 7-day diary + cohort SOP) deferred a next cycle.
- No fabrication: lo stato "OPEN" è onesto — harness MISSING finché i 3 docs non esistono.