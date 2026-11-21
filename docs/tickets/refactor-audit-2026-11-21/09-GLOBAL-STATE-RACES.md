# RA-09 — Stato globale mutabile non sincronizzato (race e test non parallelizzabili)

**Data:** 2026-11-21
**Priorità:** P1 (data race latente; blocca `t.Parallel()` nei test)
**Stato:** DONE — font, logSink, registry Ollama e hook/cache VidRush (§1.4) chiusi; vedi §8
**Owner:** `platform/renderinggen`, `platform/httpserver/middleware`, `platform/ollama/prompts`,
`capabilities/scripts/adapters`
**Origine:** report esterno 2026-11-21 §2.2; verificato sul codice
**Verifica sul codice:** **CONFERMATO** per `font_assets.go`, `middleware_logger.go`;
**DA CONFERMARE** per `ollama/prompts/config.go` e per gli hook/cache VidRush

## 1. Problema

Quattro forme dello stesso difetto: stato di package scritto senza sincronizzazione, o
ripristinato in modo non atomico mentre altri leggono.

### 1.1 `internal/platform/renderinggen/font_assets.go:111,122,200`

```go
var (
    assetRootOnce sync.Once
    assetRootPath string
)
…
assetRootOnce.Do(func() { assetRootPath = discoverAssetRoot() })
…
func resetFontAssetCache() { assetRootOnce = sync.Once{} }   // riga 200
```

`resetFontAssetCache` azzera il `sync.Once` mentre `AssetRoot()` può essere in `Do` o subito
dopo: `sync.Once` non è pensato per il reset concorrente. Race su `assetRootOnce` e su
`assetRootPath`; nei test il reset è l'unico modo per cambiare root, quindi il problema è
strutturale, non teorico.

### 1.2 `internal/platform/httpserver/middleware/middleware_logger.go:33`

```go
var logSink requestlog.RequestLogSink
```

Letto da `GetDroppedLogs`/middleware e scritto dal wiring all'avvio, senza lock né
`atomic.Pointer`. Se il wiring avviene dopo l'avvio del server (o in un test che lo cambia),
è race.

### 1.3 `internal/platform/ollama/prompts/config.go:110`

`var registry *Config` non protetto: stessa classe, lettura/scrittura concorrente.

### 1.4 Hook e cache globali VidRush

> **Nota 2026-11-21:** il censimento originale era parzialmente stale. I tre simboli citati
> non vivono più in `vidrush_helpers.go`: `defaultNowFn` è in `assets/monitor`,
> `youTubeCooldownNow` in `platform/downloader`, `defaultLocalizedIdentityMatcher` in
> `capabilities/scripts`. Le tre cache VidRush sono già `cacheutil.LRU` shardate con mutex
> proprio e contatori atomici (RA-22), quindi non erano il problema. I tre seam di clock e il
> matcher restavano `var` nudi, scritti dai test e letti dai worker: è quella la race.

`internal/capabilities/scripts/adapters/vidrush_helpers.go`: `vidrushImageCache`,
`vidrushBindingCache`, `vidrushMaterializedCache` (LRU limitate, thread-safe). I seam
mutabili sono `defaultNowFn` (`assets/monitor`), `youTubeCooldownNow`
(`platform/downloader`) e `defaultLocalizedIdentityMatcher` (`capabilities/scripts`).

## 2. Soluzione

1. **`font_assets.go`**: eliminare il reset del `sync.Once`. Due opzioni, in ordine di
   preferenza:
   - **(a)** stato immutabile per processo: `AssetRoot()` calcola una volta e nessun codice di
     produzione resetta; il test usa un seam esplicito (`SetAssetRootForTest(t, path)` con
     `t.Cleanup`, protetto da mutex, disponibile solo nei test tramite file `_test.go` nello
     stesso package).
   - **(b)** `atomic.Pointer[assetRootState]` con `sync.Mutex` per il calcolo: i lettori fanno
     `Load`, il reset pubblica un nuovo puntatore. Niente `sync.Once` azzerato.
2. **`logSink`**: `atomic.Pointer[requestLogSinkHolder]` (o `atomic.Value`) con setter
   `SetLogSink` e getter che ritorna `nil` se non impostato. Il wiring resta l'unico scrittore,
   ma la scrittura è ora atomica e visibile.
3. **`ollama/prompts` registry**: stesso trattamento (`atomic.Pointer[Config]`), oppure
   passare il registry al costruttore e rimuovere il globale. Se il package è usato in un solo
   punto del wiring, la seconda è preferibile.
4. **VidRush**: convertire gli hook in campi di una struct `deps` iniettata
   (`imageDeps{now func() time.Time, cache *imageCache, …}`) costruita nel composition root.
   Le cache diventano un tipo con mutex proprio e `cap`/TTL (vedi RA-22). Nei test si costruisce
   una `deps` per test: `t.Parallel()` torna possibile.
5. **Regola**: nessun `var` di package mutabile in `internal/platform/**` e
   `internal/capabilities/**` senza (i) un tipo con lock/atomic, oppure (ii) immutabilità dopo
   l'init. Aggiungere il controllo dei casi noti a un test statico o al percheck, con
   allowlist esplicita.
6. **Test `-race`** sui package toccati, con almeno un test che usa `t.Parallel()` e legge il
   globale mentre un altro lo resetta/imposta.

## 3. Check finali

