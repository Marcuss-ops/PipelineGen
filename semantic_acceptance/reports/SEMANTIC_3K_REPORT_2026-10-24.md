# Test 3.127 parole — cosa ha trovato il semantic engine (2026-10-24)

Fixture: `/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/semantic_acceptance/fixtures/sempio_chiara_poggi_youtube_3k.txt` (3127 parole, 14 scene da ~220 parole).
Worker: `/home/pierone/src/go-master/projects/Pyt/VeloxEditing/BrainRust/target/release/semantic-engine`.
Fingerprint: `ec4ca63f533aaf18`.

> **Nota sulla cronologia.** Questa fixture contiene nel proprio testo gli anni 2014, 2016, 2017, 2019 e 2020. Il motore li estrae fedelmente: non sono un errore di normalizzazione. La fixture separata `garlasco_chronology_corrected.txt` è quella che verifica la sequenza documentata 2009–2015. Il vecchio report mescolava le due cose.

## T01 — sintomi pre-fix

- LOCATION vietate (`RITROVAMENTO`, `Chiara`, `Poggi`): **0** — nessuna
- Gruppi `comparison` con più di 2 membri: **0** []
- Entità per tipo: DATE=17, LOCATION=5, NUMBER=18, ORG=3, PERSON=24, VISUAL_CONCEPT=641

**Esito T01: i due sintomi del report 2026-10-23 non sono più presenti su questa fixture.**

## Fatti (`entity_facts`)

- **s05** · `legal_outcome` · soggetto `Alberto Stasi` · valore `acquitted` · data `2014` · status `asserted`
- **s05** · `legal_outcome` · soggetto `Alberto Stasi` · valore `acquittal_confirmed` · data `2016` · status `asserted`
- **s05** · `legal_outcome` · soggetto `Alberto Stasi` · valore `convicted` · data `2019` · status `asserted`
- **s05** · `legal_outcome` · soggetto `Alberto Stasi` · valore `annulled_with_remand` · data `2017` · status `asserted`
- **s05** · `legal_finality` · soggetto `Alberto Stasi` · valore `final` · data `2020` · status `asserted`
- **s06** · `investigation_status` · soggetto `Andrea Sempio` · valore `under_investigation` · data `None` · status `asserted`

## Micro-summary

- «Stasi: assoluzione nel 2014» · `micro_summary` · data `2014`
- «Stasi: conferma dell'assoluzione nel 2016» · `micro_summary` · data `2016`
- «Stasi: condanna nel 2019» · `micro_summary` · data `2019`
- «Stasi: annullamento con rinvio nel 2017» · `micro_summary` · data `2017`
- «Stasi: condanna definitiva nel 2020» · `micro_summary` · data `2020`
- «Sempio: indagato» · `micro_summary` · data ``

## Timeline proposte

- s01 · `s01_visual_111` · superfici ['2007', '2007-08-13']
  > Il tredici agosto duemilasette è una data che segna l'inizio di un racconto complesso e doloroso, e per comprendere la vicenda di cui parleremo oggi è il punto esatto da cui partire. In quel periodo Garlasco è una cittadina tranquilla della provincia di Pavia, un luogo dove le famiglie si conoscono da generazioni e dove le estati trascorrono tra il fiume e le strade del centro. È qui che vive Chiara Poggi, una giovane donna il cui nome è destinato a diventare, suo malgrado, il centro di uno dei casi più discussi della cronaca italiana. La sua vita, i suoi progetti e le sue giornate sono documentati dai resoconti di quei giorni, e per noi il compito rimane quello di restare ancorati ai fatti accertati, senza aggiungere nulla che non sia verificabile. Questo è un racconto costruito su dati pubblici, su sentenze e su atti di indagine, perché quando si parla di una vita interrotta la precisione non è una scelta editoriale, ma un dovere. IL NOME CHE TORNA

