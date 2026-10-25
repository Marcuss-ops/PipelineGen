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
>
> **Ultimo aggiornamento.** I numeri sotto vengono dal worker release ricompilato il 2026-10-24 dopo l'ultima correzione (dedup semantico delle micro-summary, date canoniche con tutti gli span, test di consolidamento), con Semantic Consolidation v1 e le correzioni `Vigevano`/`RIS`; sostituiscono il run del mattino (worker debug, 12 type warning, 19/20 entità).

## T01 — risultato del gold da 2.013 parole

Worker: `BrainRust/target/release/semantic-engine` · fingerprint `9294f00793b985bb` · due run identici.

**37 PASS, 0 FAIL, 3 NEEDS_REVIEW** su 40 check. I tre review item sono provenienza del gold ricostruito, volume query raw e named share.

| Area | Esito |
|---|---|
| Span e contratto | 823 candidati, **0 offset errati**; schema v2; 15 scene |
| Entità | recall **20/20**, typed recall **20/20**, precision **46/46** menzioni nominate; 0 scambi PERSON↔LOCATION; **0 type warning** |
| Type fix | `Vigevano` è `LOCATION`; `RIS` è `ORG`; gli alias `Stasi`/`Sempio`/`Chiara Poggi` sono `PERSON` |
| Date | **11/11**, inclusi `19` e `20 settembre 2007` nella lista coordinata |
| Fatti Stasi | **5/5**: assoluzione 2009, conferma 2011, annullamento 2013, condanna 2014, definitività 2015 — tutti su Stasi |
| Conflitti | **0**: `confermò l'assoluzione` non produce più anche `acquitted` |
| Sempio | 0 fatti `convicted`/`final`; 3 menzioni `under_investigation` → **1 fatto canonico** |
| Eventi | **8/8** presenti in bullet/micro/evidence |
| Concetti visuali | **15/15** core coperti da query |
| Query raw | 0 non grounded; **1.094 entry, 72,9 per scena**, 1.113 exact (369 uniche), named share 0,15; 0/4.424 query non grounded (4.424 = 2.212 rung di `search_feedback` + 2.212 suggerimenti nelle proposte) |
| Grafici | 0 proposte, 0 item (il gold non ne prevede); `33` è un reference number, non un valore |
| Timeline | `step_sequence` 2009→2011→2013→2014→2015, **5 membri**, **1 sola** timeline di produzione |
| Confronti | 0 gruppi `comparison` oltre 2 membri |
| Story graph | identità Stasi + 5 edge `about`; **0 edge chronological** (corretto: i 5 fatti sono in una scena) |
| Coverage raw | **5/5** fatti Stasi; 5/10 fatti totali (misura delle sole proposte esistenti) |
| Coverage consolidata | **8/8 fatti canonici rappresentati, 0 scoperti**: 5 Stasi nella timeline + 3 Sempio in tre fact card contestuali |
| Micro | 9 summary raw, 8 canoniche; il finale dice **«Alberto Stasi: condanna definitiva nel 2015»** |
| Consolidamento | **1.097 proposte → 73 candidati** (−93,3%), **1.094 entry → 31 job di ricerca** (−97,2%), **2.212 query → 31** (−98,6%); 28 entity card riusano l'asset dell'identità; 10 composizioni multi-entity senza nuova ricerca |
| Evidence plan | 1 timeline, 3 fact card, 4 event link `precedes`, 1 reference number `33` escluso dai grafici, 0 evidenze/query non grounded |
| Riproducibilità | 2 run, stesso fingerprint e stesso manifest hash |

## Batterie di accettazione (ultimo run, worker release)

