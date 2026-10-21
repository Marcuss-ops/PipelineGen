# Garlasco 01 — animation audit: stato della certificazione

Data: 2026-10-21 · Repository: `refactored/` (PipelineGen) · Payload base: r9 (non modificato).

## Obiettivo

Certificare le famiglie di animazione di Garlasco 01 — mappe, short/long phrases,
gruppi multi-immagine e multi-entità, final animation di gruppo, bullet e charts —
prima di spendere un render finale sul Master 51.

## Cosa è stato consegnato

| Artefatto | Ruolo |
| --- | --- |
| `build_payloads.py` | genera `payloads/overlay.json` (solo overlay) e `payloads/final.json` (overlay + handoff Master) dal payload r9 intatto |
| `run_audit.sh` | invia un payload, attende lo stato terminale e salva il body completo in `evidence/<profile>.full.json` |
| `audit_plan.py` | valuta il piano prodotto contro G01, G02, E01, E02, M01, S01, L01, F01, B01, B02, C01, C02, X01, X02 e scrive `*.audit.json` / `*.audit.md` |
| `make audit-plan` / `scripts/audit-plan.sh` | modalità plan-only: compila l'`overlay_plan` con `output.render.enabled=false` (docs off), senza fan-out dei render, e scrive `plan.json` + `full.json` per l'audit |
| `README.md` | comandi di riproduzione e mappa dei controlli |

I due payload differiscono solo per `final_job`. Entrambi abilitano tutte le
categorie, l'opt-in esplicito dei charts e una corsia bullet indipendente
(`overlay_group_limits.bullets = 4`). Lo stile impone verbatim, distinzione
Stasi/Sempio, luoghi reali e nessun grafico costruito solo da date.

## Correzioni di pipeline (verificate da test)

### 1. Corsia bullet indipendente (B01/B02)

Prima: le chapter card erano `text_phrase` senza corsia propria. La quota
`long_phrases` le contava e `ApplyEditorialOverlayBudgetWithPhraseLanes` le
teneva nella corsia delle frasi lunghe, quindi `10 short + 10 long + bullet`
non poteva coesistere (con `max_phrase_overlays = 20` i bullet erano la prima
vittima).

Ora:

- `overlay_group_limits.bullets` è una chiave valida e apre
  `PlannerConfig.RunLevelBulletOverlayLimit`;
- `overlayGroupOf` classifica `scene_bullet` e `chapter_bullet` nel gruppo
  `bullets`, non in `long_phrases`;
- `ApplyEditorialOverlayBudgetWithBulletLane` ammette i bullet su quota propria
  (una card per scena, testo deduplicato) e li esclude dalle corsie short/long;
- il cap viene applicato sia nel budget del planner sia nell'ammissione
  autorevole di `overlay_plan_compile.go`;
- una sola quota governa **tutti** i bullet: con la corsia attiva `scene_bullet`
  esce dalla corsia editoriale (`editorialLabelIndicesExcludingBullets`) e viene
  ammesso dalla quota bullet, quindi `bullets: N` significa esattamente N card.

Test: `overlays/bullet_lane_budget_test.go` (10+10+4 coesistono; quota e dedupe;
legacy path invariato), `scripts/bullet_group_limit_test.go` (classificazione,
cap indipendente, `bullets: 0`, chiave payload, wiring del cap).

### 2. Chart atomici nell'handoff replace-mode (C01/X01)

Prima: `BuildChartItems` produce una card e N barre sulla stessa finestra. Il
gate finale `finalJobOverlayAssetsExcluding` ammetteva un overlay per intervallo
frame e spostava gli altri dentro la scena, separando le barre dalla card: un
grafico semanticamente corretto diventava una card senza barre.

Ora: `rejectSplitChartGroups` fallisce chiuso prima di qualunque tentativo
remoto se un gruppo chart ha più di un item sulla stessa finestra, con un errore
che nomina la pre-composizione richiesta. Nessun grafico viene più spezzato in
silenzio.

Test: `TestFinalJobRejectsSplitChartGroup` in
`final_job_overlay_ssot_gate_test.go`.

Limite dichiarato: la pre-composizione del grafico in un singolo artefatto
certificato **non** è implementata in questa sessione. Finché non lo è, un job
`final_job=true` che produce un chart fallisce con quel messaggio invece di
consegnare un grafico rotto. Garlasco 01 non ha numeri confrontabili nella
stessa scena (solo date), quindi il caso atteso è zero chart; il canary
positivo resta `e2e_cert/T04_bullets_charts_20261020` (C01–C05, 28 barre
animate, segmenti MP4 reali).

