# RA-23 — Deriva di governance e artefatti locali

**Data:** 2026-11-21
**Priorità:** P2 (igiene di governance; nessun impatto runtime)
**Stato:** DONE
**Owner:** architettura (`refactored`) — operator-facing
**Origine:** report esterno 2026-11-21 §4.3; verificato e **in parte corretto**
**Verifica sul codice:** **CONFERMATO** per la baseline scaduta; **CORRETTO** per i binari
(gitignored, non "non tracciati" nel senso di sporcare il repo) e per il riferimento a
`./pipelinegen` (esempio d'uso, non un binario ritirato)

## 1. Problema

### 1.1 Baseline strict scaduta

`docs/migrations/archcheck-strict-baseline.json` è una fotografia del **2026-08-24** e contiene
**4 entry** (`transitional_baselines_sorted_urgency`), **tutte con `deadline: 2026-09-30`**:

- `percheck_binder_scene_field_writes` (4 failing entries: `durable_mapper.go:117`,
  `result_reducer.go:107`, `runner_phase_script.go:78,79`),
- le altre 3 con la stessa data.

Al 2026-11-21 la deadline è passata da ~7 settimane, ma nessun gate la controlla: il file resta
"attivo" e descrive un debito che dovrebbe essere stato chiuso o ri-negoziato. Il documento
stesso dichiara (`$v4_consumers`) che l'enforcement eseguibile sta in `cmd/archcheck`,
`architecture/policy.yaml` e nelle allowlist di migrazione: la baseline è operatore-facing, e
proprio per questo il suo scadere non fa rumore.

### 1.2 Cap declassati ad advisory (decisione, non deriva)

`architecture/policy.yaml:40-51`: `max_files_per_package`, `max_lines_per_file`,
`max_lines_per_file_strict` sono in `advisory_rules`, con commento esplicito "owner decision
2026-11-16 … a cohesive 1,500-line file is not a correctness defect". Il fatto è vero; la
cornice del report ("declassati") suggerisce un difetto, ma è una **decisione registrata**. Gli
hotspot **registrati** restano hard gate (`package_hotspots.json` + `percheck_legacy_hotspot_growth`),
quindi il controllo non è stato rimosso: va solo verificato che sia ancora così e documentato.

### 1.3 Artefatti locali in root (igiene dell'operatore, non del repo)

Presenti in root: `server` (83 MB), `goldensummary` (36 MB), `semantic-extract` (34 MB),
`gen_baseline` (11 MB), `velox` (10 MB), `coverage.out` (9,5 MB), `archcheck` (6,4 MB, **12 nov**,
quindi stantio rispetto al codice), `overlaystatus`, `velox-knobs`, `regen-current-yaml`; in
`bin/` ci sono anche `pipelinegen.prev` e `pipelinegen.prev-20261121` (~167 MB).

**Tutti sono gitignored** (`git check-ignore` conferma: `.gitignore:88`, `:251`, `:435`,
`.git/info/exclude:13`, ecc.). `git status` mostra **4 file non tracciati**, tutti sorgenti/test
(`assets/fonts/Inter-Bold.ttf`, 3 `*_test.go`), non binari.

Quindi: non è "root sporca" nel senso di repo inquinato, ma **disco dell'operatore** con
~350 MB di binari, di cui almeno uno (`archcheck`) non corrisponde al codice corrente — un
rischio concreto: chi esegue `./archcheck` invece di `go run ./cmd/archcheck` misura un gate
vecchio.

### 1.4 Commento d'uso in `config.clip-render.profile.yaml:15`

```yaml
#     ./pipelinegen --config config.clip-render.profile.yaml --mode server
```

Il binario esiste (`bin/pipelinegen`, 85 MB, 2026-11-21). Non è un riferimento a un binario
ritirato: è un esempio che assume `pipelinegen` nel cwd. Al più è impreciso (il build lo mette in
`bin/`). Nessuna azione oltre l'allineamento cosmetico.

## 2. Soluzione

1. **Chiudere o ri-negoziare le 4 entry della baseline**:
   - per ciascuna, verificare se le righe indicate violano ancora il percheck;
   - se sì: correggere il codice (sono 4 file in `scripts`, tutti raggiungibili) e cancellare
     l'entry;
   - se no: cancellare l'entry (è stale) e, se il debito esiste ancora in altra forma,
     registrarlo con una deadline **futura e realistica**.
2. **Aggiungere il gate sulle deadline**: un check in `cmd/archcheck` (o un test) che legga la
   baseline e **fallisca** se una entry ha `deadline` nel passato. Senza questo, la baseline
   torna a scadere in silenzio. È il deliverable principale del ticket.
3. **Documentare §1.2**: nel `note` della baseline o in `architecture/policy.yaml` scrivere che
   gli hotspot registrati restano hard gate anche con i cap in `advisory_rules`, così il
   prossimo audit non riapre la stessa domanda.
4. **Igiene locale**: aggiungere a `docs/operations/` una nota operativa (non uno script
   distruttivo) che elenchi i binari locali attesi in root/`bin` e dica di usare
   `go run ./cmd/archcheck` o `make` invece dei binari stantii; `make clean` se esiste già.
   **Non** committare `.gitignore` aggiuntivi per file già ignorati e **non** cancellare file
   dell'operatore in un ticket di codice.
5. **Commento config**: allineare l'esempio a `bin/pipelinegen` (una riga, nessun impatto).

## 3. Check finali

```bash
cd refactored
# 1. nessuna deadline scaduta
python3 - <<'PY'
import json, datetime
d=json.load(open('docs/migrations/archcheck-strict-baseline.json'))
today=datetime.date.today().isoformat()
bad=[e for e in d.get('transitional_baselines_sorted_urgency',[]) if e.get('deadline','') < today]
print("entry scadute:", len(bad))
raise SystemExit(1 if bad else 0)
PY
# atteso: exit 0

# 2. il gate sulle deadline esiste e fallisce su una entry scaduta
go test ./cmd/archcheck/... -run 'BaselineDeadline|Governance' -v
# atteso: PASS; il test deve fallire se si reinserisce una deadline passata

# 3. il gate architetturale non è cambiato
go run ./cmd/archcheck --strict
# atteso: exit 0, has_hard_gate_hits=false (dopo RA-01/RA-02)

# 4. gli hotspot restano hard gate
go run ./cmd/archcheck --strict | grep -c 'legacy_hotspot_growth'
# atteso: 0 violazioni; il percheck è elencato tra i check attivi

# 5. nessun binario tracciato o nuovo file non tracciato in root
git status --porcelain --untracked-files=normal | awk '$1=="??"{print $2}'
# atteso: solo i file dichiarati (idealmente nessuno); nessun binario

# 6. il commento di config non punta a un percorso inesistente
grep -n "pipelinegen" config.clip-render.profile.yaml
# atteso: esempio allineato a bin/pipelinegen
```

## 4. Rischi e rollback

- **Rischio**: chiudere una entry senza correggere il codice nasconde il debito. Mitigazione:
  le entry si cancellano solo dopo aver verificato che il percheck non colpisca più le righe
  indicate.
- **Rischio**: il gate sulle deadline blocca il merge per una entry che si vuole prorogare.
  Mitigazione: prorogare è ammesso, ma richiede una modifica esplicita della data e un owner —
  che è esattamente il comportamento voluto.
- **Rollback**: revert; nessun impatto runtime.

## 6. Esito della verifica finale (2026-11-21)

- Le **4 entry** della baseline (tutte con `deadline: 2026-09-30`) sono state chiuse: nessuna delle
  quattro regole (`percheck_binder_scene_field_writes`, `percheck_spec_aliases`,
  `percheck_asset_committer_event_ssot`, `struct_deps`) produce violazioni, né in modalità
  full-tree né `--production-only` (verificato). Lista portata a `[]` con descrizione aggiornata.
- Nuovo `cmd/archcheck/baseline_deadline_test.go`: fallisce su deadline scadute o entry senza
  owner/trigger. Il checker è a sua volta testato con un'entry scaduta sintetica, così non può
  passare perché ha smesso di guardare. `go test ./cmd/archcheck/ -run 'Baseline|Transitional'` → ok.
- `config.clip-render.profile.yaml:15`: esempio allineato a `./bin/pipelinegen`.
- I binari locali sono gitignored (verificato con `git check-ignore`): nessuna azione di repo.