| Batteria | Esito |
|---|---|
| `selftest` | 0 FAIL; 1 NEEDS_REVIEW (`density_groups`); 4 NOT_RUN (`charts_grounded`, `story_graph`, `comparison_precision`, `timeline_completeness`) |
| `run` (`garlasco_full`) | 0 FAIL; 3 NOT_RUN (`charts_grounded`, `comparison_precision`, `timeline_completeness`) |
| `charts` | **5/5 PASS** (`charts_digits`, `charts_digits_grounded`, `charts_spelled`, `charts_spelled_grounded`, `charts_dates_never_bars`) |
| 3K report | 0 LOCATION vietate, 0 `comparison` oltre 2 membri, 6 fatti, coverage raw 5/6 → consolidata 6/6, 30 candidati / 13 query di produzione; planner **12 media**, 6/6 fatti coperti (vedi «Piano di produzione») |
| chronology corrected + gold | `chronology_gold` PASS, `timeline_completeness` PASS, coverage raw 5/6 → consolidata 6/6, 0 FAIL; 2 NOT_RUN |
| comparison fixture | `comparison_precision` PASS (1 confronto), 0 FAIL |
| corpus acceptance (campione 6 script, 26.359 parole) | 0 hard failure; **8.935 proposte → 595 candidati**, **369 job** e **369 query**; 0 fatti canonici (transcript EN, nessun fatto legale italiano). |
| corpus acceptance completo (88 script, 525.951 parole, rerun sul worker finale) | **0 hard failure**, 83 script con note; 2.338 scene, 99.594 entità, 34 fatti; **190.084 proposte → 12.930 candidati** (−93,2%), **8.455 job** e **8.455 query**; **34/34 fatti canonici coperti**; anni 203/203, percentuali 77/77. Vedi `CORPUS_ACCEPTANCE_2026-10-24.md`. |

## Piano di produzione (planner-level, 2026-10-24)

Il consolidamento non è più solo una vista di manifest: il planner Go
(`SemanticProductionPlan`) consuma `visual_consolidation`, applica il budget
certificato della run (20 immagini, 2 per scena), riusa un solo retrieval per
`asset_key`, ammette un'immagine di identità solo se il piano l'ha selezionata e
riporta l'esito della ricerca nello stesso contratto `search_feedback`.

Misure dal worker release reale attraverso l'adapter Go e il planner Go
(`TestSemanticEngineRealBinaryPlansPlannerVolumes`):

| Fixture | Scene | Proposte raw | Rung raw | Entry raw | Candidati | Media pianificati | Query pianificate | Scene-differiti | Budget-differiti | Fatti coperti |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `garlasco_2013_reconstructed` | 15 | 1.097 | 2.212 | 1.094 | 73 | **18** | **18** | 13 | 0 | 8/8 |
| `garlasco_full` | 10 | 1.347 | 2.671 | 1.336 | 68 | **20** | **20** | 23 | 0 | 5/5 |
| `sempio_chiara_poggi_youtube_3k` | 43 | 1.424 | 2.845 | 1.419 | 30 | **12** | **12** | 1 | 0 | 6/6 |

Lettura: sul gold 2.013 parole il planner esegue **18 ricerche** invece di 1.094
entry raw (−98,4%) e di 2.212 rung (−99,2%); le 13 composizioni in più sono
differite dal tetto di 2 immagini per scena, non dal tetto di run. Sul 3K le
ricerche scendono a 12 su 43 scene. Le identità ammesse per un'immagine sono 18
su 29 sul gold, 20 su 23 su `garlasco_full` e 12 su 13 sul 3K.

Stato del feedback: ogni composizione cercata ha una entry `pending` con ladder
esatta; un fallimento al primo rung produce `broaden`, un ladder esaurito senza
altri nomi produce `skip_visual`, un hit produce `satisfied`. Il test reale
simula entrambi i casi e verifica che il piano termini (nessun retry infinito).
Quando un ladder si esaurisce, i fatti canonici che quella composizione
rappresentava passano da `covered` a `deferred` con il motivo del provider e
l'identità perde l'ammissione immagine: la copertura non può restare verde su un
visual che non è stato prodotto.
`drop_entity` è coperto dal test unitario con un ladder multi-nome non ancora
cercato; il motore attuale emette ladder mono-identità per le ricerche media,
quindi sul corpus reale non si presenta.

### Misura live: scheduler budget-aware, routing e cache (provider reale, 2026-10-24)

Storico: la prima misura del batch **sequenziale** (250 ms di pacing, 12 s di
timeout per query) dava 18 query, 10 hit, hit rate 55,6%, p50 500 ms, p95
641 ms, totale 9.165 ms. Da allora il planner esegue le ladder indipendenti in
un pool bounded (default 4 worker), applica un token bucket per provider
(`pkg/providerbudget`) con cooldown su `429`/`Retry-After`, e consulta una
cache di piano keyed sull'entità normalizzata.

