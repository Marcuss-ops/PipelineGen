# RA-12 — `capabilities/scripts`: SCC di 117 file e god model `GenerateRequest/Result`

**Data:** 2026-11-21
**Priorità:** P1 (strutturale; abilita RA-02 e RA-16)
**Stato:** OPEN — non avviato: richiede una sessione dedicata (piano e ordine in `00-INDEX.md` §5; §8 per il blocco)
**Owner:** scripts capability
**Origine:** report esterno 2026-11-21 §1.1, §5.4
**Verifica sul codice:** **CONFERMATO** il punto di partenza (146 file prod, 40.508 LOC secondo
`architecture/package_hotspots.json`, nota "CONTRACTION BLOCKER MEASURED 2026-11-11: 117-file
strongly-connected closure"); il conteggio SCC va rifatto con lo script.

## 1. Problema

Il package `internal/capabilities/scripts` è una facciata di compatibilità con 146 file
produttivi. La nota di governance dice esplicitamente che:

- a 40.508 LOC e cap 600 righe/file, il minimo assoluto è 68 file;
- l'analisi delle dipendenze mostra una **closure strongly-connected di 117 file** per
  runner e final-job/render-queue;
- solo 5 file sono estraibili senza invertire contratti.

La causa non è la dimensione dei file: sono i **god model** `GenerateRequest` (50+ campi) e
`GenerateResult` (40+ campi) che tengono tutto connesso. Gli split sotto 600 LOC hanno
prodotto "file gemelli" nella stessa directory (128→141 file) senza ridurre la SCC: è
evidenza che il taglio per dimensione non funziona.

## 2. Soluzione — contract-first, per fasi

L'obiettivo non è "meno file" ma "un file per fase, con contratti che non si vedono". Ordine:

1. **Inventario dei campi** (misura, non opinione):
   ```bash
   cd refactored
   awk '/^type GenerateRequest struct/,/^}/' internal/capabilities/scripts/*.go
   awk '/^type GenerateResult struct/,/^}/' internal/capabilities/scripts/*.go
   ```
   Per ogni campo: chi lo **scrive**, chi lo **legge**, in quale fase (testo, audio,
   overlay, final job, pubblicazione). Il risultato è una tabella nella scheda.
2. **Definire i contratti di fase** in sottopackage dedicati (nomi già proposti dal report):
   - `internal/capabilities/scripts/contracts/generation` — input editoriale, canale, lingua,
     budget;
   - `internal/capabilities/scripts/contracts/audio` — timeline, intent SFX/BGM, audio finale;
   - `internal/capabilities/scripts/contracts/finaljob` — piano compilato, asset runtime,
     manifest (in parte esiste già in `scripts/finaljob/contract.go:81-189`).
   Ogni contratto contiene **solo** i campi della sua fase, con tipi già tipizzati.
3. **Migrazione senza big bang**:
   a. aggiungere i sottostrutturati come **campi** (`GenerateRequest.Audio audio.Request`) e
      mantenere i campi piatti come **alias deprecated** con accessor che leggono/Scrivono il
      sottostrutturato (una sola fonte di verità);
   b. migrare i lettori fase per fase;
   c. rimuovere i campi piatti quando nessun lettore li usa (misura con
      `grep -c` per campo e una build che fallisce se ricompaiono).
4. **Tagliare solo ciò che è foglia**: dopo (3), rieseguire lo script SCC del §2 di RA-11. Si
   sposta un file nel sottopackage **solo** se non riporta la SCC dentro il root. Il criterio
   di chiusura è il conteggio SCC e il conteggio file (≤141, RA-02), non il LOC per file.
5. **Vietare i nuovi gemelli**: aggiungere alla policy una regola "nessun nuovo file nel root
   `scripts` senza una voce di contratto" — in pratica il cap di RA-02 fa già da freno, ma
   serve una nota nel `note` dell'hotspot che dica che il taglio ammesso è solo per contratto.
6. **Test di proprietà**: un test che, per ciascun contratto di fase, verifichi che il
   sottostrutturato è popolato e che il campo piatto corrispondente non è più letto
   (grep-based test o `go/ast`). Impedisce il ritorno del doppio binario.

## 3. Check finali

```bash
cd refactored
# 1. dimensione e conteggio
find internal/capabilities/scripts -maxdepth 1 -name '*.go' ! -name '*_test.go' | wc -l
# atteso: <= 141
go run ./cmd/archcheck --strict   # nessuna violazione pkg_size su scripts

# 2. SCC ridotta: rieseguire lo script di RA-11 e incollare il numero
#    atteso: SCC < 117 (e nessun aumento di cicli altrove)

# 3. i contratti di fase esistono e sono usati
go list ./internal/capabilities/scripts/contracts/...
go test ./internal/capabilities/scripts/... -run 'Contract|GenerateRequest|GenerateResult' -v
# atteso: PASS

# 4. nessun campo piatto residuo per la fase migrata
grep -rn "\.<CampoMigrato>" internal/capabilities/scripts/ | grep -v _test
# atteso: nessun output (o solo l'accessor documentato)

# 5. comportamento invariato: suite del package verde
go build ./... && go vet ./...
go test ./internal/capabilities/scripts/...
# atteso: PASS (i test live possono richiedere env: dichiarare SKIP)

# 6. wiring non è peggiorato
go list -f '{{.ImportPath}} {{len .Imports}}' ./internal/app/wiring
```

## 4. Rischi e rollback

- **Rischio**: gli alias deprecated diventano permanenti e la doppia scrittura diverge.
  Mitigazione: accessor unico (una sola fonte), test di proprietà, scadenza annotata.
- **Rischio**: un campo è letto via reflection/JSON tag e sfugge al grep. Mitigazione:
  censimento anche per tag JSON (`grep -rn '"json:"<tag>'`) e test di round-trip.
- **Rischio**: conflitto con lavoro in corso su `scripts` (area ad alto churn). Mitigazione:
  rebase frequente, un contratto per PR.
- **Rollback**: i passi (3a) e (3c) sono reversibili separatamente; i sottopackage possono
  restare anche se la migrazione si ferma.
