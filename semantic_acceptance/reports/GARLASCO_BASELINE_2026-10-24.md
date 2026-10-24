# Garlasco — gold ricostruito e run del 2026-10-24

## Cosa è stato creato

| File | Ruolo |
|---|---|
| `semantic_acceptance/fixtures/garlasco_2013_reconstructed.txt` | Copione di **2.013 parole**, 15 scene, ricostruito dai 15 brief segmentali storici (commit `982f39e0f`) |
| `semantic_acceptance/fixtures/garlasco_2013_gold.json` | Gold congelato: 20 entità, 11 date, 5 fatti Stasi, 8 eventi, 15 concetti visuali core, gate |
| `semantic_acceptance/garlasco_2013_gold_score.py` | Scorer T01/T05 contro il worker Rust |
| `semantic_acceptance/garlasco_3k_report.py` | Rigenerazione del report sulla fixture da 3.127 parole |
| `semantic_acceptance/reports/garlasco_2013_gold_2026-10-24.{md,json}` | Run dettagliato del gold (post-fix) |
| `semantic_acceptance/reports/SEMANTIC_3K_REPORT_2026-10-24.{md,json}` | Report 3K rigenerato (post-fix) |
| `semantic_acceptance/reports/chronology_corrected_2026-10-24.json` | Run della fixture corretta 2009–2015 |
| `semantic_acceptance/reports/sempio_3k_acceptance_2026-10-24.json` | Check di contratto sulla fixture 3K |
| `semantic_acceptance/reports/garlasco_full_2026-10-24.json` | Check di contratto sulla fixture 2.797 parole |
| `semantic_acceptance/reports/t05_charts_2026-10-24.json` | T05 chart fixtures (5/5 PASS) |

> **Provenienza.** Il copione da 2.013 parole è ricostruito dai brief, non è il file originale perduto. Le annotazioni sono state scritte e congelate **prima** di eseguire il motore, ma non sono un gold umano indipendente. Il run è una baseline ripetibile, non una certificazione indipendente.

## T01 — risultato del gold da 2.013 parole

Worker: `BrainRust/target/debug/semantic-engine` · fingerprint `9294f00793b985bb` · due run identici.

**27 PASS, 0 FAIL, 4 NEEDS_REVIEW** su 31 check.

| Area | Esito |
|---|---|
| Span e contratto | 819 candidati, **0 offset errati**; schema v2; 15 scene |
| Entità | recall **19/20**, typed recall **18/20**, precision **32/32**; 0 scambi PERSON↔LOCATION |
| Entità mancanti | `Vigevano` non estratta; `RIS di Parma` senza un'occorrenza `ORG` |
| Type warning | **12** surface valide con tipo diverso (`Chiara Poggi`, `Stasi`, `Sempio` come `VISUAL_CONCEPT`; `RIS` come `VISUAL_CONCEPT`) |
| Date | **11/11**, inclusi `19` e `20 settembre 2007` nella lista coordinata |
| Fatti Stasi | **5/5**: assoluzione 2009, conferma 2011, annullamento 2013, condanna 2014, definitività 2015 — tutti su Stasi |
| Conflitti | **0**: `confermò l'assoluzione` non produce più anche `acquitted` |
| Sempio | 0 fatti `convicted`/`final`; 3 `under_investigation` |
| Eventi | **8/8** presenti in bullet/micro/evidence |
| Concetti visuali | **15/15** core coperti da query |
| Query | 0 non grounded; **1.081 entry, 72,1 per scena**, 1.094 exact (367 uniche), named share 0,14 |
| Grafici | 0 proposte, 0 item (il gold non ne prevede) |
| Timeline | `step_sequence` 2009→2011→2013→2014→2015, **5 membri** |
| Confronti | 0 gruppi `comparison` oltre 2 membri |
| Story graph | identità Stasi + 5 edge `about`; **0 edge chronological** (corretto: i 5 fatti sono in una scena) |
| Coverage | **5/5** fatti Stasi coperti; 5/10 fatti totali coperti |
| Micro | 9 summary; il finale dice **«Alberto Stasi: condanna definitiva nel 2015»** |
| Riproducibilità | 2 run, stesso fingerprint e stesso manifest hash |

## I tre difetti trovati e corretti