`TestSemanticSearchLiveProviderMeasuresHitRateAndLatency` (opt-in,
`VELOX_SEMANTIC_SEARCH_LIVE=1`) esegue ora tre configurazioni sullo stesso
piano Garlasco da 18 query, contro l'API pubblica di Wikimedia Commons:

| Configurazione | Dispatch | Hit | Hit rate | p50 | p95 | Totale | Cache hit | Provider query |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| baseline: Commons per tutte, no cache | 18 | 10 | 0,556 | 239 ms | 435 ms | 3.484 ms | 0 | 18 |
| routing: persone non inviate a Commons | 13 + **5 route skip** | 10 | **0,769** | 219 ms | 270 ms | **2.975 ms** | 0 | 13 |
| warm: stessa cache, piano ricostruito | 13 + 5 route skip | 10 | 0,769 | 236 ms | 248 ms | **487 ms** | **10** | **3** |

Hit: `RIS`, `Cassazione`, `Corte d'Appello`, `Procura`, `Pavia`, `Vigevano`,
`Brescia`, `reperto`, `pedali`, `bicicletta`. Miss: `Procura di Pavia`,
`ANSA`, `tesi investigativa contestata`. Le 5 route skip sono le persone
(`Chiara Poggi`, `Alberto Stasi`, `Andrea Sempio`, `Angela Taccia`,
`Liborio Cataliotti`): non sono miss, sono query che la lane Commons non può
rispondere e che in produzione vengono instradate su DuckDuckGo.

Lettura:

- **Routing.** La lane Commons perde 5 query impossibili e passa da 10/18 a
  **10/13** sul lavoro che le compete. In produzione l'adapter
  `internet_image_search` instrada `person` su DDG senza fallback Commons, e
  `organization`/`location`/`object` su Commons prima di DDG; un hit Commons
  evita la chiamata web.
- **Scheduler.** Nessun `429` in tutte e tre le run (`rate_limited=0`). Il token
  bucket a 250 ms serializza gli start della lane Commons, quindi i 4 worker
  non possono burstare il provider: il parallelismo resta tra lane diverse
  (DDG, Commons, SearXNG), non dentro la stessa lane.
- **Cache.** La run warm serve 10 hit dalla cache e chiama il provider solo per
  i 3 miss non cachati: **487 ms contro 2.975 ms** (-84%), con lo stesso hit
  rate. La cache non memorizza i miss negativi di proposito: un indice provider
  cambia e una lane challenged si riprende, quindi un miss non è un fatto
  durable.
- **Perché il cold routed guadagna solo il 15% di wall.** Il pacing Commons
  domina (250 ms per start); il guadagno reale del routing è 5 chiamate in meno
  e un hit rate del 77% invece del 56%. La latenza del tentativo (p50 ~240 ms)
  esclude l'attesa del token bucket; il totale la include.

**Limiti:** il probe usa Commons via REST e non la cascata SearXNG/DDG di
produzione; il routing qui dimostra la meccanica (person → lane DDG, non
spedita a Commons), non il blend dei tre provider. Non scarica né verifica i
byte e la cache di QUELLA misura è process-local (LRU 512, TTL 1 h), non
durable. SearXNG locale risponde 403 al formato JSON in questo ambiente. Dal
2026-10-24 il percorso di produzione (adapter `InternetImageSearcher` →
`SemanticAssetSearch`) è attivo e la cache è durable su SQLite: la misura
successiva esercita la catena reale DDG+Commons.

### Blend reale di produzione e cache durable (2026-10-24)

