# SearXNG image policy

- **Stato:** attivo dal 2026-11-18, corretto lo stesso giorno (vedi *Cronologia*).
- **Ambito:** istanza `pipelinegen-searxng` usata da PipelineGen per la ricerca
  immagini di fallback.
- **File di configurazione:** `refactored/searxng/settings.yml` (versionato).
- **Versione minima richiesta:** `searxng/searxng:2026.11.18` (le engine
  immagini della 2026.6.4 sono rotte su questo host).
- **Ticket di riferimento:** `docs/operations/TICKET-PERSON-IMAGE-IDENTITY-2026-11-18.md` (P3).

## Perché esiste

Il default upstream della categoria `images` è dominato da artwork, stock e
mood board. Sull'audit delle 13 entità PERSON del 2026-11-18 le prime sei
posizioni per query erano **47/78 su host di rumore** (Artic, Pinterest,
Pexels, Flickr, Openverse): il budget di download e di verifica veniva speso su
immagini che non possono mostrare la persona richiesta.

PipelineGen non dipende da SearXNG per gli asset con licenza: Wikipedia,
Wikimedia Commons e DuckDuckGo hanno provider dedicati. SearXNG è il fallback
per ciò che i provider diretti non coprono, quindi può essere reso più preciso
senza perdere recall licenziata.

## Configurazione

Il container monta il file direttamente:

```sh
docker inspect pipelinegen-searxng \
  --format '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{"\n"}}{{end}}'
# .../refactored/searxng/settings.yml -> /etc/searxng/settings.yml
```

Il file mantiene `use_default_settings: true` (tutti gli engine non elencati
restano quelli upstream) e disabilita per nome:

| Engine | Motivo |
|---|---|
| `bing images` | dal 2026-11-18 risponde con SERP non correlate (gatti, Mike Wazowski, incisori laser) anche a richiesta diretta: upstream degradato, non ranking |
| `artic` | opere d'arte, mai identità |
| `pinterest` | mood board e repost, prima fonte di falsi positivi |
| `openverse` | aggregatore di stock generico |
| `deviantart` | artwork |
| `pexels`, `unsplash`, `flickr` | stock/foto generiche: "una donna", non "Chiara Poggi" |
| `devicons`, `lucide` | set di icone |
| `wikicommons.images` | restituisce pagine `File:` (HTML), non byte immagine; gli asset Commons arrivano dal provider dedicato |

Restano attivi tre indici generali, tutti con risultati pertinenti:
`duckduckgo images`, `google cse images`, `brave.images`.

Il file dichiara anche `general.contact_url`: da SearXNG 2026.11 è obbligatorio
per registrare gli engine `wikipedia`, `wikidata` e `openstreetmap`.

Oltre agli engine, `internal/capabilities/images/search_engine_searxng.go`
applica una deny-list di host (stock, wallpaper, repost, AI-slop) prima di
consumare il limite per query. La lista è in
`searxImageDeniedSourceDomains` ed è coperta da
`TestSearxImageSourceDeniedMatchesHostAndSubdomains` e
`TestSearXNGImageSearchDropsDeniedSourcesWithoutSpendingTheBudget`.

## Validazione

```sh
cd refactored
docker compose pull searxng
docker compose up -d --no-deps searxng

# L'API JSON deve rispondere e restare l'unica superficie usata dall'app.
curl -s http://127.0.0.1:8080/config | python3 -c \
  'import sys,json; d=json.load(sys.stdin); print(d["version"]); print([e["name"] for e in d["engines"] if e["enabled"] and "images" in e["categories"]])'
```

Atteso: versione ≥ `2026.11.18` e i soli tre engine
`duckduckgo images`, `google cse images`, `brave.images`.

Misura attraverso il codice reale (13 query PERSON, prime 6 posizioni):

```sh
SEARXNG_LIVE_POLICY=1 SEARXNG_URL=http://127.0.0.1:8080 \
  go test ./internal/capabilities/images \
  -run '^TestSearXNGPersonPolicyLive$' -v -count=1 -timeout 6m
```

Il test fallisce se un host della deny-list arriva al chiamante.

Dump dei candidati con download e verifica tecnica:

```sh
PERSON_IMAGE_VISUAL_DUMP=1 \
PERSON_IMAGE_VISUAL_DUMP_SOURCE=searxng \
SEARXNG_URL=http://127.0.0.1:8080 \
PERSON_IMAGE_VISUAL_DUMP_OUT="$PWD/tmp/person-image-searxng-policy" \
  go test ./internal/capabilities/images -run TestPersonImageVisualDump -v -count=1 -timeout 15m
```

