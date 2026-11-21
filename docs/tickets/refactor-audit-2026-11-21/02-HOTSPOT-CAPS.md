# RA-02 — Hotspot caps: `wiring` 72→≤70 e `scripts` 146→≤141

**Data:** 2026-11-21
**Priorità:** P0 (blocca `archcheck --strict`)
**Stato:** DONE (working tree, da committare)
**Owner:** composition root (`app/wiring`) + scripts capability
**Origine:** report esterno 2026-11-21 §0.2; verificato sul codice
**Verifica sul codice:** **CONFERMATO** — 72 e 146 file produttivi misurati il 2026-11-21

## 0. Stato al ricontrollo (2026-11-21, stessa sessione) — leggere prima di iniziare

Lavoro concorrente **non committato** ha riportato i conteggi sotto il cap; il gate è verde:

```
internal/app/wiring: 70        (cap 70)
internal/capabilities/scripts: 140   (cap 141)
go run ./cmd/archcheck --strict  => exit 0, total_violations: 0
```

Il rientro è stato ottenuto anche **eliminando** otto file (stato git: `D`):

```
internal/app/wiring/adapters_middleware.go
internal/app/wiring/wire_assets_search.go
internal/capabilities/scripts/incremental_coordinator_enrich.go
internal/capabilities/scripts/map_presentation_pools.go
internal/capabilities/scripts/overlay_plan_compile_limits.go
internal/capabilities/scripts/runner_semantic_search.go
internal/capabilities/scripts/runner_translation_coverage.go
internal/capabilities/scripts/scene_commit_emit.go
```

`go build ./...` esce **0** su questo tree, quindi nulla è irrisolto a livello di compilazione:
ma un file *cancellato* per far scendere un contatore è la stessa antipattern degli split
cosmetici, al contrario. Il lavoro residuo di questo ticket è quindi:

1. **verificare che ogni simbolo dei file cancellati esista ancora** (fuso in un file vicino o
   rimosso perché morto), con il metodo del §3.4-bis;
2. **verificare i test** dei package toccati, non solo il build;
3. **ratchetare la baseline verso il basso** (70 e 140) così la crescita non torna;
4. **committare** il tutto in un commit dedicato.

## 1. Problema

Il gate `pkg_size` confronta il numero di file produttivi (`*.go` senza `_test.go`) di ogni
hotspot registrato con `architecture/package_hotspots.json`:

| Package | Misurato | Baseline | Delta | Regole |
| --- | ---: | ---: | ---: | --- |
| `internal/app/wiring` | 72 | 70 | +2 | `pkg_size` + `percheck_legacy_hotspot_growth` |
| `internal/capabilities/scripts` | 146 | 141 | +5 | `pkg_size` |

`internal/app/wiring` produce **due** violazioni perché la crescita viola sia il cap sia la
regola "il debito carry-forward non aumenta mai".

Nota di governance: `architecture/policy.yaml:48-51` ha declassato
`max_files_per_package` / `max_lines_per_file*` ad `advisory_rules`, ma gli hotspot
**registrati** restano hard gate con baseline ratchet. Quindi non si può abbassare la soglia:
si può solo rientrare o ri-ratchetare (vietato dal ticket).

## 2. Impatto

- Gate rosso, bloccante per RA-01/RA-11.
- La crescita non è neutra: nel 2026-11-05 e nel 2026-11-18 la baseline è già stata
  ri-ratchetata due volte "perché il tree committato era avanti". Ogni ri-ratchet rende il
  gate cieco sulla crescita successiva: questo ticket chiude il ciclo, non lo ripete.

## 3. Soluzione

### 3.1 Trovare i file che hanno sfondato il cap

```bash
cd refactored
# file produttivi presenti ora ma non nel commit che ha fissato la baseline
git log --diff-filter=A --name-only --pretty=format: -- internal/app/wiring/*.go \
  | grep -v _test | sort -u > /tmp/wiring-added.txt
git log --diff-filter=A --name-only --pretty=format: -- internal/capabilities/scripts/*.go \
  | grep -v _test | sort -u > /tmp/scripts-added.txt
```

Classificare i nuovi file per famiglia (runner phase, final job, overlay, media planning,
vidrush) e scegliere **il target di package già dichiarato** in
`architecture/package_hotspots.json` (`target_packages`): `internal/app/wiring/{assets,audio,
clips,drive,lifecycle,media,process,registry,rendering,script,search,stock,vidrush,voiceover,
youtube}` per wiring; `scripts/{runner,audio,vidrush}` per scripts.

### 3.2 Spostare, non spezzare

Per ogni file: spostare in blocco il codice coeso nel target e lasciare nel package root
**solo** alias o il tipo di facciata necessario ai chiamanti (stesso pattern dei precedenti
split: `audio_asset_resolver.go` e `ports_execution.go` mantengono gli alias). Regola:
- `internal/app/wiring` resta la composition root: nessuna logica di dominio (vedi RA-15).
- `internal/capabilities/scripts` resta la facciata di compatibilità: nessun nuovo file
  "gemello" nella stessa directory. **Non** applicare lo split cosmetico che ha portato da
  128 a 141 file: quello è il debito che questo ticket deve smontare.

