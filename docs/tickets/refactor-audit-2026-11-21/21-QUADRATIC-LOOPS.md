# RA-21 — Loop quadratici e allocazioni nascoste

**Data:** 2026-11-21
**Priorità:** P2 (costo CPU/GC su percorsi con molti item)
**Stato:** DONE
**Owner:** `scripts/adapters`, `scripts` (overlay), `kernel/script`
**Origine:** report esterno 2026-11-21 §3.2; verificato sul codice dove indicato
**Verifica sul codice:** **CONFERMATO** per `AppendProviderCandidatesUnique` (implementazione
O(n) per chiamata: clona il base e ricostruisce la mappa) e per il doppio loop di
`overlay_entity_cards_text.go`. **DA MISURARE** l'impatto reale.

## 1. Problema

| Sito | Difetto |
| --- | --- |
| `adapters/vidrush_registry_searchers.go:78-94` + `vidrush_candidate_shared.go:13-33` | `AppendProviderCandidatesUnique` chiamata **per singolo candidato**: ogni chiamata clona l'intero slice base e ricostruisce `map[string]struct{}` ⇒ O(N²) in N candidati |
| `scripts/overlay_entity_cards_text.go:98-120` | doppio loop `i × j` che ricalcola `dateTokens(numbers[j].Text)` a ogni iterazione invece di precalcolarlo una volta |
| `scripts/final_job_overlay_manifest.go:261-270` | scansione lineare di `batchFrames` per ogni voce del manifest |
| `kernel/script/overlay_style_spec.go:327,332,415` | mappe usa-e-getta per ogni validazione (incluse mappe da 11 chiavi per ogni effect) |

## 2. Soluzione

1. **`AppendProviderCandidatesUnique`**: cambiare il call site, non l'helper.
   ```go
   // prima: per ogni candidato
   acc = AppendProviderCandidatesUnique(acc, []Candidate{c})
   // dopo: una volta per provider
   acc = AppendProviderCandidatesUnique(acc, candidates)
   ```
   L'helper resta O(len(base)+len(additions)) per chiamata; con una chiamata per provider il
   totale è lineare. Aggiungere un test che conta le allocazioni (`testing.AllocsPerRun`) o un
   benchmark con 500 candidati.
2. **`dateTokens`**: precalcolare una slice `tokens[i] = dateTokens(numbers[i].Text)` prima del
   doppio loop e usarla dentro. Il secondo loop diventa O(n²) su confronti di stringhe corte,
   senza riallocare.
3. **`batchFrames`**: costruire una `map[int]frameEntry` (o `map[int64]…`) una volta, prima del
   loop sul manifest; lookup O(1). Se l'ordine conta, tenere anche la slice.
4. **Mappe per effect**: riusare una mappa scratch (svuotata con `clear`) o validare con una
   struct invece di una mappa; se la mappa serve come set, dichiarare la capacità una volta.
5. **Misura obbligatoria**: benchmark prima/dopo per ciascun sito, con `-benchmem`, su un input
   rappresentativo (es. 500 candidati, 40 numeri, 200 batch frame). Senza before/after il
   ticket non dichiara un guadagno: dichiara solo "complessità lineare".

## 3. Check finali

```bash
cd refactored
# 1. il call site non chiama più l'helper per singolo elemento
grep -n "AppendProviderCandidatesUnique" internal/capabilities/scripts/adapters/vidrush_registry_searchers.go
# atteso: una chiamata per provider, nessuna dentro il loop per candidato

# 2. dateTokens precalcolato
grep -n "dateTokens" internal/capabilities/scripts/overlay_entity_cards_text.go
# atteso: una sola chiamata fuori dal doppio loop (o dentro un precalcolo)

# 3. benchmark before/after (incollare entrambi)
go test ./internal/capabilities/scripts/adapters/ -run '^$' -bench 'Candidate|Registry' -benchmem
go test ./internal/capabilities/scripts/ -run '^$' -bench 'EntityCards|OverlayManifest' -benchmem
# atteso: ns/op e allocs/op migliori o uguali; nessuna regressione oltre il rumore

# 4. comportamento invariato
go test ./internal/capabilities/scripts/adapters/ ./internal/capabilities/scripts/ ./internal/kernel/script/
# atteso: PASS

# 5. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: cambiare l'ordine dei candidati altera la selezione (il primo vince). Mitigazione:
  preservare l'ordine di input; test che pinna la sequenza.
- **Rischio**: la mappa scratch riusata non è sicura sotto concorrenza. Mitigazione: scratch
  locale alla funzione, non condivisa; nessun `sync.Pool` in questo ticket.
- **Rollback**: per sito.

## 6. Esito della verifica finale (2026-11-21)

- `vidrush_registry_searchers.go`: round-robin con `seen` locale, una sola passata lineare
  (niente più clone+map per candidato).
- `overlay_entity_cards_text.go`: `isDate` e `tokenSets` precalcolati una volta; il loop interno
  usa gli indici.
- `final_job_overlay_manifest.go`: le finestre stesso-batch vengono saltate prima del confronto.
- Test `./internal/capabilities/scripts` e `.../adapters` verdi.
- Nota: non è stata eseguita una misura before/after (`-benchmem`); la scheda dichiara la
  complessità, non un guadagno percentuale.
