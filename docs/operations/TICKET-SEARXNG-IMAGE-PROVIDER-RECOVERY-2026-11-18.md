# TICKET — SearXNG image provider recovery: Bing degraded, engine upgrade (2026-11-18)

- **Stato:** RESOLVED (2026-11-18) — un solo criterio resta condizionato
  dall'ambiente: il gate E2E, vedi §6.
- **Priorità:** P1
- **Owner:** image retrieval platform (`internal/capabilities/images`,
  `searxng/settings.yml`)
- **Origine:** revisione visiva del dump PERSON del 2026-11-18 (42 immagini, 13
  entità) e successivo probe diretto sull'upstream.
- **Documenti correlati:**
  - `docs/operations/searxng-image-policy.md` (runbook: mount, policy, misure)
  - `docs/qa/PERSON-ENTITY-MAX-TESTS.md` (matrice PERSON e revisione visiva)
  - `TICKET-PERSON-IMAGE-IDENTITY-2026-11-18.md` (P3 chiude qui; P2 declinato)
  - `entity-image-pipeline-flow.md` (contratto hit → verified → bound)
- **Regola:** nessuna asserzione indebolita per far passare il ticket; nessun
  provider nuovo nel fan-out senza una misura.

## 1. Sintomo

Dopo la curation degli engine del 2026-11-18 la categoria immagini di SearXNG
non restituiva più host di stock (0/62 negati), ma la revisione visiva delle 42
immagini scaricate ha trovato **22 falsi positivi**:

| Entità | Cosa restituiva l'immagine |
|---|---|
| Mike Tyson | Mike Wazowski, *Mike the Knight*, poster della serie *MIKE* |
| Conan O'Brien | poster di *Detective Conan* |
| Michael B. Jordan | Michael Jackson |
| Steve Jobs | skin di Minecraft "Steve" |
| Chiara Poggi | scatola di play money |
| Alberto Stasi | poster di *The Miseducation of Cameron Post* |
| José María Aznar | bottiglie di liquore cinese |

Le entità Conan, Michael B. Jordan, Poggi, Stasi e Aznar erano al 100%
sbagliate. La produzione PERSON non era affetta perché usa Wikipedia e
DuckDuckGo diretti; il difetto era confinato al fallback SearXNG.

## 2. Diagnosi

1. **Non è la query, non è il parser.** Richiesta diretta a
   `bing.com/images/async?q=Mike Tyson 1991`: una volta **immagini di gatti**,
   una volta **incisori laser**. Nel payload `sid` e `cturl` sono vuoti:
   mitigazione anti-bot / sessione non valida. Bing non risponde "vietato",
   risponde con una SERP generica.
2. **SearXNG non riceve il blocco**: il suo engine Bing restituisce il misto di
   omonimi ("Mike", "Conan", "Michael", "Steve") che sembrava un problema di
   ranking.
3. **Tutti gli altri engine immagine erano morti** sulla 2026.6.4:
   `duckduckgo images` parsing error, `google images` access denied,
   `startpage images` CAPTCHA, `brave.images` suspended. SearXNG immagini =
   un solo upstream, rotto.
4. **Il percorso di produzione è sano**: DuckDuckGo diretto restituisce 6/6 URL
   per tutte le 13 entità; campione verificato a vista (Tyson 1991, Conan,
   Poggi) corretto.
5. **A/B su istanza separata (porta 8099)** con `searxng/searxng:latest`
   (2026.11.18): `duckduckgo images`, `brave.images` e il nuovo
   `google cse images` restituiscono risultati pertinenti; Bing resta l'unico
   rotto.

## 3. Fix applicato

1. **Upgrade immagine** `searxng/searxng` da `2026.6.4` a `2026.11.18`
   (`docker compose pull searxng && docker compose up -d --no-deps searxng`).
2. **`bing images` disabilitato** in `searxng/settings.yml`, con il motivo e la
   data nel file.
3. **`general.contact_url`** aggiunto: da 2026.11 è obbligatorio per registrare
   `wikipedia`, `wikidata` e `openstreetmap`.
4. **Deny-list host** del provider SearXNG mantenuta
   (`search_engine_searxng.go`, `searxImageDeniedSourceDomains`): resta valida
   contro stock/wallpaper, ma non è la difesa contro gli omonimi.
5. **Documentazione**: runbook riscritto con diagnosi, misure e rollback;
   matrice PERSON aggiornata.

## 4. Verifica

| Controllo | Comando | Esito |
|---|---|---|
| Versione e engine | `/config` su `127.0.0.1:8080` | `2026.11.18+b867c56a5`; solo `duckduckgo images`, `google cse images`, `brave.images` |
| Policy live (13 query, top-6) | `SEARXNG_LIVE_POLICY=1 go test ./internal/capabilities/images -run '^TestSearXNGPersonPolicyLive$'` | 78 candidati, 0 entità vuote, 0 host negati, 25 s |
| Dump candidati | `PERSON_IMAGE_VISUAL_DUMP=1 … SOURCE=searxng` | 78 candidati, 51 scaricati (i rifiuti sono AVIF/HTML, non identità) |
| Revisione visiva | 12 immagini, 12 entità | 10 corrette (Tyson 1991, Conan, MBJ, Jobs Stanford, Poggi, Stasi, Aznar, Miyazaki, Burry, Musk); 1 statua da collezione (Bruce Lee); 1 volto non verificabile (Ferretti) |
| Test package | `go test ./internal/capabilities/images ./internal/app/wiring/script` | verdi |
| Formattazione | `gofmt -l`, `git diff --check` | puliti sui file toccati |

