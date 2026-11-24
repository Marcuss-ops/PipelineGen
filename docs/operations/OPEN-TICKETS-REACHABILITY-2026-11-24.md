# Ticket refactored aperti — raggiungibilità 2026-11-24

**Scopo:** registrare perché i ticket non chiusi in questo checkout non possono
essere portati a CLOSED senza servizi esterni o una decisione di prodotto. Nessun
ticket di questa tabella viene dichiarato chiuso da questa sessione.

| Ticket | Residuo | Perché non è raggiungibile qui | Cosa lo sblocca |
|---|---|---|---|
| `TICKET-FINAL-JOB-BITSTREAM-REUSE-2026-11-01` (P5) | Run end-to-end del job P4 sul worker; gate mappe importate | Richiede worker/Master reali; il codice del gate avcC e i test fail-closed sono presenti | Un job P4 sul worker e il confronto ai confini del filmato |
| `TICKET-FINAL-JOB-SPEEDUP-LEVERS-2026-11-02` | L1 copy-window 100%, L2 `prepare_join`, L4 tassa per frammento, L5 riduzione frammenti; L3 rimisurazione | Ogni leva richiede benchmark same-SHA e run con cache; la varianza end-to-end è +23% | n≥3 run per configurazione sullo stesso payload |
| `TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE` | Gate ≥100 overlay/min; mix 100 item con mappe; A3–A5 | Cross-repo (Chronon3d/Master) e richiede run con mappe | Run closeout certificata con mappe e owner Chronon/Master |
| `TICKET-OVERLAY-CHAIN-ENTITY-IMAGES-2026-11-19` | Clip con aspetti misti, confronto 2 vs 6, r15 dopo i fix mappa; blur lungo il pan | Richiede render reali; il blur è del renderer RenderingGen | Run della suite catene e fix renderer |
| `TICKET-PARADIGM-ALPHA-LAYER-HUD` | A2–A4/B1–B3 solo SPEC; producer emette `placement: null` | È una decisione di prodotto, non un fix; A0 ha dato misura negativa (1,44× vs 8×) | Autorizzazione esplicita a implementare o archiviazione |
| `TICKET-MASTER-STOCK-MATERIALIZATION-2026-11-14` | Sei criteri Master/stockpipeline | Il codice non è in questo workspace; il lato Producer è chiuso | Owner Master 51 / stockpipeline |
| `E2E-03` (root) | Guadagno 1-vs-N misurato su script lungo | Richiede un job reale; identità 1-vs-4 e cache R47 sono già provate | Una run reale con baseline e rerun |
| `IMG-05` (root) | Run 5–6 scene con cache hit, zero 429 e asset Roma/Campidoglio | Richiede provider live e un job reale | Run immagini dopo rebuild/restart del Producer |
| `CLOSE-04` (root) | Run completa `--skip-live` verde | Il tree refactored è sporco per il cantiere concorrente; `verify-agent` va in timeout a 300 s | Commit/clean tree e diagnosi del timeout |
| `PAR-01` (root) | Lane A misurata su 3–10 run | Richiede config dello scheduler Master; il `queue_ms` citato non è confermato | Riattribuzione della misura e config Master |
| `CR-19-23` (root) | Verifica griglia su render reale a 2/30/100 s | Richiede API, GPU e Drive | Render reale con e senza griglia |
| `BONIFICA-07` (root) | Hotspot C++ Chronon3d | Fuori dal perimetro `refactored` | Owner Chronon3d |

## Chiusi in questa sessione

- **BON-01**: criteri di riapertura soddisfatti e debito di orchestrazione
  rimosso. `scripts/runner.Execution` possiede sequenza, stato run-scoped,
  checkpoint e failure; il root implementa `Host` con le fasi. Le porte
  applicative sono in `scripts/ports/script` e `scripts/ports/exec`, con soli
  alias nel root. Inventario generation chiuso: zero lettori non classificati,
  quattro confini di fase motivati. Baseline hotspot `scripts` ratchettato da
  140 a 62; hotspot controllati anche sotto il cap e line budget sui cinque
  package più grandi. Test, race runner, build, vet, archcheck e hotspot verdi.
- **TICKET-CHAIN-SEGMENT-SSOT-2026-11-18**: CLOSED come SUPERSEDED da OVL-01;
  il pianificatore pre-v2 non esiste più.