```bash
cd refactored
# 1. race detector sui package toccati
go test -race ./internal/platform/renderinggen/ ./internal/platform/httpserver/middleware/ \
  ./internal/platform/ollama/prompts/ ./internal/capabilities/scripts/adapters/
# atteso: PASS, nessun "DATA RACE"

# 2. il reset concorrente non esiste più
grep -rn "sync.Once{}" internal/platform/renderinggen/
# atteso: nessun output

# 3. nessun globale mutabile nei siti elencati
grep -rn "^var logSink\|^var registry \*Config" internal/platform/
# atteso: nessun output (sostituiti da atomic.Pointer o campi)

# 4. i test possono girare in parallelo
go test -race -parallel 8 ./internal/capabilities/scripts/adapters/ -run 'VidRush'
# atteso: PASS; i test che mutavano i globali ora usano una deps per test

# 5. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: cambiare `AssetRoot` in un seam di test può far divergere il percorso reale da
  quello testato. Mitigazione: il seam chiama la stessa `discoverAssetRoot()`.
- **Rischio**: iniettare le deps VidRush tocca molti call site. Mitigazione: introdurre la
  struct con un default costruito dai globali attuali, poi migrare i chiamanti uno alla volta,
  e rimuovere i globali solo alla fine.
- **Rollback**: revert per punto.

## 6. Esito della verifica finale (2026-11-21)

- `renderinggen/font_assets.go`: `sync.Once` sostituito da `assetRootMu` + `assetRootSet`;
  `resetFontAssetCache` è ora una sezione critica normale (nessun azzeramento concorrente).
- `httpserver/middleware/middleware_logger.go`: `logSink atomic.Pointer[logSinkHolder]`, setter e
  getter atomici; tutti i lettori passano da `currentLogSink()`.
- `go test -race ./internal/platform/renderinggen/ ./internal/platform/httpserver/middleware/` → ok.
- **Aperti**: `platform/ollama/prompts/config.go` registry e le cache/hook globali VidRush
  (`vidrush_helpers.go`). La seconda richiede l'iniezione delle deps in un tipo, che tocca molti
  call site: scheda separata.

## 7. Esito della verifica finale (2026-11-21, registry Ollama)

Il registry di `platform/ollama/prompts` non era un globale scritto senza lock: non aveva
**nessun writer**. `prompts.Load` è stato ritirato in uno sweep precedente; `registry` era
rimasto dichiarato e sempre `nil`, quindi le cinque letture (`prompts.Get() != nil`) erano
catene morte che fallivano sempre verso il prompt hardcoded. Renderlo `atomic.Pointer` avrebbe
protetto un valore che nessuno scrive.

Decisione: rimuovere la catena vestigiale, non sincronizzarla.

- eliminati `config.go` (`Config`, `MemorySection`, `registry`, `Get`, `render`),
  `render_media.go`, `render_script.go`;
- rimossi i rami `prompts.Get()` in `generate.go`, `generate_metadata.go`,
  `generate_translation.go`, `generate_translation_batch.go` e `ai/classifier/classifier.go`,
  e i tre in `prompts/builders_chat.go` più quello in `prompts/system_prompt.go`: il fallback
  hardcoded era già l'unico percorso eseguito;
- nessun test copriva `Config`/`Render*`, quindi la copertura non cambia.

Verifiche: `go test ./internal/platform/ollama/... ./internal/platform/ai/classifier/ -count=1`
PASS; `deadcode -test=false ./...` scende da 279 a 229 (la catena non è più conteggiata come
raggiungibile); `go build ./...`, `go vet ./...`, `archcheck --strict` exit 0.

**Ancora aperto:** §1.4 (hook e cache VidRush). Richiede la struct `deps` iniettata e la
migrazione dei call-site; il ticket stesso la indica come migrazione incrementale, non come fix
puntuale. Resta PARTIAL per questo punto.

## 8. Esito §1.4 — hook e cache VidRush (2026-11-21)

Nessuno dei tre seam è più un `var` nudo scritto in gara con i lettori:

- **`assets/monitor`** — `defaultNowFn` sostituito da `defaultNowClock
  atomic.Pointer[func() time.Time]`; `DateAfterFromCursor` legge via
  `resolvedDefaultNow()` e `SetDefaultNowForTests(nil)` pubblica il default di produzione.
  Il setter è passato dal file di test al package, così il commento e il codice coincidono.
- **`platform/downloader`** — `youTubeCooldownNow` sostituito da `youTubeCooldownClock
  atomic.Pointer[func() time.Time]`; `nowForYouTubeCooldown` legge il pointer e i test usano
  `setYouTubeCooldownNowForTest(nil|fn)` invece di assegnare il globale.
- **`capabilities/scripts`** — `defaultLocalizedIdentityMatcher` è ora
  `atomic.Value` con wrapper `localizedMatcherHook`, perché `atomic.Value` non può contenere
  un'interfaccia nil: hook non impostato = `nil`, mai un typed nil. `TestMain` usa
  `setDefaultLocalizedIdentityMatcherForTests`.
- **Cache VidRush** — già LRU shardate con `sync.Mutex` per shard e contatori atomici: nessuna
  modifica necessaria, nessun reset globale.

Verifiche eseguite:

```
go test -race ./internal/platform/downloader/ ./internal/capabilities/assets/monitor/   # ok
go test -race -parallel 8 ./internal/capabilities/scripts/adapters/ -run VidRush       # ok
go test ./internal/capabilities/scripts/ -count=1                                      # ok
go build ./... && go vet <package toccati> && gofmt -l                                 # puliti
grep -rn "sync.Once{}" internal/platform/renderinggen/                                 # 0
grep -rnE "^var (logSink|registry \*Config|defaultNowFn|youTubeCooldownNow|defaultLocalizedIdentityMatcher)" internal/
#   solo i due nuovi contenitori atomici (logSink atomic.Pointer, matcher atomic.Value)
```

**RA-09 DONE.**
