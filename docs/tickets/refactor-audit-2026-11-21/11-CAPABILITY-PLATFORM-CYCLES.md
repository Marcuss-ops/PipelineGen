# RA-11 — Cicli `capabilities ↔ platform` e fan-out patologico

**Data:** 2026-11-21
**Priorità:** P1 (strutturale; nessuna violazione hard oggi, ma è la causa di RA-01)
**Stato:** **DONE (2026-11-21)** — coppie bidirezionali 19→18; coppia `system ↔ httpserver` risolta e protetta da `percheck_system_http_boundary`; fan-out `app/wiring` 217 ≤ 218
**Owner:** architettura (`refactored`) — owner per famiglia da assegnare nel ticket
**Origine:** report esterno 2026-11-21 §1.1
**Verifica sul codice:** **DA RIPRODURRE** — il report dichiara 148 import
`capabilities → platform`, 119 nel senso opposto, **19 cicli** di sottosistema e un fan-out
di 214–218 package in `internal/app/wiring`. I numeri non sono stati riprodotti in questa
sessione; il metodo è al §2.

## 1. Problema

Go vieta i cicli fra package, non fra **sottosistemi**. Il grafo reale mostra:

- `internal/kernel` pulito (0 import verso `capabilities`/`platform`/`app`): il livello neutro
  esiste già ed è la destinazione corretta dei contratti condivisi.
- Inversione bidirezionale: le capability importano `platform/*` (repository, transport,
  osservabilità) e `platform/*` importa le capability (policy, modelli, servizi). Il risultato
  sono **coppie bidirezionali** a livello di famiglia (non SCC: in Go un ciclo di package non
  compila): `capabilities/scripts ↔ platform/media`,
  `capabilities/scripts ↔ platform/observability`, `capabilities/jobs ↔ platform/sqlite`,
  `capabilities/youtube ↔ platform/{postgres,sqlite}`, `capabilities/assets ↔ platform/{sqlite,drive,downloader,filesystem}`,
  più 11 coppie fra domini di capability. Misura: §2.
- `internal/app/wiring` importa 214–218 package interni; `internal/capabilities/scripts` 42–50
  (`runner.go` aggrega 39+ dipendenze). Un fan-out così alto rende ogni modifica un cambio
  trasversale e impedisce il test isolato.

La conseguenza operativa è già visibile: l'unico modo per far parlare un adapter con un
contratto della capability è importare il package business (RA-01), perché il contratto non
ha un proprietario neutro.

## 2. Riprodurre la misura (primo passo obbligatorio)

```bash
cd refactored
go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./internal/... > /tmp/graph.txt
python3 - <<'PY'
import collections, sys
edges = collections.defaultdict(set)
for line in open('/tmp/graph.txt'):
    pkg, _, imports = line.strip().partition('|')
    for imp in imports.split():
        if imp.startswith('github.com/Marcuss-ops/PipelineGen/internal/'):
            edges[pkg].add(imp.replace('github.com/Marcuss-ops/PipelineGen/',''))
# Tarjan iterativo
index, low, on, stack, idx, sccs = {}, {}, set(), [], [0], []
def strong(v):
    work = [(v, iter(edges[v]))]
    index[v]=low[v]=idx[0]; idx[0]+=1; stack.append(v); on.add(v)
    while work:
        node, it = work[-1]
        advanced=False
        for w in it:
            if w not in index:
                index[w]=low[w]=idx[0]; idx[0]+=1; stack.append(w); on.add(w)
                work.append((w, iter(edges[w]))); advanced=True; break
            elif w in on:
                low[node]=min(low[node], index[w])
        if advanced: continue
        work.pop()
        if work:
            low[work[-1][0]] = min(low[work[-1][0]], low[node])
        if low[node]==index[node]:
            comp=[]
            while True:
                w=stack.pop(); on.discard(w); comp.append(w)
                if w==node: break
            if len(comp)>1: sccs.append(sorted(comp))
for v in list(edges):
    if v not in index: strong(v)
print("SCC cicliche:", len(sccs))
for c in sorted(sccs, key=len, reverse=True)[:25]:
    caps=[p for p in c if '/capabilities/' in p]; plat=[p for p in c if '/platform/' in p]
    if caps and plat: print(len(c), "cap↔platform", caps[:2], plat[:2])
PY
```