### 1. La definitività era attribuita alla corte, non alla persona

Prima: «Il 12 dicembre 2015 la condanna divenne definitiva in Cassazione» produceva `final` su `org_319` (**Cassazione**), non su `person_73` (**Alberto Stasi**).

Fix in `BrainRust/semantic-engine/src/legal.rs`: `finality_subject` preferisce una persona nella frase, poi l'unica persona recente; l'ente è l'ultima risorsa. Regressione: `finality_prefers_the_convicted_person_over_the_court_after_the_marker`.

Ora il fatto finale è su Stasi, il micro è corretto e la coverage Stasi è 5/5.

### 2. «Confermò l'assoluzione» produceva due fatti sullo stesso giorno

Prima: a `2011-12-06` comparivano sia `acquitted` (marker generico `assoluzione`) sia `acquittal_confirmed` (marker specifico).

Fix in `legal.rs`: `confirmation_spans` sopprime il marker generico **solo quando è annidato** nel marker di conferma. Un'assoluzione distinta nello stesso periodo (2014) resta viva anche se la frase contiene una conferma del 2016. Regressioni: `confirmed_acquittal_does_not_also_emit_a_plain_acquittal` e il preesistente `a_second_date_in_the_sentence_does_not_override_the_outcome_date`.

### 3. Le liste coordinate di date perdevano il primo e il secondo elemento

Prima: «gli atti riportano i giorni 19, 20 e 26 settembre 2007» estraeva solo `26 settembre 2007`; `19` e `20` restavano `NUMBER`.

Fix in `BrainRust/semantic-core/src/dates.rs`: `coordinated_days_before` riconosce la lista separata da virgole o congiunzioni e fa ereditare a ogni giorno lo stesso mese e anno. L'ultimo giorno conserva lo span completo `26 settembre 2007`; i precedenti conservano il proprio span numerico con data ISO corretta. Regressione: `coordinated_days_share_the_month_and_year`.

## Test del motore

`RUST_ICU_MAJOR_VERSION_NUMBER=70 cargo test -p semantic-core -p semantic-engine`:

- **136** test `semantic-core` passati;
- **23** test lib `semantic-engine` passati;
- **61** test di integrazione `semantic-engine` passati;
- **0 falliti**.

La variabile ICU 70 è necessaria su questa macchina locale; la release canonica resta ICU 72 (Bookworm).

## 3K — cosa è cambiato rispetto al report 2026-10-23

Fixture `sempio_chiara_poggi_youtube_3k.txt`: **3.127 parole, 14 scene**, fingerprint `ec4ca63f533aaf18`.

| Sintomo pre-fix | Ora |
|---|---|
| `RITROVAMENTO`, `Chiara`, `Poggi` come `LOCATION` | **0** |
| Gruppo `comparison` da 5 membri | **0** gruppi oltre 2 membri |
| `LOCATION` totali | 5: `Garlasco`, `Pavia` (più le occorrenze interne) |
| Persone | 13 menzioni `PERSON`: `Chiara Poggi`, `Andrea Sempio`, `Alberto Stasi` |
| Fatti | 5, tutti su `Alberto Stasi` |
| Coverage | **5/5** fatti coperti da timeline |
| Query | 1.349 entry, 1.352 exact (451 uniche) |

**Caveat:** gli anni 2014/2016/2017/2019/2020 sono nel testo della fixture, non sono un errore del motore. La sequenza documentata 2009–2015 è verificata dalla fixture separata `garlasco_chronology_corrected.txt`.

## Fixture storica da 2.797 parole

La fixture `garlasco_full.txt` (12 scene) resta un controllo di contratto: 731 menzioni, 6 fatti, **4/6 fatti coperti** (2 non coperti), 1.365 entry di ricerca. Non è un gold: serve a verificare che il contratto regga anche sul copione storico più lungo.

## Chronology corrected — run di controllo

`custom --text-file garlasco_chronology_corrected.txt --gold garlasco_chronology_gold.json`:

- **PASS** `chronology_gold`: `[2009 acquitted, 2011 acquittal_confirmed, 2013 annulled_with_remand, 2014 convicted, 2015 final]`;
- **PASS** `entity_linking`, `timeline_completeness`, `visual_coverage` 5/5, `story_graph` 10 nodi/5 edge;
- **NOT_RUN** `charts_grounded` (nessuna proposta chart) e `comparison_precision` (nessun gruppo comparison).