## Misure del 2026-11-18

| | Default upstream | Curation, SearXNG 2026.6.4 | Fix, 2026.11.18 |
|---|---:|---:|---:|
| Engine immagini attivi | 15 | 5 | 3 |
| Risultati top-6 (13 query) | 78 | 62 | 78 |
| Host di rumore | 47 (60%) | 0 | 0 |
| Entità senza candidati | 0 | 1 | 0 |
| Immagini corrette (revisione visiva) | non rilevato | 20/42 (47,6%) | campione 10/12, entità prima a zero ora corrette |
| Tempo della misura provider | ~75 s | 2,5 s | 25 s |

Dopo il fix il dump salva 51 candidati su 78 (65%): i rifiuti sono
`vidrush image is not a valid supported image` (formati non decodificabili,
AVIF/HTML) e un limite di download, non giudizi di identità.

### Revisione visiva dopo il fix

Campione di 12 immagini su 12 entità, aperte una per una:

| Entità | Esito |
|---|---|
| Mike Tyson | copertina *Sports Illustrated* del 24 giugno 1991, corretto |
| Conan O'Brien | ritratto, corretto |
| Michael B. Jordan | ritratto, corretto |
| Steve Jobs | cerimonia Stanford 2005, corretto |
| Chiara Poggi | foto personale, corretta |
| Alberto Stasi | foto, corretta |
| José María Aznar | ritratto, corretto |
| Hayao Miyazaki | ritratto in studio, corretto |
| Michael Burry | ritratto, corretto |
| Elon Musk | ritratto, corretto |
| Bruce Lee | statua da collezione Blitzway: somiglianza corretta, non utilizzabile come foto editoriale |
| Gianluca Ferretti | volto generico non riconducibile all'entità: non verificabile |

Conclusione: il fix risolve i falsi positivi per omonimia (prima Conan,
Michael B. Jordan e Steve Jobs erano al 100% sbagliati). Restano due casi che
solo il verdetto può gestire: la statua (identità giusta, supporto sbagliato) e
il nome non pubblico.

## Cronologia della diagnosi

1. Dopo la curation, 22/42 immagini erano la persona sbagliata: Mike Wazowski
   per Tyson, *Detective Conan* per Conan, Michael Jackson per Michael B.
   Jordan, skin di Minecraft per Jobs, play money per Poggi.
2. Probe diretto su `bing.com/images/async` con `q=Mike Tyson 1991`: immagini
   di **gatti** in una richiesta e **incisori laser** in un'altra. `sid` e
   `cturl` vuoti nel payload: mitigazione anti-bot / sessione non valida.
3. Verifica del percorso di produzione: DuckDuckGo **diretto** (non via
   SearXNG) restituisce 6/6 URL corretti per tutte le 13 entità. La produzione
   non era affetta; lo era solo il fallback.
4. A/B con `searxng/searxng:latest` (2026.11.18) su porta 8099: `duckduckgo
   images`, `brave.images` e il nuovo `google cse images` funzionano; Bing resta
   l'unico rotto.
5. Fix: aggiornamento immagine + `bing images` disabilitato + `contact_url`.
   Verificato in produzione con `TestSearXNGPersonPolicyLive` (78 candidati, 0
   entità vuote, 0 host negati) e con la revisione visiva sopra.

## Limiti noti

- **Tre engine, non più.** Se un giorno anche `duckduckgo`, `google cse` o
  `brave` vengono bloccati, la categoria immagini resta senza fonti. Il test
  live asserisce l'assenza di host negati, non un minimo per entità.
- **Alcuni engine rispondono in ritardo.** `brave.images` e `duckduckgo images`
  compaiono in `unresponsive_engines` con `timeout` pur restituendo risultati:
  il provider Go ha 5 s di budget e va rimisurato se la latenza cresce.
- **La deny-list è globale.** Vale anche per ORG/BRAND/LOCATION: per quelle
  classi il percorso editoriale interroga Commons prima di SearXNG, quindi la
  rimozione dello stock non toglie l'asset licenziato.
- **SearXNG non è nel percorso PERSON breve.** La rotta PERSON usa Wikipedia
  (pageimage) e DuckDuckGo diretti; SearXNG entra solo come fallback.
- **La policy non decide l'identità.** Un host pulito non prova che il pixel
  mostri la persona: la verifica resta affidata a
  `IsUsableCandidateQuality` + verdetto VLM. La statua di Bruce Lee è
  l'esempio: identità giusta, supporto sbagliato.