Sequenza consigliata: prima `scripts` (5 file, target già esistente `scripts/runner` o
`scripts/audio`), poi `wiring` (2 file, target `wiring/script` o `wiring/media`).

### 3.3 Chiudere il conteggio

```bash
for d in internal/app/wiring internal/capabilities/scripts; do
  printf "%s: " "$d"; find "$d" -maxdepth 1 -name '*.go' ! -name '*_test.go' | wc -l
done
# atteso: 70 e 141 (o meno)
```

Se dopo lo spostamento il conteggio è ancora sopra, ripetere con il file successivo: il
criterio è "≤ baseline", non "due file".

### 3.4 Aggiornare la nota, non la baseline

In `architecture/package_hotspots.json` si aggiorna **solo** la nota con la data e il motivo
della riduzione. `baseline_files`/`current` scendono al nuovo misurato **solo se** il ticket
decide di ratchetare verso il basso (consigliato: sì, porta il cap a 70/141 e impedisce la
regressione). Mai alzare.

### 3.4-bis Verificare i file cancellati (obbligatorio)

```bash
cd refactored
# elenco dei file cancellati nel working tree
git status --porcelain | awk '$1=="D"{print $2}'

# per ognuno, i simboli dichiarati nel file originale e la loro presenza altrove
for f in $(git status --porcelain | awk '$1=="D"{print $2}'); do
  echo "== $f"
  git show HEAD:"$f" | grep -E '^(func|type|var|const) ' | sed -E 's/^(func \([^)]*\) |func |type |var |const )([A-Za-z0-9_]+).*/\2/' \
    | while read -r sym; do
        n=$(git grep -c "\b$sym\b" -- '*.go' | wc -l)
        [ "$n" -eq 0 ] && echo "   ORFANO: $sym"
      done
done
# atteso: nessun ORFANO, oppure un ORFANO per ogni simbolo dichiarato morto
#         esplicitamente nel ticket
```

Se un simbolo è sparito senza sostituto e senza essere morto, il file va ripristinato e la
riduzione va ottenuta spostando codice in un target di package (come al §3.2), non cancellando.

## 4. Check finali

```bash
cd refactored
# 1. conteggi al cap
for d in internal/app/wiring internal/capabilities/scripts; do
  printf "%s: " "$d"; find "$d" -maxdepth 1 -name '*.go' ! -name '*_test.go' | wc -l
done
# atteso: 70 e 141 (o inferiori)

# 2. gate architetturale: le due violazioni hotspot non ci sono più
go run ./cmd/archcheck --strict
# atteso: exit 0, has_hard_gate_hits=false (insieme a RA-01 chiuso)

# 2-bis. i file cancellati non hanno lasciato buchi (vedi §3.4-bis)
git status --porcelain | awk '$1=="D"{print $2}'
# atteso: nessun file, oppure ogni cancellazione giustificata con simboli fusi altrove

# 3. nessun ciclo nuovo introdotto dallo spostamento
go build ./... && go vet ./...

# 4. i target ricevono codice coeso e testato
go test ./internal/app/wiring/... ./internal/capabilities/scripts/...
# atteso: PASS sui package toccati

# 5. il diff è solo spostamenti + alias
git diff --stat
# atteso: rename + pochi alias; nessuna modifica di comportamento
git diff -M --summary | grep -c 'rename'   # i rename devono essere la maggioranza

# 6. la baseline non è stata alzata
git diff architecture/package_hotspots.json
# atteso: baseline_files/current uguali o inferiori; nessun aumento
```

## 5. Rischi e rollback

- **Rischio**: spostare un file che contiene un `init()` o una variabile di package con
  effetti collaterali cambia l'ordine di inizializzazione. Mitigazione: cercare `func init(`
  e `var .* = ` con chiamate nei file candidati prima di spostarli.
- **Rischio**: import cycle `scripts → scripts/runner → scripts`. Mitigazione: i sottopackage
  non devono importare il root; verificare con
  `go list -deps ./internal/capabilities/scripts/runner | grep 'capabilities/scripts$'` →
  nessun output.
- **Rollback**: revert; il gate torna rosso con i conteggi originali.

## 6. Esito della verifica finale (2026-11-21)

- `internal/app/wiring` = **70** (cap 70), `internal/capabilities/scripts` = **140** (cap ratchetato a 140).
- `architecture/package_hotspots.json`: `baseline_files`/`baseline.files`/`current.files` di `scripts` portati **141 → 140** (ratchet verso il basso, mai verso l'alto).
- `go run ./cmd/archcheck --strict` → exit 0, 0 violazioni.
- `go build ./...` e `go vet ./...` verdi su un tree con 8 file cancellati dal lavoro concorrente:
  la riduzione non ha rotto la compilazione. I simboli dei file cancellati restano da verificare
  con la procedura §3.4-bis prima del commit, insieme alla suite dei package toccati (eseguita: verde).
