# Code Health Improvement Plan — 2026-07-09

Piano d'azione derivato dall'audit diagnostico della codebase (2,406 file Go,
284,917 LOC). Priorità incrociata per **frequenza di commit × complessità**
secondo la matrice: Alta frequenza + Alta complessità = PRIORITÀ ASSOLUTA.

---

## §1 — Diagnostica rapida (stato al 2026-07-09)

| Metrica | Valore | Soglia critica |
|---------|--------|----------------|
| File Go totali | 2,406 | — |
| Directory + affollata (non-test) | `internal/app` (107 file) | >30 = warning, >40 = CI fail |
| File + grande (non-test) | `voiceover/ports.go` (668 LOC) | >500 = split candidate |
| `go vet` issues in `internal/` | **0** ✅ | — |
| Package-level `var X map/slice` exported | **0** ✅ | — |
| `sync.Mutex`/`RWMutex` in production | ~70 | — |
| `os.ReadFile`/`os.Open` in `internal/application/` | ~112 | pattern I/O binder |
| Hotspot #1 | `CHANGELOG.md` (393 commit) | doc, atteso |
| Hotspot #2 | `internal/app/composition.go` (129) | **PRIORITÀ** |
| Hotspot #3 | `internal/app/registry.go` (123) | **PRIORITÀ** |
| Hotspot #4 | `internal/app/wire_script.go` (79) | medio |

---

## §2 — 8 Aree di intervento (ordinate per priorità)

### 🔥 BAND A: PRIORITÀ ASSOLUTA (Alta freq. × Alta complessità)

#### A1 — `internal/app/` mega-package (107 file, 129 commit/90gg)
**Problema:** Il composition root è un monolite da 107 file. Ogni modifica a
un wiring tocca file adiacenti e il rischio di conflitto è altissimo.
**Azione:** Split per capability bundle (già iniziato con
`wire_assets_*.go`). Continuare con:
- Estrarre `wire_script_*.go` in sotto-file per capability
- Applicare Pattern 5: max ~15 file per sottopackage
- **File target:** `composition.go` (510 LOC), `registry.go`, `lifecycle.go`

#### A2 — File enormi (>500 LOC) — Top 5
1. `voiceover/ports.go` (668) → separare interfacce da DTO
2. `scripts/usecase/translation.go` (624) → split per fase
3. `voiceover/process_segment.go` (569) → split per step
4. `api/assets/stock/handler.go` (568) → split handler + DTO + validation
5. `ai/ollama/generate.go` (554) → split per strategia di generazione

### 🟠 BAND B: ALTA PRIORITÀ (Complessità/Fragilità)

#### B1 — I/O sincrono diretto in `internal/application/`
**Problema:** ~112 chiamate `os.ReadFile`/`os.Open` nel layer applicativo
violano il pattern I/O Binder (Pattern 0). I file system cambiano, il
codice applicativo no.
**Azione:**
- Identificare i 10-15 call site più critici (hot path di generazione script,
  pipeline stock, enrichment)
- Aggiungere porte tipizzate nei package `ports.go`
- Spostare I/O negli adapter infrastrutturali

#### B2 — `internal/app/build_bundles_domain.go` (510 LOC, 62 commit)
**Problema:** Il costruttore del domain bundle è un collo di bottiglia
per ogni nuova capability.
**Azione:** Split per dominio estratto (giobs, assets, scripts, media).

#### B3 — Duplicazione error-string e `fmt.Errorf` non tipizzati
**Problema:** 162+ `fmt.Errorf(...)` call sparsi; molti non usano
`sentinel %w` wrapping.
**Azione:** Audit delle 20 stringhe di errore più duplicate; estrarre
in sentinelle tipizzate `errors.Is`-compatibili.

### 🟡 BAND C: PRIORITÀ MEDIA (Toccare se si rompe o in finestra)

#### C1 — Dead code / file obsoleti
**Problema:** `staticcheck` non ha completato (timeout), ma `rg TODO|FIXME`
mostra ~35 forward-pointer scaduti o riferimenti a wave archiviate.
**Azione:**
- Rimuovere TODO riferiti a wave archiviate (Wave 1-13)
- Verificare file in `internal/api/sources/` — la directory esiste ancora?

#### C2 — `internal/application/jobs/` — retry fragili
**Problema:** `resolveMaxRetries` con fallback `return 3` già rimosso
(PR-jobs-retry-contract), ma auditing va completato su tutti i tipi job.
**Azione:** Verificare che ogni `job.Type*` abbia `MaxRetries` esplicito
nel registry.

#### C3 — Package-level `sync.Mutex` non necessario
**Problema:** ~70 mutex nel codice; alcuni potrebbero essere sostituiti
con `sync.Once` o non servire affatto (es. `enrichMetaMu` in
`semantic_enricher.go`).
**Azione:** Audit dei 10 mutex package-level più sospetti.

### 🟢 BAND D: PRIORITÀ BASSA (Manutenzione ordinaria)

#### D1 — Commenti `// TODO` e `// FIXME` scaduti
**Azione:** Rimuovere o aggiornare i ~35 TODO trovati nel layer
applicativo. Quelli riferiti a wave completate vanno eliminati.

#### D2 — `scripts/` directory — cleanup Python
**Azione:** Verificare che gli script Python siano ancora allineati
con le porte Go; rimuovere script non più chiamati.

---

## §3 — Ordine di esecuzione consigliato

```
A1.1 → A1.2 → A2.1 → A2.2 → A2.3 → B1.1 → B1.2 → B2 → C1 → C2 → C3 → D1 → D2
  └── Prima i mega-package e i file enormi (bloccano tutto il resto)
       └── Poi I/O binder (previene regressioni)
            └── Poi pulizia (dead code, TODO, sentinelle)
```

Ogni intervento atterra su `main` come commit atomico auto-sufficiente.
**NO branches, NO PR, NO `--force`.** Push diretto dopo `gofmt + go vet + go build + go test -short`.

---

## §4 — Criteri di verifica per ogni intervento

- `gofmt -l` pulito sui file toccati
- `go vet ./<package>/...` exit 0
- `go build ./<package>/...` exit 0
- `go test -short -count=1 ./<package>/...` PASS
- Se split di file: simboli esportati invariati, lookup path preservati

---

## §5 — Forward-pointer

- `PR-CODE-HEALTH-HOTSPOT-CROSSREF` (deadline 2026-08-15): cross-validazione
  post-wave con `git log --since=90.days` per verificare che nuovi hotspot
  non siano emersi dopo gli interventi.