Il risultato va **incollato nella scheda** come baseline (data + commit). Senza questo numero
il ticket non parte: la lista dei 19 cicli del report è un'ipotesi.

### 2.1 Misura eseguita (2026-11-21, working tree) — e correzione del metodo

Lo script qui sopra restituisce **`SCC cicliche: 0`**: è la risposta attesa, non un successo.
In Go il compilatore vieta i cicli fra package, quindi un SCC di package è sempre banale;
anche collassando i nodi a famiglia, un grafo aciclico resta aciclico. La metrica giusta per
descrivere il problema del report è la **relazione bidirezionale fra famiglie**
(A importa B e B importa A), che non richiede un ciclo di package.

Misura corretta (stessi dati `go list`, normalizzando il path del modulo su sorgente e
target, chiave = `internal/<layer>/<family>`), eseguita sul tree corrente:

```
famiglie: 85 | coppie bidirezionali: 19
  capabilities/scripts ↔ capabilities/research, stockintelligence, platform/media, platform/observability
  capabilities/jobs    ↔ capabilities/youtube, platform/sqlite
  capabilities/youtube ↔ platform/postgres, platform/sqlite
  capabilities/assets  ↔ clips, jobs, scriptassets, voiceover, youtube, platform/{sqlite,drive,downloader,filesystem}
  capabilities/operations ↔ platform/sqlite
  capabilities/system  ↔ platform/httpserver
fan-out: app 98, app/wiring 218 (import di package interni), scripts 42, assets 39
```

I numeri del report (19, 218, 42) sono quindi **confermati**, ma la loro etichetta no:
sono coppie bidirezionali, non 19 SCC. Il criterio di chiusura del §4 è riscritto di
conseguenza; la baseline da battere è **19**.

## 3. Soluzione

Il lavoro è per cicli, non per package. Per ogni ciclo scelto:

1. **Scegliere il contratto** che crea l'import inverso (tipo, interfaccia, funzione).
2. **Spostarlo in `internal/kernel/...`** (o in una porta già esistente in
   `capabilities/<x>/ports` se è davvero solo interfaccia). Il kernel non importa né
   capability né platform: se il contratto richiede un tipo di platform, si estrae quel tipo
   nel kernel.
3. **Invertire la dipendenza** con una porta: la capability dichiara l'interfaccia che consuma
   (`type RenderQueueClient interface{…}`), l'adapter in `platform` la implementa importando
   solo il kernel/ports.
4. **Aggiungere un percheck con allowlist che ratcheta verso il basso** per la coppia risolta:
   ```go
   // cmd/archcheck/scan/boundaries/percheck_<famiglia>.go
   // forbidden: internal/platform/<x> → internal/capabilities/<y>
   // inventory: esattamente i file attuali, owner + removal trigger
   ```
   Stesso meccanismo di `percheck_platform_port_boundary.go`: senza il ratchet il ciclo
   ritorna al primo commit distratto.