## T05 — corpus score: cosa è eseguibile e cosa no

**Eseguito (meccanico):**

- `charts_digits`, `charts_spelled`, `charts_dates_never_bars`: **5/5 PASS**;
- concetti visuali core sul gold 2.013: **15/15**;
- query grounding: **0/4.358** non grounded;
- corpus 3K: 1.349 entry, 1.352 exact (451 uniche), coverage **5/5**, 0 LOCATION vietate.

**Non eseguito:**

- recall/precision **semantica di corpus** per oggetti e keyword visuali. Esiste una sola baseline congelata (2.013 parole) e una fixture 3K senza annotazioni indipendenti. Il run 3K misura contratto, grounding, coverage e volume, non la pertinenza delle query. Un numero di corpus richiederebbe almeno 2–3 copioni annotati da una persona diversa dal motore. Il report non lo inventa: resta `NOT_RUN`.

## Cosa resta NEEDS_REVIEW

1. **Provenienza**: il gold è ricostruito, non indipendente.
2. **Type warning (12)**: le forme brevi `Stasi`/`Sempio` e alcune occorrenze di `Chiara Poggi` sono `VISUAL_CONCEPT` invece di `PERSON`; `RIS` è `VISUAL_CONCEPT` invece di `ORG`; `Vigevano` manca. Non sono scambi PERSON↔LOCATION.
3. **Volume query**: 1.081 entry e 4.358 query per 2.013 parole, 72,1 per scena. Il grounding è perfetto, ma serve un budget/top-N o una deduplicazione per identità.
4. **Named share 0,14**: solo il 14% delle exact mention uniche nomina un'entità/concetto del gold; i concetti visuali generici non sono annotati, quindi non è una precision umana.

## Distanza aggiornata

| Salto | Stato dopo questi run |
|---|---|
| **1. Gold** | Baseline T01/T05 **verde sui gate semantici** (0 FAIL). Restano 4 review item e manca il gold **indipendente**/corpus annotato. |
| **2. Fatti oltre legale/facility** | Invariato: il gold misura il dominio legale. I tre fix erano attribuzione/parsing, non generalità. |
| **3. Loop fino al video** | Invariato: planner `failed/retry`, chart→video e certificazione del render non sono esercitati. |

La frase secca: **il motore è più misurabile e ha tre difetti in meno; non è ancora più generale.** La baseline ora distingue chiaramente cosa è rotto (type consistency e budget query) da cosa è solo non ancora annotato (corpus indipendente).

## Prossimi passi

1. **Coerenza di tipo**: alias persona (`Stasi`, `Sempio`) sempre `PERSON`; cue `RIS` come `ORG`; `Vigevano` come `LOCATION`.
2. **Budget query**: top-N per scena o dedup per identità; misurare la pertinenza su un campione etichettato.
3. **Corpus**: annotare altri 2–3 copioni per trasformare la baseline in un vero score T05.
4. **Generalità**: schema evento/ruolo/predicato oltre i pack legali/facility.
5. **Video**: planner `failed/retry`, chart→video, certificazione render.

## Comandi riproducibili

```bash
cd BrainRust
RUST_ICU_MAJOR_VERSION_NUMBER=70 cargo test -p semantic-core -p semantic-engine
RUST_ICU_MAJOR_VERSION_NUMBER=70 cargo build -p semantic-engine --bin semantic-engine

cd ../refactored
python3 semantic_acceptance/garlasco_2013_gold_score.py \
  --worker ../BrainRust/target/debug/semantic-engine --repeat 2

python3 semantic_acceptance/garlasco_3k_report.py \
  --worker ../BrainRust/target/debug/semantic-engine

python3 semantic_acceptance/semantic_today_acceptance.py charts \
  --worker ../BrainRust/target/debug/semantic-engine

python3 semantic_acceptance/semantic_today_acceptance.py custom \
  --worker ../BrainRust/target/debug/semantic-engine \
  --text-file semantic_acceptance/fixtures/garlasco_chronology_corrected.txt \
  --scene-words 220 \
  --gold semantic_acceptance/fixtures/garlasco_chronology_gold.json
```
