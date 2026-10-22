# Changelog — 22 ottobre 2026 — PipelineGen (`refactored`)

Lavoro della giornata sul motore semantico Rust e sulla sua accettazione. Quattro
commit su `main`: `7d15e3c61`, `3fdb207f0`, `11a3bad60`, `d2e0e68c1`
(88 file, +19.397/−2.369 rispetto all'inizio della giornata).

## 1. Regole linguistiche spostate in lessici versionati

`3fdb207f0`, `d2e0e68c1`

- Ogni vocabolario curato vive ora in `rust/lexicon/`, non più nel codice Rust:
  `stopwords`, `object_vocabulary`, `location_cues`, `scale_words`,
  `legal_patterns`, `facility_facts`, `process_phases`, `discourse_cues`,
  `relation_cues`, `entity_gazetteer`, tabella CLDR dei mesi, `language_profiles`,
  `orthography`, `months`.
- `semantic-core::lexicon` è l'unico loader: default incorporati con
  `include_str!`, override per file via `SEMANTIC_LEXICON_DIR`, fallback con
  warning su override malformato.
- `LEXICON_VERSION = lexicon.v4` entra in `AnalyzerVersions` e nella cache key
  del worker, così un cambio di tabella invalida i risultati precedenti.
- Le cue di discorso duplicate sono state accorpate in `discourse_cues.json`:
  Phrase Impact, relations, causal, comparison e temporal leggono la stessa
  tabella invece di una lista per modulo.
- Rimosso il leakage di dominio demo: punteggio/compounding food e
  `SUBJECT_PHRASES` in VisualNER, le quattro tabelle `DEFAULT_*`
  mediterranee/sportive in `mediasampler` (ora i keyword arrivano solo dalla
  richiesta, i test forniscono il proprio dominio).
- Morfologia verbale basata su UDPipe al posto dei suffissi italiani:
  `Token.lemma`/`Token.upos` dal modello installato, `verb.rs` riconosce un
  predicato da tag `VERB`/`AUX` o lemma infinitivo diverso dalla superficie;
  `verbal_morphology.json`, `VERB_ENDINGS` e `NOUN_ENDINGS` sono stati rimossi.
  L'allineamento del tokenizer gestisce le contrazioni UDPipe (`del` → `di`+`il`)
  e resta locale allo scan.
- Le convenzioni per lingua sono dati: `language_profiles.json` contiene per
  codice ISO-639-1 (più un `default`) treebank UDPipe, `date_order`,
  separatori delle migliaia/decimali e la coppia
  `verb_filter`/`infinitive_suffixes`. `pos.rs`, `dates.rs`, `numbers.rs` e
  `verb.rs` non branchano più su codici lingua (`!= "en"`, `is_italian()`).
- `orthography.json` possiede le abbreviazioni di frase (`common`, `corporate`,
  `by_language`) e i marcatori d'era; il profilo di scrittura (`script`,
  `scripts[].cased`, `capitalized_nouns`) rende il pivot
  `uppercase == proper name` una funzione `uppercase_is_name_evidence`, così i
  nomi Han/Giapponesi/Arabi/Thai sono visibili al gazetteer e i nomi comuni
  tedeschi maiuscoli non diventano persone.
- Corretto lo scan dei mesi multi-byte in `dates.rs`: il cursore avanzava di un
  byte e finiva dentro un code point con nomi cirillici/cechi/turchi
  (panic del worker). Ora avanza di un carattere, `to_source` rifiuta indici
  non di confine e a parità di posizione vince la forma più lunga
  (`март` vs `марта`).

## 2. Gazetteer GeoNames e misura su Garlasco

`3fdb207f0`, `d2e0e68c1`

- `entity_gazetteer.json` ha ora un indice reale: 69.433 località popolate e
  446.095 alias da GeoNames `cities500` (popolazione ≥ 5.000 più capitali,
  CC BY 4.0), rigenerabile con `lexicon/tools/build_geonames_gazetteer.py`.
  Lookup O(1), tabella di ambiguità per lingua (`it`: 306 parole comuni come
  `corte`/`come`) e guard di capitalizzazione evitano che un nome comune
  diventi LOCATION. Ogni script non latino ha slot dedicati: `北京`, `東京`,
  `서울` sono nell'indice.
- Misura su Garlasco (`semantic_acceptance/gazetteer_garlasco_measure.py`,
  report `gazetteer_garlasco_report.md`): recall delle località gold da 1/2 a
  2/2 sia in VisualNER sia nel manifest del semantic-engine (aggiunge `Pavia`,
  nessuna rimozione, nessun nuovo falso positivo; `Corte` e `Come` restano
  esclusi). L'upgrade ORG legale ora richiede lo stesso inizio di entità e non
  consuma più la LOCATION annidata `Pavia`.

## 3. Bullet, fatti legali e micro-summary condivisi

`3fdb207f0`, `d2e0e68c1`