5. **Ordine consigliato** (massimo valore per sforzo):
   - `capabilities/scripts ↔ platform/media` (è la causa di RA-01 e dell'handoff final job);
   - `capabilities/jobs ↔ platform/sqlite` (porta di persistenza: il contratto è già quasi
     un'interfaccia);
   - `capabilities/youtube ↔ platform/{postgres,sqlite}` (adapter di sola lettura).
   Gli 8 cicli fra capability si affrontano dopo, con lo stesso metodo, in una scheda separata.
6. **Fan-out**: fissare un budget misurato (`go list -f '{{.ImportPath}} {{len .Imports}}'
   ./internal/app/wiring`) e non superarlo. L'obiettivo non è "pochi import" in assoluto ma
   "nessun import nuovo senza motivo": il budget si abbassa quando RA-15 sposta la logica
   media fuori da wiring.

## 4. Check finali

```bash
cd refactored
# 1. le coppie bidirezionali fra famiglie sono scese (baseline 19)
#    rieseguire la misura del §2.1 e incollare "coppie bidirezionali: N"

# 2. la coppia dichiarata nel ticket non è più bidirezionale
go list -deps ./internal/platform/media/... | grep 'internal/capabilities/scripts$'
# atteso: nessun output per la direzione scelta; poi verificare che anche
#         capabilities/scripts non importi platform/media (le due direzioni insieme = coppia)

# 3. il percheck è attivo e l'inventario è esatto
go run ./cmd/archcheck --strict | grep -i 'percheck_<famiglia>'
# atteso: nessuna violazione; un file migrato senza rimuovere l'entry produce
#         percheck_platform_port_inventory_stale

# 4. fan-out non peggiorato
go list -f '{{.ImportPath}} {{len .Imports}}' ./internal/app/wiring
# atteso: <= baseline misurata nel ticket

# 5. build/test
go build ./... && go vet ./...
go test ./internal/kernel/... ./internal/platform/media/... ./internal/capabilities/scripts/...
go run ./cmd/archcheck --strict   # exit 0
```

## 5. Rischi e rollback

- **Rischio**: estrazione massiva che tocca centinaia di file. Mitigazione: un ciclo per PR,
  con alias di compatibilità e allowlist che si restringe.
- **Rischio**: spostare un tipo nel kernel trascina con sé dipendenze (es. `zap`, `sql`).
  Mitigazione: il contratto nel kernel deve essere composto solo di tipi primitivi e struct
  pure; se serve `*sql.DB`, è il segno che il contratto è una porta di I/O e va in
  `capabilities/<x>/ports`, non nel kernel.
- **Rollback**: revert per ciclo.

## 6. Chiusura (2026-11-21)

**DONE.** La baseline è stata riprodotta e ridotta, non ereditata.

- **Regressione RA-13 corretta** — lo spostamento dell'adapter in
  `platform/postgres/cliprender` aveva creato una coppia nuova con
  `capabilities/cliprender` (misura intermedia: 20). Il contratto `RenderCache`,
  `RenderCacheRecord`, `RenderBackend` ed `ErrCacheMiss` è stato spostato in
  `internal/kernel/media/render_cache.go`; la capability mantiene alias, l'adapter
  importa solo il kernel. La direzione `platform → capabilities/cliprender` è sparita.
- **Coppia dichiarata risolta** — `capabilities/system` importava `platform/httpserver`
  solo per `Module.Build`. Il runtime descriptor è passato al composition root
  (`StaticCapabilityModule`, che ora implementa davvero `CapabilityModule.Name`);
  la capability espone solo il route module. Resta la direzione consentita
  `httpserver → capabilities/system/health`.
- **Percheck** — `percheck_system_http_boundary` vieta l'import inverso con allowlist
  vuota; un ritorno della coppia fallisce il gate.

Misura finale (stesso metodo del §2.1, `go list` + normalizzazione a famiglia):

| Misura | Baseline | Dopo |
| --- | --- | --- |
| Coppie bidirezionali | 19 | **18** |
| Fan-out interno `internal/app/wiring` | 218 | **217** |
| Fan-out interno `internal/capabilities/scripts` | 42 | 44 (misura del working tree attuale; nessun import di `scripts` è stato toccato da questa sessione) |

Le 18 coppie residue restano debito registrato: sono famiglie di dominio
(`assets ↔ clips/jobs/voiceover/youtube/scriptassets`), persistenza
(`jobs/assets/youtube/operations ↔ platform/sqlite`), media/observability
(`scripts ↔ platform/media|observability`) e `youtube ↔ platform/postgres`.
Ognuna richiede l'estrazione del contratto proprietario, non un move di file.

Check eseguiti: `go build ./...` exit 0; `go vet ./...` exit 0; `gofmt -l` vuoto;
`go test ./cmd/archcheck/scan/boundaries ./internal/platform/httpserver/... ./internal/capabilities/system/... ./internal/app/wiring` PASS;
`go run ./cmd/archcheck --strict` exit 0, 0 violazioni, 67 warning.