Il planner è ora **attivo in produzione**: `semantic_production_search` vale
`true` come default di configurazione ed è reso esplicito in `config.yaml` e
`config.production.example.yaml`; un `false` esplicito resta la leva di
rollback. La cache di piano è **durable su SQLite** (migrazione
`283_semantic_plan_cache.sql`, tabella `semantic_plan_cache`), con TTL
(`semantic_plan_cache_ttl_seconds`, default 3600) e **invalidazione per
provider** (`InvalidateProvider`, che cancella solo le risposte di una lane e
lascia calde le altre; l'entry conserva provider e lane che hanno risposto).

`TestLiveSemanticProductionBlendRoutesRealProviders`
(`VELOX_SEMANTIC_BLEND_LIVE=1`, in `internal/app/wiring`) costruisce il piano
dal copione Garlasco con il worker Rust reale e lo esegue con la catena di
produzione completa — `internetImageSearchAdapter` → `ImageSearchResolver` →
registry provider → DuckDuckGo/Commons/SearXNG — due volte sullo stesso piano,
con la cache SQLite su file temporaneo:

| Passata | Query | Hit | DDG | Commons | SearXNG | Cache hit | Provider query | p50 | p95 | Totale |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| cold (cache SQLite vuota) | 18 | **18** | 8 | 10 | 0 | 0 | 18 | 6.267 ms | 9.828 ms | 27.145 ms |
| warm (stesso piano, stessa cache) | 18 | **18** | 8 | 10 | 0 | **18** | **0** | 0 | 0 | **2 ms** |

Lettura:

- **Persone → DDG.** Le 5 persone (`Chiara Poggi`, `Alberto Stasi`,
  `Andrea Sempio`, `Angela Taccia`, `Liborio Cataliotti`) sono servite tutte da
  DuckDuckGo; il test verifica che nessuna query `person` venga risposta da
  Commons.
- **Editoriali → Commons prima.** Le 10 risposte Commons coprono `RIS`,
  `Cassazione`, `Corte d'Appello`, `Procura`, `Pavia`, `Vigevano`, `Brescia`,
  `reperto`, `pedali`, `bicicletta`. Dove Commons non ha un asset
  (`Procura di Pavia`, `ANSA`) la cascata ripiega su DDG: 6/6 organizzazioni
  risolte, 0 miss.
- **SearXNG non chiamato su questo corpus.** Ogni query ha già un hit da DDG o
  Commons, quindi la lane di recupero (SearXNG) non viene attivata; il blend
  osservato è DDG+Commons. SearXNG resta la fallback per risposte vuote o
  queries lunghe e non è esercitato da questo copione — limite dichiarato.
- **Warm dopo restart.** La seconda passata ricostruisce il piano e apre la
  stessa cache SQLite: 18/18 hit, 0 chiamate provider, 2 ms. La durabilità è
  coperta anche dai test dell'adapter (riapertura su un nuovo processo, TTL,
  invalidazione mirata, payload malformato = miss).

### Flake rust/timeline: causa e regressione

`verify-changed` poteva eseguire `rust` (`./internal/platform/media/rustexec/...`)
e `timeline` (`./internal/platform/media/...`, che **contiene** rustexec) in
parallelo: la stessa suite girava due volte insieme. `CombinedAudioRenderer`
scriveva il master in `/tmp/pipelinegen-final-audio-<planSHA>.m4a`, nome
**deterministico**, quindi i due processi scrivevano lo stesso file e uno
leggeva un master mentre l'altro lo riscriveva
(`TestListenGateDifferentialABCD`: `rust media render_audio_plan: ffprobe
exited with exit status: 1`, riprodotto con le due `go test` concorrenti).

- Fix: `audio.FinalAudioOutputPath` riserva un nome **unico per invocazione**
  (hash del piano mantenuto per tracciabilità), usato sia da `rustexec` sia da
  `gencore`.
- Regressione: `TestConcurrentSamePlanRendersUseDistinctMasters` rende due
  volte lo stesso piano in parallelo e certifica entrambi i master; sul vecchio
  path fallisce con lo stesso `ffprobe exited with exit status: 1`.
- Regressione di scheduling: `scripts/ci/test_verify_components_concurrency.py`
  (registrato nel componente `rust`) impone che `timeline` dipenda da `rust`,
  così il runner serializza le due suite e non esegue mai due volte lo stesso
  test binary reale.

### Consumo degli asset risolti

Il batch non misura più e basta. Ogni hit conserva il proprio content address e
le proprie location (`SHA256`, `DriveLink`, `MIME`, provider, dimensioni) su
`SemanticProductionUnit.ResolvedImage`. `ApplySemanticResolvedImages` proietta
l'hit sull'annotazione dell'entità nella scena del candidato, con tre regole
deliberate:

1. **Solo hit content-addressed.** Un hit con URL ma senza SHA-256 resta una
   discovery sul piano: l'indice media dell'overlay è content-addressed e un
   binding senza hash verrebbe scartato in silenzio. Non viene promesso come
   card.
