# TICKET — Person-image identity gap: retrieval precision and reference verification (2026-11-18)

- **Stato:** OPEN
- **Priorità:** P1
- **Owner:** image pipeline (retrieval → visual verification → selection → binding)
- **Origine:** audit live dei provider del 2026-11-18 su 13 entità PERSON, con ispezione umana dei candidati scaricati.
- **Documenti correlati:**
  - `TICKET-IMAGE-PIPELINE-STRUCTURAL-REMEDIATION-2026-11-14.md` (96/96 chiusi; questo ticket non riapre IMG-028/031/032/033)
  - `entity-image-pipeline-flow.md` (contratto hit → verified → selected → materialized → bound)
  - `vision-inference-separation.md` (client/modello VLM separato dal testo)
  - `docs/qa/PERSON-ENTITY-MAX-TESTS.md` (matrice PERSON e harness live)
  - `BrainRust/docs/tickets/TICKET-28-imagecatalog-retirement.md` (il catalogo Rust resta ritirato)
- **Regola:** nessuna immagine non verificata può essere bindata; nessuna asserzione può essere indebolita per far passare il ticket; nessun provider nuovo entra nel fan-out senza una prova misurata.

## 1. Problema

La pipeline sa trovare immagini che corrispondono al **testo** della query, ma non sa dimostrare che il **pixel** mostri la persona richiesta.

Per una richiesta `QueryClass=PERSON` il percorso live è ancora prevalentemente lessicale:

```text
nome entità
  → DDG (query breve, eventualmente + "portrait")
  → dedup per URL
  → download guardato (SSRF, redirect, MIME, byte/pixel)
  → VerifyVidRushImageBytes (MIME, dimensioni, SHA-256, pHash)
  → gate semantico VLM opzionale
  → catalogo / binding (richiede SemanticStatus=accepted)
```

L’identità entra solo nel penultimo passaggio, e solo se lo scorer è agganciato. Il risultato può essere uno dei tre:

1. **immagine sbagliata** — il VLM accetta un falso positivo;
2. **nessuna immagine** — non c’è scorer, oppure il VLM rifiuta tutto, oppure il catalogo non ha un verdict riutilizzabile;
3. **immagine giusta** — il VLM riconosce la persona e la accetta.

Il sistema non distingue a monte questi tre esiti: dipende dalla fama della persona, dalla query del provider e dalla configurazione del VLM.

## 2. Impatto

- **Qualità editoriale:** una persona nota può essere sostituita da un omonimo, una modella stock, un poster, una banconota o un frame di un film.
- **Fiducia:** il report può contare immagini `verified`/`bound` che sono tecnicamente valide ma semanticamente sbagliate.
- **Costo:** il VLM viene chiamato su candidati che un filtro di identità avrebbe scartato prima del download.
- **Copertura:** le persone non enciclopediche non hanno un verdict possibile; senza un’immagine di riferimento il VLM non può “conoscere” il soggetto.
- **Manutenzione:** ogni nuovo provider o metamotore aumenta la recall, non la precisione, e sposta il costo sul gate successivo.

## 3. Evidenza live

### 3.1 Metodo

- 13 entità PERSON, 6 candidati per entità.
- Stesso searcher DDG e stesso downloader guardato della pipeline (`DownloadVidRushImageForCandidate`, `VerifyVidRushImageBytes`).
- Run separata contro SearXNG (`http://127.0.0.1:8080`).
- Ispezione umana dei file salvati e dei contact sheet; **nessun VLM usato per il giudizio**.
- Artefatti in `refactored/tmp/person-image-live/` e `refactored/tmp/person-image-searxng/` (gitignorati).

Comandi:

```sh
cd refactored
PERSON_IMAGE_VISUAL_DUMP=1 \
PERSON_IMAGE_VISUAL_DUMP_SOURCE=ddg \
PERSON_IMAGE_VISUAL_DUMP_OUT="$PWD/tmp/person-image-live" \
go test ./internal/capabilities/images -run TestPersonImageVisualDump -v -count=1 -timeout 15m

PERSON_IMAGE_VISUAL_DUMP=1 \
PERSON_IMAGE_VISUAL_DUMP_SOURCE=searxng \
SEARXNG_URL=http://127.0.0.1:8080 \
PERSON_IMAGE_VISUAL_DUMP_OUT="$PWD/tmp/person-image-searxng" \
go test ./internal/capabilities/images -run TestPersonImageVisualDump -v -count=1 -timeout 15m
```

### 3.2 Numeri tecnici

| Fonte | Candidati | File validi | Rifiuti | Duplicati | Tempo |
|---|---:|---:|---:|---:|---:|
| DuckDuckGo | 78 | 74 | 4 | 0 | 73.51 s |
| SearXNG | 78 | 46 | 32 | 0 | 72.49 s |

I rifiuti SearXNG sono in gran parte HTTP 403 su thumbnail/hotlink. Non sono un problema di qualità semantica: sono un problema di acquisizione.

### 3.3 Qualità semantica osservata

**DuckDuckGo** (identità riconoscibile / candidati ispezionati):

| Entità | Esito | Note |
|---|---:|---|
| Mike Tyson | 6/6 | identità corretta, ma solo 1 ritratto pulito; poster, bibite, hotel |
| Chiara Poggi | 1/6 | 3 collage, 1 scena, 1 donna sbagliata |
| Alberto Stasi | 3/6 | uomini a caso |
| Michael Burry | 6/6 | Burry corretto, nessun frame con Bale |
| George Washington | 6/6 | nessuna banconota |
| José María Aznar | 6/6 | — |
| Conan O’Brien | 6/6 | — |
| Michael B. Jordan | 6/6 | — |
| Bruce Lee | 4/4 | URL rifiutati tecnicamente |
| Hayao Miyazaki | 2/6 | — |
| Steve Jobs | 5/5 | 1 URL 403 |
| Gianluca Ferretti | 0/5 | 5 uomini generici + template CV; rifiuti tecnici |
| Elon Musk | 6/6 | — |

**SearXNG** (identità riconoscibile / candidati ispezionati):

| Entità | Esito | Note |
|---|---:|---|
| Mike Tyson | 1/4 | hotel, poster, bibita |
| Chiara Poggi | 0/6 | orecchino, modelle stock, Venezia |
| Alberto Stasi | 0/3 | uomo a caso, altro uomo, uccello |
| Michael Burry | 2/3 | 1 frame con Christian Bale |
| George Washington | 2/4 | banconota da 1 dollaro |
| José María Aznar | 0/2 | Brooklyn Bridge, campanile |
| Conan O’Brien | 1/3 | poster di Detective Conan |
| Michael B. Jordan | 3/4 | — |
| Bruce Lee | 2/2 | — |
| Hayao Miyazaki | 2/4 | — |
| Elon Musk | 2/3 | razzo |
| Steve Jobs | 3/4 | Minecraft, computer Apple |
| Gianluca Ferretti | 0/4 | coppia, coppetta Ferrero, cameriere, Clarins |

La run SearXNG è **controfattuale** rispetto al runtime: per una query `PERSON` corta con risultati DDG, `internet_image_search.go` non chiama SearXNG. Serve a rispondere alla domanda “SearXNG può sostituire DDG per le persone?”, e la risposta è no.

### 3.4 Cosa dimostra e cosa non dimostra

Dimostra:

- il bacino grezzo è rumoroso;
- la verifica tecnica accetta immagini semanticamente sbagliate;
- la composizione degli engine SearXNG è dominata da stock/Pinterest/Artic;
- le query brevi non disambiguano omonimi e nomi comuni.

Non dimostra:

- che il percorso live finale binderebbe tutti questi file (il gate VLM può rifiutarli);
- che il VLM sia configurato o meno nell’ambiente;
- che le query del test siano identiche a quelle di produzione: alcune sono più specifiche (`Mike Tyson 1991`, `Steve Jobs Stanford 2005`, `Gianluca Ferretti ingegnere`);
- il comportamento end-to-end di un job reale.