- `semantic-core::bullets` è l'unico algoritmo dei bullet: genera i candidati
  (span di frase + segnali visivi/misura grounded) e possiede l'unica tabella di
  salienza. VisualNER proietta i candidati, il semantic engine porta lo score su
  ogni candidato, `SemanticBulletSpan.score` lo trasporta e Phrase Impact ordina
  il pool con `bullets::editorial_priority(importance, score)`.
- `semantic-engine/src/legal.rs` estrae fatti legali/procedurali conservativi
  (assoluzione, condanna, conferma, annullamento con rinvio, definitività,
  stato dell'indagine, atti di mandato/udienza/conclusione, durata della pena,
  età, partner). Il soggetto è un'entità PERSON/ORG/ROLE grounded nella stessa
  frase, il predicato datato scatta solo con la data in quella frase, la
  negazione resta in `status`.
- `EntityFact.normalized_value` porta l'esito, `normalized_date` la data ISO:
  due esiti diversi nello stesso giorno non collassano più in un solo valore.
- `semantic-engine/src/micro.rs` rende solo fatti `asserted` in micro-riassunti
  e gruppi comparison/sequence ordinati, mantenendo `evidence_fact_ids` e senza
  riscrivere `not_convicted` come innocenza.
- `semantic-engine/src/grounding.rs` (mai dichiarato con `mod grounding;`,
  quindi codice compilato-out duplicato) è stato rimosso; i tre helper locali
  `retain_grounded_*`, il predicato `is_visual_entity` e `fnv1a_hex` sono
  collassati sulle versioni condivise in `semantic-core`.

## 4. Accettazione e verifica

`3fdb207f0`, `11a3bad60`, `d2e0e68c1`

- Nuovo harness `semantic_acceptance/`: `semantic_today_acceptance.py`
  (selftest / batteria Garlasco / testi custom), `garlasco_semantic_gold_test.py`,
  `chart_bullet_certifier.py`, `scaling_test.py` (2K/3K/4K/5K parole),
  `gazetteer_garlasco_measure.py` e le fixture (`garlasco_full.txt`,
  `garlasco_gold.json`, `sempio_chiara_poggi_youtube_3k.txt`).
- `config/verify-components.json`: `semantic_acceptance/` è mappato sulla
  componente Rust, quindi i file di accettazione finiscono nei gate giusti.
- `RUST_SEMANTIC_ENGINE_TODO.md` registra il completamento del run
  production-shaped Garlasco end-to-end: job locale
  `job_1792623461506911130_c383c9de`, run semantico
  `run_6b6d9b09-de07-4fb8-9281-58677859a45f`, 36 overlay, final job remoto
  `job_0e4e1a494ddfaaa7`; MP4 finale 1920×1080 H.264 + AAC, 24 fps,
  1.187,333 s, 714.461.217 byte, SHA-256
  `a4fa2d3f100ae0d295706d567ace92b3b66a82b122c646bf823e603648c7d4cf`.
- Resta aperto un punto esplicito: la qualità visiva/semantica del render
  Garlasco e la decodifica pulita del video. `ffprobe` conferma stream e durate
  coerenti, ma la decodifica completa `ffmpeg` logga warning H.264
  (missing reference / illegal buffer state) e DTS non monotoni. Il run prova
  che l'assemblaggio finale è avvenuto, non che ogni frame decodifichi
  correttamente.
- Gate registrati nel commit `3fdb207f0`: `cargo test --release --workspace
  --no-fail-fast` 457 passed / 0 failed su 20 suite,
  `cargo clippy --release --workspace --all-targets -- -D warnings` pulito,
  `cargo fmt --all -- --check` pulito, replay byte-identico doppio per
  `visualner` (modello + fallback) e `semantic-engine`. Per `d2e0e68c1` i test
  citati sono quelli mirati su dates/lexicon/numbers/sentences e gli estrattori
  VisualNER.

## 5. Retention SQLite

`3fdb207f0`

- `RetentionResult` espone `pre_wal_bytes` e documenta che
  `pre_size_bytes`/`post_size_bytes` misurano solo il file principale del
  database: `bytes_reclaimed` resta significativo anche quando il checkpoint
  finale non riesce a troncare il WAL su un servizio vivo. `wal_bytes` è la
  dimensione del `-wal` dopo il run: pagine in attesa, non spazio perso.

## 6. Payload operativi Garlasco

`3fdb207f0`

- Aggiunti i payload `resubmit_r10` (`01_cronologia`, `02_procedimento`,
  `03_stasi_sentenza`, `04_nuova_indagine`, revisioni r10–r13), lo script
  `make_r13.py`, `run_r13.sh` e il log di confronto.

## Nota

Questo changelog è stato scritto a posteriori, leggendo i quattro commit della
giornata; descrive il lavoro già presente su `main`, non aggiunge modifiche di
codice. Il commit che lo aggiunge è solo documentazione; come controllo rapido
sono stati rieseguiti i test di retention SQLite
(`go test ./internal/platform/sqlite -run Retention`, ok).