2. **Solo composizioni mono-identità.** Una ricerca che copre due nomi è stata
   fatta con una query sola; applicarla a entrambi asserirebbe un match che la
   ricerca non ha fatto. Restano allo stage media, che è identity-aware.
3. **Il media verificato vince.** `projectEntityImageBindings` preserva il
   binding `bound` con hash quando non trova un candidato migliore, e lo
   sostituisce quando il fan-out per-segmento produce un asset `resolved`.

I test che chiudono il ciclo sono `TestSemanticSearchBatchResolvedHitBecomesAnOverlayBinding`
(batch reale → binding overlay) e i quattro casi di
`processor_semantic_plan_binding_test.go` (preserva, sostituisce, rifiuta senza
hash).

### DDG: dal silenzio all'errore tipizzato

Il 2026-10-24 DuckDuckGo ha risposto con la challenge anti-bot (`HTTP 202`,
body `{"challenge":{"cc":"sre"...}}`) sia al path `/i.js` sia a
`html.duckduckgo.com`. Il codice la trattava come "zero risultati" e ritentava
tre volte, quindi ogni query perdeva ~1,4 s e il chiamante non poteva
distinguere un blocco da un corpus vuoto.

Ora:

- `internal/platform/duckduckgo` riconosce la challenge (202/403, envelope JSON,
  anomaly modal) e restituisce `ErrChallenged`;
- un breaker process-local di **3 minuti** (o il `Retry-After` più lungo) fa
  fallire subito le query successive, senza round trip;
- il provider immagini traduce il cooldown in errore tipizzato invece di una
  lista vuota; `SearchBest` esclude solo DDG e tiene i fratelli;
- `internetImageSearchAdapter` non abortisce più la retrieval quando DDG
  fallisce: Commons e SearXNG vengono comunque interrogati.

Misura live dal container (`DDG_LIVE_PROBE=1`): quando DDG risponde, 8 candidati
in **544 ms** (due richieste: pagina vqd + `/i.js`). Quando è in challenge, la
prima query apre il cooldown e le successive non effettuano richieste.

**Limiti dichiarati:** i numeri della tabella planner-level sono *lavoro
pianificato*; la misura live sopra è l'unica esecuzione reale e usa Commons, non
la cascata di produzione. Nessun MP4 è stato prodotto. Il batch planner di
produzione esiste ed è cablato dietro il flag
`VELOX_SCRIPTS_SEMANTIC_PRODUCTION_SEARCH`; gli hit diventano overlay solo se
arrivano da una riga durable (hash presente). La cache è process-local: non
sopravvive a un restart e non è condivisa tra processi. La ricerca per-segmento
della fase media resta attiva: finché il piano non precede il fan-out, le due
retrieval coesistono e il costo è la somma, non il massimo.

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

## Altri difetti trovati e corretti nell'ultimo passaggio

### 4. `Vigevano` e `RIS` non erano tipizzati

`Vigevano` era assente perché il POS model la legge come forma verbale ("vigevano") e il candidato veniva scartato. La città è ora nel seed curato delle località: `curated_name_span` la emette come `LOCATION` anche quando il tagger la legge come verbo. `RIS` era un `VISUAL_CONCEPT`; ora è una cue `ORG` nel pack legale italiano. Risultato: entità **20/20**, typed **20/20**, **0 type warning**.

### 5. La consolidazione non era consumabile dal planner

Aggiunto `visual_consolidation` al manifest e `SemanticCandidateBundle.Consolidation` al confine Go. Il validatore rifiuta scene/identità/fatti/gruppi sconosciuti, span o query non grounded, fatti senza rappresentazione né motivo e metriche incoerenti. Il test reale `TestSemanticEngineRealBinaryConsolidatesGarlascoGold` verifica 8/8 fatti, 31 job, 31 query, 1 timeline, 3 card, 4 event link e `33`.

### 6. Due bug latenti del contratto Go

Il test Garlasco ha scoperto che `projectSemanticGroups` rifiutava i tipi `step_sequence` e `fact_comparison` e pretendeva `text == evidence` anche per i gruppi fact-backed, dove `text` è la micro-summary e `evidence` è la frase verbatim. Ora i due tipi sono validi e l'uguaglianza è richiesta solo per i gruppi derivati direttamente dal source; i gruppi fact-backed portano il label parafrasato e l'evidenza separata.