Il dump misura il **candidate basin**, non il binding finale. È un test della fonte e della verifica tecnica, non del gate semantico.

## 4. Cause

### 4.1 Retrieval lessicale, non identity resolution

“Chiara Poggi” è una stringa. Il motore restituisce ciò che è rilevante per quella stringa, non ciò che contiene il suo volto. Omonimie, nomi comuni e personaggi omonimi (Conan O’Brien / Detective Conan) non sono disambiguati.

### 4.2 Ranking per qualità dell’immagine, non per identità

`SearchBest` ordina per `resultQualityScore`: score upstream, risoluzione, bonus licenza. Un ritratto sbagliato in 4K batte un ritratto giusto in 800 px. Non esiste un segnale “questo volto è quello richiesto”.

### 4.3 Verifica tecnica scambiata per verifica di contenuto

`VerifyVidRushImageBytes` certifica MIME, byte, pixel, dimensioni, SHA-256 e pHash. È necessario, ma non dice nulla su chi sia il soggetto. Le immagini stock di modelle e gli uomini generici passano tutte.

### 4.4 Gate semantico opzionale e senza riferimento

`MediaResolverImageStage.WithSemanticScorer` è opzionale. Se non c’è scorer:

- discovery fail-open: i candidati restano `pending`;
- binding fail-closed: richiede `accepted`;
- esito: nessuna immagine.

Se c’è scorer, `OllamaPersonImageScorer` accetta con:

```go
verdict.IdentityMatch && !verdict.Collage && verdict.Confidence >= 0.85
```

`single_person` è riportato ma non è un veto. Il prompt vieta esplicitamente di usare query, URL e metadati come prova di identità: è corretto, ma lascia tutta la decisione a un modello generativo che non ha un’immagine di riferimento. La `confidence` è autoreferenziale, non una probabilità calibrata. Il budget di tentativi per identità può inoltre fermarsi prima del candidato giusto.

### 4.5 Commons viene saltato per i PERSON

In `internet_image_search.go` il ramo `personRoute` non interroga Commons. Il commento dice:

> “A person is the opposite case: Commons returns nothing for proper names, so it is not asked at all.”

La probe Wikipedia dice altro: 8/13 entità hanno una pageimage utilizzabile.

| Entità | Pageimage Wikipedia |
|---|---:|
| Mike Tyson | 844×1126 |
| George Washington | 4628×5619 |
| José María Aznar | 1050×1575 |
| Conan O’Brien | 2475×3712 |
| Michael B. Jordan | 1104×1472 |
| Bruce Lee | 2017×2516 |
| Elon Musk | 2617×3489 |
| Steve Jobs | 2097×2792 |

Mancano Poggi, Stasi, Burry e Ferretti; Miyazaki ha 342×519, sotto la soglia. La ricerca Commons per Poggi restituisce solo libretti d’opera; per Ferretti un PDF non correlato; per Stasi/Burry la probe rapida ha preso 429, quindi il dato non è conclusivo.

Due precisazioni:

- il provider `searchWikimediaCommons` cerca **file Commons per nome**, non interroga le pageimage di Wikipedia; la copertura Wikipedia è un segnale, non una garanzia che il provider trovi lo stesso file;
- Commons non è biometrico: una pageimage può essere una statua, una firma o un’immagine fuori epoca. È un prior migliore del web aperto, non una verità.

### 4.6 Query troppo corte e senza contesto

Il runtime usa il nome, al massimo `+ "portrait"`. Non usa professione, epoca, contesto della scena, fonte preferita o negative terms. La variante `portrait` aiuta, ma non disambigua un omonimo.

### 4.7 Il catalogo è rigoroso, ma non popola il cold start

`IsUsableCandidateQuality` richiede `SemanticStatus=accepted`, `VerifierVersion` corrente e una provenienza visiva esplicita (`ollama visual identity verification confirmed`, `manual visual identity review confirmed`, `pinned by media_manifest.v1`). Una riga `accepted` basata solo su nome/query e dimensioni non è riutilizzabile. È corretto, ma per una persona nuova senza VLM o revisione manuale non esiste un modo per creare il primo verdict.