In una storia come questa esiste sempre un nome che resta ai margini per molto tempo, prima di tornare improvvisamente al centro dell'attenzione. In questa vicenda quel nome è quello di Andrea Sempio. Nell'estate del duemilasette Sempio è un ragazzo del luogo, una persona che conosce quei quartieri e quelle persone, e il cui legame con l'abitazione della famiglia Poggi è un dettaglio che rimane a lungo in secondo piano.
- s05 · `s05_visual_090` · superfici ['2014', '2016', '2017', '2019', '2020']
  > Nel duemilaquattordici il giudice di primo grado pronuncia una prima assoluzione, che nel duemilasedici viene confermata nel giudizio d'appello. Per molti sembra la fine di una vicenda, ma non è così. Nel duemiladiciassette la Cassazione interviene annullando con rinvio la sentenza, e il caso torna in aula con un nuovo giudizio d'appello che si conclude nel duemiladiciannove con una condanna a sedici anni di reclusione. Nel duemilaventi questa condanna diventa definitiva.
- s11 · `s11_visual_093` · superfici ['tredici agosto duemilasette']
  > Sappiamo che il tredici agosto duemilasette una giovane donna è stata uccisa nella sua casa, e che quel fatto ha cambiato per sempre la vita di molte persone.
- s14 · `s14_visual_043` · superfici ['duemilaventicinque', 'ventiquattro maggio']
  > Nel duemilaventicinque le indagini sulla posizione di Sempio concludono la fase preliminare il ventiquattro maggio, con il deposito di cinque consulenze difensive.

## Story graph

- `fact:s05:person_99_acquittal_confirmed_02` · fact · «duemilasedici» · scene ['s05'] · data 2016
- `fact:s05:person_99_acquitted_01` · fact · «duemilaquattordici» · scene ['s05'] · data 2014
- `fact:s05:person_99_annulled_with_remand_04` · fact · «duemiladiciassette» · scene ['s05'] · data 2017
- `fact:s05:person_99_convicted_03` · fact · «duemiladiciannove» · scene ['s05'] · data 2019
- `fact:s05:person_99_final_05` · fact · «duemilaventi» · scene ['s05'] · data 2020
- `fact:s06:person_02_under_investigation_01` · fact · «indagati» · scene ['s06'] · data None
- `identity:location_03` · identity · «Garlasco» · scene ['s01', 's03', 's14'] · data None
- `identity:location_04` · identity · «Pavia» · scene ['s01', 's06'] · data None
- `identity:person_01` · identity · «Chiara Poggi» · scene ['s01', 's02', 's09', 's10', 's11', 's14'] · data None
- `identity:person_02` · identity · «Andrea Sempio» · scene ['s01', 's03', 's05', 's06', 's10', 's11', 's14'] · data None
- `identity:person_99` · identity · «Alberto Stasi» · scene ['s03', 's05', 's10', 's11', 's14'] · data None
- edge `about`: `identity:person_02` → `fact:s06:person_02_under_investigation_01` · cross_scene=True
- edge `about`: `identity:person_99` → `fact:s05:person_99_acquittal_confirmed_02` · cross_scene=True
- edge `about`: `identity:person_99` → `fact:s05:person_99_acquitted_01` · cross_scene=True
- edge `about`: `identity:person_99` → `fact:s05:person_99_annulled_with_remand_04` · cross_scene=True
- edge `about`: `identity:person_99` → `fact:s05:person_99_convicted_03` · cross_scene=True
- edge `about`: `identity:person_99` → `fact:s05:person_99_final_05` · cross_scene=True

## Visual coverage

- s05 · `person_99_acquitted_01` · **covered** · ['timeline'] · importanza 0.90
- s05 · `person_99_annulled_with_remand_04` · **covered** · ['timeline'] · importanza 0.90
- s05 · `person_99_convicted_03` · **covered** · ['timeline'] · importanza 0.90
- s05 · `person_99_final_05` · **covered** · ['timeline'] · importanza 0.90
- s05 · `person_99_acquittal_confirmed_02` · **covered** · ['timeline'] · importanza 0.70
- s06 · `person_02_under_investigation_01` · **uncovered** · [] · importanza 0.90
- totale: 5/6 fatti coperti