## Test del motore

`RUST_ICU_MAJOR_VERSION_NUMBER=70 cargo test -p semantic-core -p semantic-engine`:

- **136** test `semantic-core` passati;
- **23** test lib `semantic-engine` passati;
- **68** test di integrazione `semantic-engine` passati (62 del motore + 6 di consolidamento);
- **0 falliti**.

La variabile ICU 70 è necessaria su questa macchina locale; la release canonica resta ICU 72 (Bookworm).

## 3K — cosa è cambiato rispetto al report 2026-10-23

Fixture `sempio_chiara_poggi_youtube_3k.txt`: **3.127 parole, 14 scene**, fingerprint `ec4ca63f533aaf18`.

| Sintomo pre-fix | Ora |
|---|---|
| `RITROVAMENTO`, `Chiara`, `Poggi` come `LOCATION` | **0** |
| Gruppo `comparison` da 5 membri | **0** gruppi oltre 2 membri |
| `LOCATION` totali | 5: `Garlasco`, `Pavia` (più le occorrenze interne) |
| Persone | 24 menzioni `PERSON` su 6 superfici: `Chiara Poggi`, `Andrea Sempio`, `Sempio`, `Poggi`, `Alberto Stasi`, `Stasi` |
| Fatti | 6: 5 su `Alberto Stasi` + 1 `under_investigation` su `Andrea Sempio` |
| Coverage | **5/6** fatti coperti dalle proposte raw (scoperto: `under_investigation`); la consolidazione copre **6/6** con una fact card |
| Query | 1.355 entry, 1.363 exact (451 uniche) |
| Consolidamento | **1.359 proposte → 30 candidati**, **1.355 entry → 13 job**, **2.717 query → 13**; **6/6 fatti canonici rappresentati** (raw 5/6) |
| Story graph | 12 nodi, 6 edge (6 cross-scene) |

**Caveat:** gli anni 2014/2016/2017/2019/2020 sono nel testo della fixture, non sono un errore del motore. La sequenza documentata 2009–2015 è verificata dalla fixture separata `garlasco_chronology_corrected.txt`.

## Fixture storica da 2.797 parole

La fixture `garlasco_full.txt` (12 scene) resta un controllo di contratto: 731 menzioni, 7 fatti, **5/7 fatti coperti** dalle proposte raw (2 non coperti), 1.367 entry di ricerca; story graph 15 nodi/7 edge (5 cross-scene). La consolidazione copre invece **7/7 fatti canonici**: i due fatti senza proposta raw diventano fact card contestuali. Produzione: **1.377 proposte → 71 candidati**, **1.367 entry → 43 job di ricerca**, **2.730 query → 43**. Non è un gold: serve a verificare che il contratto regga anche sul copione storico più lungo.

## Chronology corrected — run di controllo

`custom --text-file garlasco_chronology_corrected.txt --gold garlasco_chronology_gold.json`:

- **PASS** `chronology_gold`: `[2009 acquitted, 2011 acquittal_confirmed, 2013 annulled_with_remand, 2014 convicted, 2015 final]`;
- **PASS** `entity_linking` (24 persone, 5 luoghi), `timeline_completeness`, `visual_coverage` 5/6 raw, `story_graph` 12 nodi/6 edge, `search_feedback` 1.355 entry (ladder 4 rung); consolidazione 6/6 fatti canonici;
- **NOT_RUN** `charts_grounded` (nessuna proposta chart) e `comparison_precision` (nessun gruppo comparison).

## T05 — corpus score: cosa è eseguibile e cosa no

**Eseguito (meccanico):**

- `charts_digits`, `charts_spelled`, `charts_dates_never_bars`: **5/5 PASS**;
- concetti visuali core sul gold 2.013: **15/15**;
- query grounding: **0/4.358** non grounded;
- corpus 3K: 1.355 entry, 1.363 exact (451 uniche), coverage raw **5/6** (consolidata **6/6**), 0 LOCATION vietate.

**Non eseguito:**