## Verifiche eseguite

| Verifica | Esito |
| --- | --- |
| `go test ./internal/capabilities/overlays/... ./internal/capabilities/scripts/ -count=1` | PASS (4 package, 0 fail) |
| `gofmt -l` sui file modificati | pulito |
| `make verify-agent` — foundation, model-alias, `go vet ./...`, `go build ./...`, verify-dev | PASS |
| `make verify-agent` — componenti impattati | PASS (`final=PASS`, 21 componenti: script, database, research, clips, stock, qdrant, indexing, drive, docs, voiceover, images, ollama, translation, storage, timeline, jobs, api, youtube, artlist, kernel, rust-muscles) |
| `audit_plan.py` — fixture positivo (2 entità, mappa, short/long, bullet, gruppo) | PASS: 11/14 PASS, exit 0; G02/C02 correttamente `NOT_OBSERVED`/`NOT_APPLICABLE` |
| `audit_plan.py` — fixture con chart solo-date e gruppo spezzato | PASS del comportamento: C01/C02/X01 FAIL, exit 1 |
| `audit_plan.py` — input senza `overlay_plan` | exit 2 con errore esplicito |
| Composer/parser sidecar — test pacchetti scripts, adapters e overlays | PASS, inclusi i nuovi controlli di simultaneità e immagini stock |
| `go test ./internal/capabilities/scripts/overlay -count=1` | PASS |
| `make build` | PASS, build finale `2026-10-21T10:29:21Z` |
| `make verify-agent` | PASS (gitleaks, foundation, static, componenti impattati) |
| `GET /health` dopo il riavvio finale | PASS (`healthy`, PID 101662, build `10:29:21Z`) |

## Stato live e criteri aperti

Il primo tentativo `job_1792567009309375257_f77d798f` fallì perché il processo
attivo non accettava ancora `overlay_group_limits.bullets`; il body è in
`evidence/overlay.full.json` e il relativo report certifica solo ciò che era
nel piano precedente.

Il servizio è stato ricompilato e riavviato il 21 ottobre alle 10:29 UTC.
`/health` è tornato `healthy` e riporta build `2026-10-21T10:29:21Z`.
Un tentativo plan-only (`job_1792577870660791709_1e7ee6f3`) è rimasto fermo alla
generazione scene per oltre quattro minuti; è stato cancellato senza rendering.
Body terminale: `evidence/plan-only/full.json`.

La composizione multi-image ora usa finestre simultanee, coordinate affiancate,
nessuna camera sequenziale e motion/caption indipendenti per livello. I test
`TestComposeNearbyEntityCardsIntoSimultaneousGroup`,
`TestComposeNearbyEntityImagesCreatesOneSimultaneousComposite` e
`TestCompileOverlayPlanRetryKeepsStableIndependentImageMotions` coprono questo
contratto. I test del sidecar stock verificano ricerca e materializzazione delle
immagini con video stock conservato.

Il job `final_job=true` `job_1792579345184089253_acc0ca7a` è stato avviato il
21 ottobre alle 10:42 UTC e cancellato alle 11:14 UTC su richiesta, perché la
fase bullet VisualNER non avanzava: due chiamate sono scadute e una successiva
è rimasta in esecuzione per oltre dieci minuti. Il body è conservato in
`evidence/final.full.json`; non è stato certificato un MP4 finale.

Resta da verificare con un render che RenderingGen mostri effettivamente il
gruppo simultaneo e l'ingresso/uscita, e da eseguire la certificazione MP4/audio.
La pre-composizione dei charts in un
artefatto singolo per Master 51 resta non implementata; Garlasco deve produrre
zero charts in assenza di dati confrontabili. Il canary positivo charts rimane
separato in `e2e_cert/T04_bullets_charts_20261020`.

## Riferimenti

- Payload r9 sorgente: `ops/jobs/garlasco_batch_10_20261019_sequential_v7/resubmit_r9/01_cronologia.r9.json`
- Audit harness: questa directory
- Canary chart/bullet con MP4 reali: `../e2e_cert/T04_bullets_charts_20261020/CERTIFICATION.md`
