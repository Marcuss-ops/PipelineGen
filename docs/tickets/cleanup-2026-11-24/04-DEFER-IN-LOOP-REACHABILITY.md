# CLEANUP-04 — `defer` dentro il loop delle orphan query

**Data:** 2026-11-24
**Priorità:** P2 (igiene, rischio zero)
**Stato:** DONE (commit `d21cc4de9`)
**Owner:** `cmd/admin/internal/audit`
**Origine:** radiografia 2026-11-24, §2D
**Base:** `refactored` @ `2a3ea0b2c` (working tree pulito)

## 1. Problema

In `cmd/admin/internal/audit/reachability_graph.go:469` il ciclo sulle tabelle
registra:

```go
defer func() { _ = orphanRows.Close() }()
```

per ogni iterazione. Le closure si accumulano fino al ritorno della funzione
esterna, anche se il risultato è già stato consumato. Subito dopo il loop c'è
già la chiusura esplicita:

```go
_ = orphanRows.Close()
```

Quindi il `defer` è ridondante. Il caso `break` per errore di `Scan` esce dal
loop e raggiunge comunque la chiusura esplicita; non c'è un percorso che salti
la `Close`.

## 2. Soluzione

Rimuovere la sola riga del `defer`. La chiusura esplicita resta l'unico owner
del ciclo di vita di `orphanRows`. Non si introduce `defer` in una funzione
anonima per iterazione: sarebbe la stessa cosa con più codice.

## 3. Check finali

```bash
cd refactored
rg -n 'defer func\(\) \{ _ = orphanRows.Close\(\) \}' cmd/admin/internal/audit/reachability_graph.go
# atteso: nessun risultato
go test ./cmd/admin/internal/audit/... -count=1
go vet ./cmd/admin/internal/audit/...
gofmt -l cmd/admin/internal/audit/reachability_graph.go
```

## 4. Esito (2026-11-24)

- `defer func() { _ = orphanRows.Close() }()` rimosso; resta la chiusura
  esplicita dopo il loop. `rg` sul file → nessun risultato.
- Riverifica finale: `go test ./cmd/admin/internal/audit/... -count=1` PASS
  (0,174 s); `go vet` sullo stesso package rc 0.
- `gofmt -l` sul file → vuoto.
- Commit: `d21cc4de9` — `fix(admin): close orphan rows outside the loop`.

## 5. Cosa resta

Niente di bloccante. Il ticket è DONE e committato; il `defer` ridondante è
rimosso e la chiusura esplicita resta l'unico owner di `orphanRows`. Restano
fuori perimetro push e suite completa
(`TestA1_ScaleBacklogInteractiveFirstClaimUnderBudget` flaky sotto carico).