- recall/precision **semantica di corpus** per oggetti e keyword visuali. Esiste una sola baseline congelata (2.013 parole) e una fixture 3K senza annotazioni indipendenti. Il run 3K misura contratto, grounding, coverage e volume, non la pertinenza delle query. Un numero di corpus richiederebbe almeno 2–3 copioni annotati da una persona diversa dal motore. Il report non lo inventa: resta `NOT_RUN`.

## Cosa resta NEEDS_REVIEW

1. **Provenienza**: il gold è ricostruito, non indipendente.
2. **Volume query raw**: 1.094 entry e 4.424 query (2.212 rung di feedback + 2.212 suggerimenti) per 2.013 parole, 72,9 per scena. Il grounding è perfetto e la consolidazione le riduce a **31 query di produzione**, ma l'inventario raw resta volontariamente esaustivo: non è un budget editoriale.
3. **Named share 0,15**: solo il 15% delle exact mention uniche nomina un'entità/concetto del gold; i concetti visuali generici non sono annotati, quindi non è una precision umana.
4. **Job esterni reali**: la riduzione 1.094 → 31 è misurata nel manifest (`estimated_search_jobs`), non su chiamate effettive a PipelineGen/provider. Il planner non consuma ancora `SemanticCandidateBundle.Consolidation`: il prossimo passo è il wiring, non un altro numero nel report.

## Distanza aggiornata

| Salto | Stato dopo questi run |
|---|---|
| **1. Gold** | Baseline T01/T05 **verde sui gate semantici** (0 FAIL). Entità 20/20, date 11/11, fatti 5/5, consolidazione 8/8. Restano 3 review item e manca il gold **indipendente**/corpus annotato. |
| **2. Fatti oltre legale/facility** | Invariato: il gold misura il dominio legale. I fix erano attribuzione/parsing/tipizzazione, non generalità. |
| **3. Loop fino al video** | Invariato: planner `failed/retry`, chart→video e certificazione del render non sono esercitati. La consolidazione prepara il piano, non lo renderizza. |

La frase secca: **il motore è preciso, grounded e ora selettivo; non è ancora certificato come produttore di video.** La baseline distingue ciò che è misurato (comprensione + selezione) da ciò che resta esterno (job reali, frame, MP4).

## Prossimi passi

1. **Wiring planner**: consumare `Consolidation.ProductionCandidates` in PipelineGen e sostituire le 1.094 entry raw con i 31 asset job; misurare job, asset, frame e tempo reali.
2. **Budget/qualità**: valutare i 73 candidati su un campione etichettato; il taglio a 31 ricerche è una scelta deterministica, non una precision umana.
3. **Corpus**: annotare altri 2–3 copioni per trasformare la baseline in un vero score T05.
4. **Generalità**: schema evento/ruolo/predicato oltre i pack legali/facility.
5. **Video**: planner `failed/retry`, chart→video, certificazione render.

## Comandi riproducibili

```bash
cd BrainRust
RUST_ICU_MAJOR_VERSION_NUMBER=70 cargo test -p semantic-core -p semantic-engine --release
RUST_ICU_MAJOR_VERSION_NUMBER=70 cargo build --release -p semantic-engine

cd ../refactored
W="../BrainRust/target/release/semantic-engine"
python3 semantic_acceptance/garlasco_2013_gold_score.py --worker "$W" --repeat 2
python3 semantic_acceptance/garlasco_3k_report.py --worker "$W"

python3 semantic_acceptance/semantic_today_acceptance.py selftest --worker "$W"
python3 semantic_acceptance/semantic_today_acceptance.py run --worker "$W"
python3 semantic_acceptance/semantic_today_acceptance.py charts --worker "$W"

python3 semantic_acceptance/semantic_today_acceptance.py custom --worker "$W" \
  --text-file semantic_acceptance/fixtures/garlasco_chronology_corrected.txt \
  --scene-words 220 \
  --gold semantic_acceptance/fixtures/garlasco_chronology_gold.json

python3 semantic_acceptance/semantic_today_acceptance.py custom --worker "$W" \
  --text-file semantic_acceptance/fixtures/comparison_precision.txt \
  --scene-words 220

VELOX_RUST_SEMANTIC_ENGINE_PATH="$PWD/../BrainRust/target/release/semantic-engine" \
  go test ./internal/platform/media/rustexec/ -run TestSemanticEngineRealBinary -count=1
```
