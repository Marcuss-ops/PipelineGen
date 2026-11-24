# CLEANUP-05 — Ondata 2A: 13 adapter `rustexec` fuori dall'inventario

**Data:** 2026-11-24
**Priorità:** P1 (strutturale, sblocca il ratchet archcheck)
**Stato:** OPEN
**Owner:** `media-rust` + `kernel/script`
**Origine:** radiografia 2026-11-24, Ondata 2; inventario RA-01
**Base:** `refactored` @ `3e0a9e3ae`

## 1. Problema

`cmd/archcheck/scan/boundaries/percheck_platform_port_boundary.go` registra
**13 file** `internal/platform/media/rustexec/*.go` che importano ancora
`internal/capabilities/scripts` (o un suo simbolo non qualificato). Il gate è
verde solo perché le entry sono nell'inventario; la metà `stale` impone di
cancellare ogni entry nello stesso cambio che migra il file.

Il trigger registrato — **`semantic ports move to kernel/script`** — è ora
azionabile: `internal/kernel/script/semantic` esiste e i tre file
`semantic_engine_{discourse,people_events,places}.go` sono già migrati
(RA-01). Restano i 13 file sotto.

| File | Simboli `scriptgen.*` unici | Famiglia |
| --- | ---: | --- |
| `semantic_adapters.go` | 7 | porte/candidate |
| `semantic_engine.go` | 7 | analyzer/input/identity |
| `semantic_engine_consolidation.go` | 13 | consolidamento |
| `semantic_engine_contract.go` | 3 | contratto |
| `semantic_engine_editorial.go` | 3 | editorial batch |
| `semantic_engine_entity_linking.go` | 6 | entity linking |
| `semantic_engine_identity.go` | 2 | identità localizzata |
| `semantic_engine_phrases.go` | 5 | phrase alignment/selection |
| `semantic_engine_projection.go` | 21 | proiezione |
| `semantic_engine_search_feedback.go` | 4 | search feedback |
| `semantic_engine_story.go` | 5 | story graph |
| `semantic_engine_quantities.go` | 3 | quantità |
| `video_processor.go` | 0 (`scripts.FinalAudioReference`, `scripts.AudioPipelineMetrics`) | audio render bridge |

Censimento riproducibile:

```bash
cd refactored
for f in semantic_adapters.go semantic_engine.go semantic_engine_consolidation.go \
  semantic_engine_contract.go semantic_engine_editorial.go \
  semantic_engine_entity_linking.go semantic_engine_identity.go \
  semantic_engine_phrases.go semantic_engine_projection.go \
  semantic_engine_search_feedback.go semantic_engine_story.go \
  semantic_engine_quantities.go video_processor.go; do
  p="internal/platform/media/rustexec/$f"
  printf '%s: ' "$f"
  rg -o 'scriptgen\.[A-Za-z0-9_]+' "$p" | sort -u | sed 's/scriptgen\.//' | tr '\n' ' '
  echo
done
```

## 2. Soluzione

1. **Censimento per file** con il comando sopra; separare DTO puri
   (struct/enum con metodi puri) da funzioni/validatori con policy o I/O.
2. **Spostare i DTO verbatim** nei package kernel già esistenti o in un nuovo
   `internal/kernel/script/semantic` (stesso nome usato da RA-01). Regola del
   kernel: zero import da `internal/capabilities/**` e `internal/platform/**`.
3. **Porte e tipi di frase** (`PhraseAligner`, `PhraseSelector`,
   `PhraseSelectionRequest`, `SelectedPhrase`, `PhraseSelectionProfile`) vanno
   nel kernel, non in `capabilities/audio`: il contratto non può restare in una
   capability solo perché oggi è comodo.
4. **Alias temporanei** in `internal/capabilities/scripts` per non rompere i
   chiamanti; gli alias si rimuovono quando gli adapter importano il kernel.
5. **Aggiornare gli adapter** per famiglia, un PR per volta (es.
   consolidation → projection → story/editorial → entity linking/identity →
   phrases/quantities/search feedback → adapters/engine → video processor).
   Ogni PR cancella le entry `platformCapabilityScriptsInventory` dei file
   migrati; il gate `stale` fallisce se la cancellazione manca.
6. **`video_processor.go`**: l'import è non qualificato (`scripts "..."`), non
   `scriptgen`. Spostare `FinalAudioReference` e `AudioPipelineMetrics` nel
   kernel audio/script e far importare all'adapter solo il kernel.
7. **Non allargare** `platformAllowedCapabilitySubpackages`: l'unica eccezione
   ammessa resta `internal/capabilities/scripts/ports`.

## 3. Criteri di accettazione

```bash
cd refactored
# 1. nessun adapter della lista importa il package business
for f in semantic_adapters.go semantic_engine.go semantic_engine_consolidation.go \
  semantic_engine_contract.go semantic_engine_editorial.go \
  semantic_engine_entity_linking.go semantic_engine_identity.go \
  semantic_engine_phrases.go semantic_engine_projection.go \
  semantic_engine_search_feedback.go semantic_engine_story.go \
  semantic_engine_quantities.go video_processor.go; do
  rg -n 'internal/capabilities/scripts' "internal/platform/media/rustexec/$f"
done
# atteso: nessun output

# 2. inventario rustexec vuoto e gate verde
python3 - <<'PY'
import re
s=open('cmd/archcheck/scan/boundaries/percheck_platform_port_boundary.go').read()
m=re.search(r'var platformCapabilityScriptsInventory = map\[string\]string\{(.*?)\n\}', s, re.S)
rows=re.findall(r'"([^"]+)":', m.group(1))
print('rustexec entries:', sum('/rustexec/' in f for f in rows))
PY
# atteso: 0
go run ./cmd/archcheck --strict   # exit 0, has_hard_gate_hits: false

# 3. il kernel resta foglia
go list -deps ./internal/kernel/script/... | grep -E 'internal/(capabilities|platform)'
# atteso: nessun output

# 4. build/vet/test dei package toccati
go build ./...
go vet ./internal/platform/media/rustexec/... ./internal/kernel/script/...
go test ./internal/platform/media/rustexec/... ./internal/kernel/script/... \
  ./internal/capabilities/scripts/... -count=1
# i test che avviano il binario Rust reale possono SKIP: riportare SKIP, non PASS
```

## 4. Rischi e rollback

- **Rischio:** un DTO ha metodi che leggono env o fanno I/O. Mitigazione: si
  sposta solo il tipo; il metodo con I/O resta nell'adapter o diventa una porta.
- **Rischio:** l'alias temporaneo in `scripts` ricrea il debito. Mitigazione:
  ogni PR rimuove gli alias della famiglia migrata e il check 3 lo verifica.
- **Rollback:** il cambiamento è di import e package; nessuna migrazione dati.
  Si ripristinano gli import e le entry di inventario nello stesso commit.
- **Regola di chiusura:** CLEANUP-05 si chiude solo quando l'inventario
  `rustexec` è zero; nessuna chiusura per famiglia parziale.