## 5. Perché aggiungere provider non risolve

| Progetto | Categoria | Risolve l’identità? | Perché no |
|---|---|---|---|
| 4get | metamotore | No | stessi upstream Google/Bing/Yandex, stessi rate-limit, stessa SEO |
| Degoog | metamotore | No | stessa classe di SearXNG |
| Websurfx | metamotore Rust | No | stesso problema di scraping, community più piccola |
| YaCy | indice proprio/P2P | No, da solo | copertura debole su persone recenti, pesante, richiede cura dei domini |
| Crawl4AI + Meilisearch/Qdrant | ingestion + indice locale | No, da solo | duplicherebbe `entitycatalog` + pgvector/SigLIP; utile solo offline |
| Google CSE | API ufficiale | No | 100 query/giorno; resta un match testuale |

Il collo di bottiglia è la **precisione**, non la recall. Aggiungere fonti aumenta il numero di candidati sbagliati che il gate deve respingere.

## 6. Piano di risoluzione

### P0 — Diagnosi e osservabilità (nessun cambio di comportamento)

Obiettivo: capire in quale regime gira l’ambiente target.

- Verificare nei log la presenza di `InternetImagesProcessor wired with the shared visual identity verifier`.
- Verificare la configurazione del client vision (`VisionVerdictClient`, `OLLAMA_VISION_*`, modello).
- Distinguere per entità: `NO_QUERY`, `PROVIDER_EMPTY`, `UNVERIFIED`, `NOT_MATERIALIZED`, `IDENTITY_MISMATCH`.
- Registrare accept/reject del VLM per identità, con motivo e verifier version.
- Non cambiare il routing in questa fase.

**Accettazione P0**

- [ ] Un probe live sulle 13 entità riporta, per entità, fonte, candidati, verdict e motivo.
- [ ] Il report distingue “nessun candidato”, “candidato non verificato”, “VLM ha rifiutato”, “VLM ha accettato”.
- [ ] Nessuna modifica al comportamento di produzione.

### P1 — Commons/Wikipedia prima, per i PERSON con pagina

Obiettivo: usare il prior più forte disponibile senza aggiungere servizi.

- Per `PERSON`, provare Wikipedia/Commons **prima** di DDG quando esiste una pagina.
- Usare la pageimage come candidato e, quando possibile, come immagine di riferimento.
- Fallback a DDG se non c’è pagina, se la licenza non è esplicita o se le dimensioni non passano il gate.
- Non saltare Commons in blocco: correggere l’assunzione nel routing.
- Mantenere Commons per `ORGANIZATION`, `BRAND`, `LOCATION`, `OBJECT`.

**Accettazione P1**

- [ ] Per le 8/13 entità con pageimage, il candidato Commons/Wikipedia è valutato prima di DDG.
- [ ] Per Poggi, Stasi, Burry e Ferretti il fallback DDG resta attivo e non viene bloccato.
- [ ] Licenza esplicita e soglia dimensioni sono applicate come oggi.
- [ ] Il percorso editoriale (ORG/BRAND/LOCATION/OBJECT) non regredisce.
- [ ] Test: routing PERSON con/senza pageimage; fallback su errore Commons; nessun bind senza verdict.

### P2 — Verifica basata su riferimento

Obiettivo: sostituire “il VLM riconosce?” con “il volto nel candidato è lo stesso del riferimento?”.

- Memorizzare un volto di riferimento per l’identità (Commons/Wikipedia, catalogo verificato, revisione manuale).
- Rilevare i volti nel candidato.
- Calcolare un embedding (ArcFace/FaceNet o person re-ID) e confrontarlo con il riferimento.
- Applicare una soglia calibrata sul set di audit, non una soglia arbitraria.
- Usare il VLM solo come secondo stadio per layout/collage/contesto, non come unico giudice.
- Per i casi non-portrait (Washington sulla banconota, luoghi, loghi), usare regole per tipo di entità o un pin manuale.
- Se non c’è un riferimento o il match è sotto soglia, restituire “nessuna immagine verificata”: è un esito valido.

