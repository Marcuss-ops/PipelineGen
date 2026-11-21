# RA-18 — Sei funzioni oltre la soglia di complessità

**Data:** 2026-11-21
**Priorità:** P1 (manutenibilità su percorso caldo)
**Stato:** PARTIAL — 4 di 6 sotto soglia (§8); restano `materialize` 107 e `semanticBundles` 92
**Owner:** per funzione (kernel/script, adapters, scripts, overlays)
**Origine:** report esterno 2026-11-21 §2.1
**Verifica sul codice:** **DA MISURARE** — i valori di branch del report (143/120/114/111/101/100)
non sono stati riprodotti; le funzioni esistono ai file:riga indicati

## 1. Problema

| # | Funzione | File:riga | Complessità dichiarata | LOC |
| ---: | --- | --- | ---: | ---: |
| 1 | `(*OverlayStyleSpec).Validate` | `internal/kernel/script/overlay_style_spec.go:256` | 143 branch | 145 |
| 2 | `(*vidRushMaterializationRun).materialize` | `internal/capabilities/scripts/adapters/processor_vidrush_materialize_run.go:30` | 120 branch | 328 |
| 3 | `(*MediaResolverImageStage).processInternetImageSegments` | `internal/capabilities/scripts/adapters/media_resolver_image_segments.go:19` | 114 branch | 419 |
| 4 | `(*QueueRenderEnqueuer).enqueueChrononPlan` | `internal/capabilities/scripts/render_queue_submit.go:27` | 111 branch | 440 |
| 5 | `buildFinalJobPayloads` | `internal/capabilities/scripts/final_job_payload.go:219` | 101 branch | 386 |
| 6 | `(*OverlayPlan).Validate` | `internal/capabilities/overlays/model/model.go:352` | 100 branch | 194 |

La soglia di progetto è **ciclo ≤40** (usata nella sessione 2026-11-06 in
`docs/stato/refactored.md`); nessuna di queste è vicina. Il difetto non è la lunghezza: è che
ogni funzione mescola **validazione**, **policy** e **orchestrazione di I/O** (nel caso 2
ownership check + hydration + forceRefresh + cap per provider + L1/L2 cache + budget + retry +
metrics + persistenza catalogo in un solo `for`).

## 2. Primo passo: misurare

```bash
cd refactored
go install github.com/fzipp/gocyclo/cmd/gocyclo@latest
export PATH="$(go env GOPATH)/bin:$PATH"
gocyclo -top 25 -avg ./internal | head -30
```

Incollare l'output nella scheda. I numeri del report diventano la baseline solo dopo questa
misura; la soglia di chiusura è **≤40** per le sei funzioni e **nessuna funzione >40 nella
top 25** lasciata senza scheda.

## 3. Soluzione

### 3.1 Validation (casi 1 e 6) — helper tabellari, messaggi identici

1. **Congelare i messaggi**: prima di toccare il codice, un test tabellare che chiama
   `Validate` su input invalidi e cattura **stringa esatta** dell'errore. Il messaggio è
   contratto osservabile (va nei log e nei report di certificazione): non deve cambiare.
2. Estrarre i primitivi:
   ```go
   func validateFiniteRange(field string, v *float64, min, max float64) error
   func validateHexColor(field, v string) error      // riusa kernel/color (RA-17)
   func validatePositiveInt(field string, v *int) error
   ```
3. Raggruppare per blocco dello spec (`shadow`, `color`, `size`, `bullet`, `cursor`, `image`,
   `card_background`) in metodi `validateShadow()`, `validateImage()`, … e chiamarli in
   sequenza. Ogni metodo ha ≤40 cyclo.
4. `OverlayPlan.Validate`: un validator per tipo di item (`map[itemType]func(item) error`) +
   validazione di timeline/canvas separata. I 100 branch diventano una tabella.

### 3.2 Orchestrazione con I/O (casi 2, 3, 4, 5) — stadi nominati

Per ciascuna, estrarre **stadi** con un nome che dice cosa fanno, ognuno con input/output
espliciti, senza cambiare l'ordine:

- `materialize`: `resolveOwnership` → `hydrateCandidates` → `applyForceRefresh` →
  `applyProviderCaps` → `lookupCaches` (L1/L2) → `acquireWithinBudget` (con retry isolato) →
  `recordMetrics` → `persistCatalog`. Il `for` per provider resta nel chiamante e chiama
  `materializeOneProvider`.
- `processInternetImageSegments`: `planSegmentQueries` → `resolveSegmentCandidates` (per
  segmento) → `scoreAndFilter` → `emitOutcome`. La parte cache è condivisa con RA-19 (L1/L2
  drift): se si tocca, farlo **dopo** l'estrazione, non insieme.
- `enqueueChrononPlan`: `validatePlanForEnqueue` → `mapPlanToSubmitRequest` →
  `submitWithRetry` → `recordReceipt`.
- `buildFinalJobPayloads`: allineato a RA-16 — `buildCompiledPlanPayload` →
  `buildRuntimeAssetsPayload` → `buildOverlaysPayload` → `buildFinalizePayload`. Se RA-16 è
  già passato, questa funzione è quasi vuota.

Regola: nessuno stadio introduce nuove goroutine, nuovi retry o nuove chiamate di rete. È un
refactor di forma, non di comportamento.

### 3.3 Guard

- Aggiungere `gocyclo -over 40 ./internal` al check locale (make target o script CI) con una
  allowlist esplicita **vuota** per i sei file a fine ticket. Se il repo non vuole un nuovo
  target, usare un test che invoca `gocyclo` se presente e skippa altrimenti — ma il ticket
  deve lasciare un modo ripetibile di misurare.

## 4. Check finali

