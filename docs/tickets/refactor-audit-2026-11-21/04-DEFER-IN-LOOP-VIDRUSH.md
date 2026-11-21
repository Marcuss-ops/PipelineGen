# RA-04 — `defer` dentro il loop: lock di discovery trattenuti (rischio deadlock)

**Data:** 2026-11-21
**Priorità:** P1 (code smell con rischio futuro; **non** un deadlock oggi)
**Stato:** **DONE (2026-11-21)**
**Owner:** scripts capability — adapters (VidRush image fan-out)
**Origine:** report esterno 2026-11-21 §2.2; verificato sul codice
**Verifica sul codice:** **RIDIMENSIONATO** — il `defer` nel loop esiste, ma il deadlock no

## 0. Correzione dell'analisi e fix applicato (2026-11-21)

L'analisi riga per riga ha mostrato che il report **non** aveva visto il rilascio esplicito a
fine iterazione:

```go
if releaseDiscovery != nil {
    releaseDiscovery()
    releaseDiscovery = nil
}   // ultima istruzione del corpo del for
```

Quindi: (a) il lock viene rilasciato a ogni iterazione, non a fine funzione; (b) due query
con la stessa chiave non possono andare in self-deadlock; (c) il `defer` era solo una rete di
sicurezza per i `return` anticipati, che scatta all'uscita della funzione. **Nessun bug
osservabile.**

Il difetto residuo è reale ma di forma: una closure `defer` registrata per ogni query, tenuta
viva fino al ritorno, con un invariante non dichiarato (chi rimuove il release esplicito
reintroduce il leak). Fix applicato, minimale e a comportamento identico:

- un **unico** `defer` registrato prima del loop, che rilascia l'handle eventualmente ancora
  aperto sui `return` anticipati;
- l'handle corrente è una variabile di funzione (`releaseDiscovery`), assegnata a ogni query e
  azzerata dal release esplicito di fine iterazione;
- commento che dichiara l'invariante "lock per query, non per funzione".

Verifica eseguita: `go build` + `go vet` sul package, `gofmt -l` pulito,
`go test ./internal/capabilities/scripts/adapters/ -run 'InternetImage|ImageCandidates|VidRush'`
→ **ok 1.640s**.

## 1. Problema (forma originale, storica)

Nel loop `for _, query := range imageQueries` il codice fa:

```go
results, catalogFallback, catalogRefreshRequired, fromCache, releaseDiscovery, resolutionErr :=
    f.resolveEntityImageCatalogAndCache(ctx, plan, query, entityCacheKey, catalogIdentity, catalogEligible)
if releaseDiscovery != nil {
    defer func() {
        if releaseDiscovery != nil {
            releaseDiscovery()
        }
    }()
}
```

`defer` in Go ha scope **di funzione**, non di blocco: il `releaseDiscovery` di ogni query
resta trattenuto fino al `return` della funzione intera. La variabile è catturata dal
closure: ogni `defer` esegue `releaseDiscovery()` sulla variabile **corrente**, ma poiché
`:=` crea una nuova variabile a ogni iterazione il closure cattura quella dell'iterazione —
il che rende il comportamento corretto solo per caso e fragile a ogni refactor (se la
dichiarazione passa a `=` o il closure legge un campo, i defer rilasciano tutti l'ultimo
handle).

Effetti:
1. Il lock/single-flight per chiave di discovery resta preso per tutte le query successive.
2. Se due query nella stessa lista mappano sulla stessa chiave, la seconda attende un lock
   che sarà rilasciato solo al ritorno della funzione → **self-deadlock** (o attesa fino al
   ctx).
3. La cache di discovery non può essere riusata da una goroutine concorrente durante il
   fan-out, annullando il beneficio del single-flight.

## 2. Soluzione

1. **Estrarre il corpo dell'iterazione in un metodo dedicato** con scope proprio:

   ```go
   func (f *… ) resolveInternetImageQuery(ctx context.Context, plan …, fanoutPlan …,
       workerInput …, query string, seen map[string]struct{}, candidates *[]scriptpkg.SegmentAssetCandidate,
       allCacheHits *bool) (outcome *vidRushProviderOutcome, err error)
   ```

   Dentro il metodo:
   ```go
   results, …, releaseDiscovery, err := f.resolveEntityImageCatalogAndCache(...)
   if releaseDiscovery != nil { defer releaseDiscovery() }
   ```
   Lo scope del `defer` è ora l'iterazione.
2. **Restituire l'esito invece di inviare nel canale** dal loop interno: il chiamante
   gestisce `outcomes <- …` e il `return` anticipato. Questo conserva la semantica attuale
   (una query in errore fa terminare la funzione) senza che il `defer` sopravviva.
3. **Regola di linter**: aggiungere al check locale (o a un test statico) la proibizione di
   `defer` dentro `for` nei package `adapters`. Se il repo ha già un linter custom in
   `cmd/archcheck`, aggiungere un percheck `percheck_defer_in_loop` con allowlist vuota per
   `internal/capabilities/scripts/adapters/*.go`; altrimenti un test con `go/ast` nel package.
   Il gate deve impedire la ricomparsa, non solo correggere l'istanza.
4. **Verificare gli altri siti** dello stesso pattern nel package:
   ```bash
   go vet ./internal/capabilities/scripts/adapters/  # non lo rileva: usare il check al punto 3
   grep -rn -B3 -A3 "defer func" internal/capabilities/scripts/adapters/*.go | grep -B3 "for "
   ```

## 3. Check finali

```bash
cd refactored
# 1. il pattern non è più presente nel file
awk '/for .*range/{f=1} f&&/defer /{print FILENAME": "NR": "$0} /^}/{f=0}' \
  internal/capabilities/scripts/adapters/vidrush_internet_image_candidates.go
# atteso: nessun output

# 2. il nuovo check statico esiste e passa
go test ./internal/capabilities/scripts/adapters/ -run 'DeferInLoop|Static'
# atteso: PASS (e il test deve fallire se si reintroduce un defer in un for)

# 3. nessuna regressione di comportamento
go test -race ./internal/capabilities/scripts/adapters/ -run 'InternetImage|ImageCandidates|VidRush'
# atteso: PASS; in particolare nessun "test timed out" (il deadlock si manifesta come timeout)

# 4. nuovo test di regressione: due query con la stessa chiave di discovery
#    completano entro il timeout (es. context.WithTimeout 2s) e producono un solo fetch.
#    Va aggiunto nel package; senza questo test il ticket non si chiude.

# 5. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: cambiare il punto in cui il risultato viene inviato su `outcomes` può alterare
  l'ordine dei candidati. Mitigazione: mantenere l'ordine di `imageQueries` e assemblare i
  risultati nel chiamante nell'ordine originale; test che pinna l'ordine.
- **Rischio**: `releaseDiscovery` può essere `nil` in un ramo e non in un altro; il metodo
  deve preservare esattamente quella condizione.
- **Rollback**: revert del commit; il bug torna ma non peggiora (nessun cambio di contratto).