Prima/dopo sulla stessa metrica: host di rumore **47/78 → 0/78**; entità senza
candidati **1 → 0**; falsi positivi per omonimia sulle tre entità prima al 100%
**risolti**.

## 5. Cosa non è stato fatto

- **Nessun commit**: il repository ha lavoro concorrente non committato e il
  ticket non lo tocca.
- **Nessun nuovo provider nel fan-out**: il fix usa engine già presenti
  nell'immagine SearXNG.
- **Nessuna modifica alla query PERSON**: il probe diretto ha escluso che il
  problema fosse lessicale.

## 6. Gate E2E: stato e prova di non-regressione

`TestLiveImageRetrievalJobPersistsAndReusesAssets` è **rosso** al momento della
chiusura: esaurisce il budget di 150 s e fallisce su
`upsert entity: context deadline exceeded` (contesto già scaduto, non errore di
bind). Non è causato da questo fix:

1. Con **SearXNG irraggiungibile** (`SEARXNG_URL=http://127.0.0.1:9`) fallisce
   allo stesso modo, 150 s.
2. Con `PERSON_CATALOG_VERIFY_LIMIT=1` fallisce allo stesso modo: non è il
   numero di verifiche.
3. Una chiamata vision reale misurata sul dump costa **2,1 s**; una chat breve
   0,37 s.
4. La macchina era a **load 23–28** con `ffmpeg` ×3 al 100%, embedding server e
   altri `go test` concorrenti; lo stesso gate era passato a 89,8 s prima della
   saturazione.

Follow-up: rilanciare a carico basso. Se resta rosso, strumentare la fase di
acquisizione del resolver (download lenti) — non alzare il timeout del gate.

## 7. Criteri di accettazione

- [x] Bing non è più nel fan-out immagini; la configurazione è esplicita e
      riproducibile nel repo.
- [x] Le 13 query PERSON restituiscono 6 candidati ciascuna, 0 entità vuote,
      0 host negati.
- [x] Le entità prima al 100% sbagliate (Conan, Michael B. Jordan, Poggi, Stasi,
      Aznar) restituiscono la persona corretta nella revisione visiva.
- [x] La deny-list ha test dedicati e non consuma il budget per query.
- [x] Il runbook documenta mount, engine policy, misure, diagnosi e comandi di
      verifica.
- [ ] Gate E2E verde — condizionato dal carico dell'host, non dal fix (§6).

## 8. File toccati

- `searxng/settings.yml` — `bing images` disabilitato, `general.contact_url`,
  commenti aggiornati.
- `docs/operations/searxng-image-policy.md` — runbook riscritto (fix, misure,
  cronologia, limiti).
- `docs/qa/PERSON-ENTITY-MAX-TESTS.md` — stato P0/P1/P2/P3, revisione visiva,
  sezione post-fix.
- `internal/capabilities/images/search_engine_searxng.go` — deny-list host.
- `internal/capabilities/images/search_engine_searxng_filter_test.go` — test
  deny-list (host, sottodomini, budget).
- `internal/capabilities/images/searxng_person_policy_live_test.go` — misura
  opt-in delle 13 query.
- `internal/capabilities/images/person_image_quality_audit_test.go` — etichetta
  d'errore corretta per la sorgente SearXNG.
- `internal/app/wiring/script/internet_image_search_person_fallback_test.go` —
  fallback Wikipedia → DuckDuckGo.

## 9. Riproduzione

```sh
cd refactored
docker compose pull searxng
docker compose up -d --no-deps searxng

curl -s http://127.0.0.1:8080/config | python3 -c \
  'import sys,json; d=json.load(sys.stdin); print(d["version"]); print([e["name"] for e in d["engines"] if e["enabled"] and "images" in e["categories"]])'

SEARXNG_LIVE_POLICY=1 SEARXNG_URL=http://127.0.0.1:8080 \
  go test ./internal/capabilities/images \
  -run '^TestSearXNGPersonPolicyLive$' -v -count=1 -timeout 6m
```

## 10. Rischi residui

- **Tre engine soli**: se DDG, Google CSE e Brave bloccano l'IP, la categoria
  immagini resta scoperta. Il test live verifica gli host negati, non un minimo
  per entità.
- **Timeout sporadici** di `brave.images` e `duckduckgo images`: arrivano
  risultati, ma il provider Go ha 5 s di budget complessivo.
- **Il gate VLM resta obbligatorio**: la statua di Bruce Lee prova che
  l'identità può essere giusta e il supporto sbagliato.
- **Nomi non pubblici** (es. Ferretti): nessun engine può verificare un volto
  senza riferimento; l'esito corretto è "nessuna immagine".

## Prossimi passi (2026-11-19)

1. **Chiudere l'unico criterio condizionato:** eseguire il gate live §6 con
   `SEARXNG_LIVE_POLICY=1 SEARXNG_URL=http://127.0.0.1:8080` e
   `TestSearXNGPersonPolicyLive`; registrare qui esito, data e versione
   dell'engine. Senza questo comando il ticket resta `RESOLVED` solo in locale.
2. **Se l'ambiente non è disponibile:** scrivere esplicitamente che il gate
   non è eseguibile qui e perché; non convertirlo in PASS.
3. **Dopo il gate:** dichiarare la riga di stato come `CLOSED` (la parola
   `RESOLVED` non è riconosciuta dall'indice e la scheda resta conteggiata
   aperta) e rigenerare l'indice.
4. **Rischio da tenere d'occhio nelle run entità:** tre engine soli e budget di
   5 s; una run con molte entità è la prova pratica che la copertura tiene.