**Accettazione P2**

- [ ] Sul set etichettato delle 13 entità: **0 bind di persona sbagliata**.
- [ ] Le entità con reference e candidate corretto vengono accettate; le entità senza reference restano senza immagine, non con un’immagine casuale.
- [ ] Il verdetto registra modello, versione e soglia.
- [ ] Il VLM non è l’unica autorità: un suo falso positivo non basta a bindare.

### P3 — Politica di fonti e query

- Query con contesto: nome + ruolo + epoca, quando la scena lo fornisce.
- Negative terms e deny-list: `stock`, `collage`, `poster`, `Pinterest`, artwork non pertinente.
- Allow-list: Wikimedia, biografie ufficiali, agenzie, testate.
- Ranking che premi la fonte, non solo la risoluzione.
- SearXNG: curare `settings.yml` del container; `engines=` non è affidabile su questa istanza. Tenere Bing/DDG/stock pertinenti, escludere Pinterest/Artic/Openverse/DeviantArt.
- Budget dei candidati **dopo** il filtro di identità, non prima.

**Accettazione P3**

- [ ] La configurazione SearXNG è esplicita e riproducibile nel repo/runbook.
- [ ] Per le query PERSON, i primi 6 risultati non contengono Pinterest/stock generico/artwork non pertinente.
- [ ] Le query di scena usano il contesto disponibile senza trasformare la scena in un vincolo keyword.
- [ ] Nessun nuovo metamotore entra nel fan-out in questo ticket.

## 7. Verifica

### Compile e test mirati

```sh
cd refactored
gofmt -l internal/capabilities/images/person_image_quality_audit_test.go
go test -count=1 -run '^$' \
  ./internal/capabilities/images \
  ./internal/capabilities/images/search \
  ./internal/capabilities/scripts/adapters
git diff --check
```

### Test PERSON esistenti

```sh
cd refactored
go test -count=1 -run 'PersonEntityMax' ./internal/capabilities/images/entitycatalog
go test -count=1 -run 'PersonEntityMax' ./internal/capabilities/scripts/adapters
go test -count=1 ./internal/capabilities/scripts/adapters
```

### Probe visuale (opt-in, non CI)

```sh
cd refactored
PERSON_IMAGE_VISUAL_DUMP=1 \
PERSON_IMAGE_VISUAL_DUMP_SOURCE=ddg \
PERSON_IMAGE_VISUAL_DUMP_OUT="$PWD/tmp/person-image-live" \
go test ./internal/capabilities/images -run TestPersonImageVisualDump -v -count=1 -timeout 15m
```

### Probe Commons/Wikipedia

La probe usata in questo audit interroga `https://en.wikipedia.org/w/api.php` e
`https://it.wikipedia.org/w/api.php` con `prop=pageimages`, poi verifica le
dimensioni. Il provider reale (`searchWikimediaCommons`) va provato a parte:
cerca file Commons per nome, non pageimage. Rispettare il pacing di 2 secondi
già presente in `waitForCommonsRequest`; una raffica di richieste produce 429.

## 8. Rischi e rollback

- **Commons/pageimage sbagliata o fuori epoca.** Mitigazione: usarla come
  riferimento, non come bind automatico; validare licenza, dimensioni e
  contesto.
- **Nuova dipendenza face embedding.** Mitigazione: modello locale, versione
  pinnata, niente upload di volti a servizi esterni; documentare licenza e
  costo.
- **VLM lento/costoso.** Mitigazione: budget per identità, cache dei verdicti,
  face match come pre-filtro.
- **Persone senza pagina.** Mitigazione: accettare “nessuna immagine
  verificata” invece di un’immagine casuale; pin manuale per i casi editoriali
  importanti.
- **Rollback.** Il routing Commons-first e il face match devono essere
  attivabili/disattivabili senza cambiare il contratto del catalogo; il
  fallback DDG resta disponibile.