```bash
cd refactored
# 1. le sei funzioni sono sotto soglia
gocyclo -over 40 ./internal \
  | grep -E 'overlay_style_spec.go|processor_vidrush_materialize_run.go|media_resolver_image_segments.go|render_queue_submit.go|final_job_payload.go|model/model.go'
# atteso: nessun output

# 2. nessuna nuova funzione sopra soglia
gocyclo -top 25 ./internal | awk '$1>40' | wc -l
# atteso: 0 (o solo funzioni con scheda aperta e owner)

# 3. i messaggi di validazione sono invariati
go test ./internal/kernel/script/ ./internal/capabilities/overlays/ -run 'Validate|ErrorMessage' -v
# atteso: PASS con il test di congelamento pre-refactor

# 4. comportamento invariato sui percorsi con I/O
go test ./internal/capabilities/scripts/adapters/ -run 'Materialize|InternetImage'
go test ./internal/capabilities/scripts/ -run 'RenderQueue|ChrononPlan|FinalJobPayload'
# atteso: PASS

# 5. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 5. Rischi e rollback

- **Rischio**: estrarre stadi cambia l'ordine degli effetti (es. metriche prima della cache).
  Mitigazione: mappare l'ordine attuale riga per riga e pinnarlo con test di sequenza (fake
  che registra le chiamate).
- **Rischio**: i test esistenti coprono solo il caso felice. Mitigazione: catturare i messaggi
  di errore **prima** del refactor (punto 3.1.1) e i rami di cache/retry con fake.
- **Rollback**: per funzione; le sei sono indipendenti.

## 7. Misura disponibile al 2026-11-21

`gocyclo` installato con `go install github.com/fzipp/gocyclo/cmd/gocyclo@latest`
(`gocyclo -top 25 -avg ./internal`, media 4.51). La baseline reale è più alta del report:

| # | Funzione | File:riga | gocyclo misurato | Report |
| ---: | --- | --- | ---: | ---: |
| 1 | `(*OverlayStyleSpec).Validate` | `internal/kernel/script/overlay_style_spec.go:302` | **183** | 143 |
| 2 | `(*vidRushMaterializationRun).materialize` | `internal/capabilities/scripts/adapters/processor_vidrush_materialize_run.go:30` | **107** | 120 |
| 3 | `(*QueueRenderEnqueuer).enqueueChrononPlan` | `internal/capabilities/scripts/render_queue_submit.go:27` | **99** | 111 |
| 4 | `(*OverlayPlan).Validate` | `internal/capabilities/overlays/model/model.go:353` | **97** | 100 |
| 5 | `(*MediaResolverImageStage).processInternetImageSegments` | `internal/capabilities/scripts/adapters/media_resolver_image_segments.go:20` | **93** | 114 |
| 6 | `buildFinalJobPayloads` | `internal/capabilities/scripts/final_job_payload.go:219` | **92** | 101 |

Nessuna è vicina alla soglia ≤40; il taglio va fatto una funzione per commit, con il test di
congelamento dei messaggi prima del refactor (§3.1.1). La soglia di chiusura resta ≤40.

## 8. Avanzamento al 2026-11-21 (4 di 6)

Due funzioni sono scese sotto soglia con estrazione di helper, mantenendo **ordine dei controlli
e messaggi identici** (nessuna stringa di errore riscritta, nessuna condizione spostata):

| # | Funzione | Prima | Dopo | Helper introdotti |
| ---: | --- | ---: | ---: | --- |
| 1 | `(*OverlayStyleSpec).Validate` | 183 | **25** | `validate` su bullet/card_background/cursor/image; `validateFrame`, `validateShadow`, `validateGlow`, `validateBackdrop`, `validateStroke`, `validateMotions` |
| 4 | `(*OverlayPlan).Validate` | 97 | **≤31** | `validateHeader`, `validateBackground`, `validateItems`, `validateItem`, `validateAssetRefs`, `validateSoundEffect`, `validateImageLayers` |
| 3 | `(*QueueRenderEnqueuer).enqueueChrononPlan` | 99 | **37** | `foldDynamicMapBackground` (16, piano semantico + import spec + cleanup), `stageChrononPlanAssets` (25, asset manifest e font), `publishChrononArtifacts` (27, certificazione Objectstore/Drive) |
| 5 | `(*MediaResolverImageStage).processInternetImageSegments` | 93 | **39** | `planSegmentInternetImageDiscovery` (7, outcome tipizzato), `resolveInternetImageQuery` (40, ex corpo del fan-out), `finalizeInternetImageQuery` (13, scoring/verdict/filtri), `internetImageQueryResult` tipizzato a package level |

Verifiche: `go build ./...` exit 0; `go test ./internal/kernel/script/...` e
`./internal/capabilities/overlays/...` PASS; `gofmt -l` pulito sui due file.
Nessuna funzione >40 introdotta: nel package `kernel/script` il massimo è ora 38
(`validateImageGrid`), in `overlays/model` 77 (`(*MapOverlay).validateCameraMove`, fuori dalle
sei e da schedare).

`processInternetImageSegments` (93→39) è stato diviso in tre tagli che **non cambiano l'ordine
dei fatti**: la decisione per segmento (pin / bypass / reuse / search) è un valore di enum, il
corpo della goroutine di fan-out è il metodo `resolveInternetImageQuery` invariato, e la
post-elaborazione per query (identità, verdetti visivi, scoring, filtri, warning) è
`finalizeInternetImageQuery`. Verifiche: `go build ./...` ok, `go test
./internal/capabilities/scripts/adapters/` e `./internal/capabilities/scripts/` (16.9s) PASS,
`go vet` pulito, `gofmt -l` vuoto.

`enqueueChrononPlan` (99→37) è stato diviso in quattro tagli lungo i confini che il commento
stesso dichiarava: fold della mappa dinamica (con `cleanup` restituito, così il `RemoveAll`
continua a girare alla fine della funzione), manifesto asset, e contratto di pubblicazione
post-render (Objectstore + Drive), lasciando in `enqueueChrononPlan` la cache e la corsa
submit/wait. Verifiche: `go build ./...` ok, `go test ./internal/capabilities/scripts/` (16.1s)
PASS, `go vet` pulito, `gofmt -l` vuoto.

**Restano 2**: `materialize` 107, `semanticBundles` 92. Per `semanticBundles` è stato tentato lo split in
`projectReferences`/`projectTextBlocks`: l'estrazione automatica ha prodotto un albero non
compilabile ed è stata annullata con `git checkout HEAD --` sul file (nessun residuo). Va rifatta
a mano, spezzando anche `semanticBundles` in richiesta/manifest/bundle, perché la sola
estrazione del loop lascia due helper a ~45.