## Gruppi semantici

- s01 · chronological · «Cronologia» · 2 membri
  - Nell'estate del duemilasette Sempio è un ragazzo del luogo, una persona che conosce quei quartieri e quelle persone, e il cui legame con l'abitazione della famiglia Poggi è un dettaglio che rimane a lungo in secondo piano.
  - Il tredici agosto duemilasette è una data che segna l'inizio di un racconto complesso e doloroso, e per comprendere la vicenda di cui parleremo oggi è il punto esatto da cui partire.
- s05 · chronological · «Cronologia» · 3 membri
  - Nel duemilaquattordici il giudice di primo grado pronuncia una prima assoluzione, che nel duemilasedici viene confermata nel giudizio d'appello.
  - Nel duemiladiciassette la Cassazione interviene annullando con rinvio la sentenza, e il caso torna in aula con un nuovo giudizio d'appello che si conclude nel duemiladiciannove con una condanna a sedici anni di reclusione.
  - Nel duemilaventi questa condanna diventa definitiva.
- s05 · step_sequence · «Sequenza: 5 tappe» · 5 membri
  - Stasi: assoluzione nel 2014
  - Stasi: conferma dell'assoluzione nel 2016
  - Stasi: annullamento con rinvio nel 2017
  - Stasi: condanna nel 2019
  - Stasi: condanna definitiva nel 2020

## Keyword (`important_words`) più frequenti

Andrea Sempio ×5, Sempio ×5, Chiara Poggi ×4, Alberto Stasi ×4, Garlasco ×3, Poggi ×3, caso giudiziario ×3, Stasi ×3, Pavia ×2, giovane donna ×2, cronaca italiana ×2, fatti accertati ×2, conseguenze reali ×2, sentenza definitiva ×2, passaggio tecnico ×2, nuova fase ×2, reperto conservato ×2, via definitiva ×2, persona coinvolta ×2, racconto complesso ×1, punto esatto ×1, cittadina tranquilla ×1, dati pubblici ×1, scelta editoriale ×1, secondo piano ×1, paese piccolo ×1, tinte forti ×1, percorso giudiziario ×1, famiglie intere ×1, regole precise ×1

## Query

- 1353 entry, 1361 exact_mention (451 uniche)
- duplicati principali: `persone` ×16, `nome` ×14, `indagini` ×14, `caso` ×14, `sempio` ×13, `vicenda` ×12, `tempo` ×12, `procedimento` ×12, `scienza` ×12, `andrea sempio` ×11

## Entità per tipo (campione)

- **DATE** (17 menzioni): duemilasette, duemilaquattordici, duemilasedici, duemiladiciassette, duemiladiciannove, duemilaventi, duemilaventicinque
- **LOCATION** (5 menzioni): Garlasco, Pavia
- **NUMBER** (18 menzioni): tredici, due, sedici, ventiquattro, cinque, diciotto
- **ORG** (3 menzioni): tribunale, Cassazione, Procura di Pavia
- **PERSON** (24 menzioni): Chiara Poggi, Andrea Sempio, Sempio, Poggi, Alberto Stasi, Stasi
- **VISUAL_CONCEPT** (641 menzioni): racconto complesso, punto esatto, cittadina tranquilla, giovane donna, cronaca italiana, fatti accertati, dati pubblici, scelta editoriale, secondo piano, data, doloroso, vicenda, periodo, provincia, luogo, famiglie, generazioni, estati, fiume, strade

## Cosa cambia rispetto al report 2026-10-23

- Il report vecchio elencava `RITROVAMENTO`, `Chiara` e `Poggi` come `LOCATION`: qui la verifica è esplicita e il risultato è sopra.
- Il report vecchio mostrava un gruppo `comparison` da 5 membri: qui la verifica è esplicita e il risultato è sopra.
- Gli anni 2014/2016/2017/2019/2020 restano quelli scritti nella fixture; per la sequenza corretta usare `garlasco_chronology_corrected.txt`.
- Il numero di query resta alto: è un problema di prodotto (budget del planner), non un errore di grounding.