## 9. Fuori scope

- Ripristinare `BrainRust/imagecatalog` (TICKET-28).
- Indebolire il gate di provenienza o riusare verdicti metadata-only.
- Integrare Pexels/Pixabay nel fan-out: l’integrazione è stata revertita e
  SearXNG li interroga già.
- Aggiungere 4get/Degoog/Websurfx/YaCy/Crawl4AI in questo ticket.
- Cambiare il contratto wire o il formato degli asset.
- Commit/push/deploy: questo ticket è un documento di lavoro.

## 10. Domande aperte

1. Nell’ambiente target `VisionVerdictScorer` è configurato? Con quale modello e
   quale endpoint?
2. Per le persone senza pagina Wikipedia, esiste una fonte di riferimento
   approvata (biografia ufficiale, agenzia, archivio interno)?
3. È accettabile introdurre un modello di face embedding locale? Quale, con
   quale licenza e quale budget di memoria/CPU?
4. Per i personaggi storici, l’epoca è un requisito hard o un criterio di
   ranking?
5. Qual è la politica editoriale quando non c’è un’immagine verificata:
   placeholder, card testuale, o slot vuoto?
6. Il set delle 13 entità è sufficiente come benchmark, o serve un set
   etichettato più ampio prima di calibrare la soglia?

## Appendice A — Query usate nel probe

```text
Mike Tyson 1991
Chiara Poggi
Alberto Stasi
Michael Burry investor
George Washington portrait
José María Aznar
Conan O'Brien
Michael B. Jordan
李小龙 Bruce Lee
宮崎駿 Hayao Miyazaki
Elon Musk
Steve Jobs Stanford 2005
Gianluca Ferretti ingegnere
```

Alcune query sono più specifiche del runtime: servono a misurare la qualità
della fonte, non a replicare esattamente la query di produzione.

## Appendice B — Riferimenti di codice

- `internal/app/wiring/script/internet_image_search.go` — routing PERSON,
  DDG-first, salto Commons, variante `portrait`, fallback SearXNG.
- `internal/capabilities/images/search/provider_selection.go` — `SearchBest`,
  ranking per qualità, dedup per URL.
- `internal/capabilities/images/search_engine_searxng.go` — ricerca SearXNG,
  `categories=images`, nessun `engines=`, licenza `Unknown`.
- `internal/capabilities/images/search_engine_commons.go` — ricerca file
  Commons, licenza esplicita, soglia 800 px / 400k, pacing 2 s.
- `internal/capabilities/scripts/adapters/vidrush_image_validation.go` —
  download guardato e `VerifyVidRushImageBytes` (solo tecnica).
- `internal/capabilities/scripts/adapters/image_semantic_gate.go` — scorer
  opzionale, discovery fail-open.
- `internal/capabilities/scripts/adapters/media_resolver_image_segments.go` —
  filtro fail-closed per PERSON quando lo scorer è presente.
- `internal/capabilities/scripts/adapters/media_resolver_entity_binding.go` —
  binding richiede `SemanticStatus=accepted`.
- `internal/capabilities/images/entitycatalog/port.go` —
  `IsUsableCandidateQuality`, provenienza visiva, verifier version.
- `internal/platform/ollama/adapters/person_image_scorer.go` — prompt VLM,
  `identity_match`, `collage`, `confidence >= 0.85`, budget tentativi.
- `internal/capabilities/images/person_image_quality_audit_test.go` —
  `TestPersonImageVisualDump`, harness opt-in usato per l’audit.

## Appendice C — Stato del lavoro

- Nessuna modifica runtime in questa sessione.
- Nessun provider nuovo integrato.
- Pexels/Pixabay: integrazione revertita; nessun residuo.
- Unico artefatto di codice: `TestPersonImageVisualDump` (opt-in, non CI).
- La probe Commons/Wikipedia è stata eseguita a mano; non è un test CI.
- Il ticket è OPEN; la prima fase eseguibile è P0 (diagnosi), poi P1 (Commons).
