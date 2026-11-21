# RA-15 — Logica media dentro `internal/app/wiring`

**Data:** 2026-11-21
**Priorità:** P1 (composition root con logica di dominio; aiuta RA-02)
**Stato:** OPEN — non avviato: richiede una sessione dedicata (piano e ordine in `00-INDEX.md` §5; §8 per il blocco)
**Owner:** composition root + platform/media
**Origine:** report esterno 2026-11-21 §1.2; verificato sul codice
**Verifica sul codice:** **CONFERMATO** — `final_job_segment_prepare.go` è **565 righe**;
gli altri due file citati esistono nello stesso package

## 1. Problema

`internal/app/wiring` dovrebbe essere una composition root: costruire oggetti e collegarli.
Invece contiene un'intera pipeline media:

- `internal/app/wiring/final_job_segment_prepare.go` — allineamento GOP, padding frame,
  invocazioni ffmpeg, caching, content-addressing;
- `internal/app/wiring/final_job_fragment_pipeline.go`;
- `internal/app/wiring/final_job_placement.go` — formule frame→µs con FPS fisso (riga 168).

Conseguenze:
1. La pipeline non è testabile senza costruire il wiring completo.
2. Il dominio media è duplicato: le stesse formule e gli stessi passi esistono anche in
   `capabilities/scripts` (vedi RA-16 sulle formule frame→µs replicate).
3. Questi file contano nel cap hotspot di RA-02: spostarli è anche la via più naturale per
   rientrare da 72 a ≤70.

## 2. Soluzione

1. **Classificare ogni funzione** del file in tre gruppi:
   - **policy** (quali finestre, quale GOP, quale profilo, quando copiare vs encodare) →
     `internal/capabilities/scripts/finaljob` (o `kernel/media` se è geometria pura);
   - **meccanica** (ffmpeg/ffprobe, I/O file, cache su disco, hashing) →
     `internal/platform/media/finaljob` come adapter;
   - **wiring** (costruzione, injection, scelta dell'implementazione) → resta in
     `app/wiring`, ma solo poche righe.
2. **Spostare la meccanica** in `platform/media/finaljob` dietro una porta già esistente o
   nuova (`SegmentPreparer interface{ Prepare(ctx, plan) (Result, error) }`). Il platform può
   usare `exec.CommandContext` e `ffprobe`; il wiring no.
3. **Deduplicare le formule** frame→µs: una sola funzione in `kernel/media` che usa le
   costanti `CanonicalFPSNum/Den` di `kernel/media/assembly_contract.go` (già esistenti), e
   tre chiamanti che la usano. Vedi RA-16 §2.4.
4. **Ridurre il file**: dopo lo spostamento, `final_job_segment_prepare.go` deve contenere solo
   la costruzione dell'adapter. Se resta >150 righe, la policy non è stata separata.
5. **Chiudere il buco con un percheck**: `internal/app/**` non deve invocare
   `exec.Command*`, `os.Open/ReadFile` su media, o contenere costanti di geometria
   (`1920`, `1080`, `24`, `GOP`). Allowlist iniziale = i file attuali, ratchet verso il basso.
6. **Test**: i test della pipeline si spostano con il codice (stesso package); il wiring ha un
   solo test di costruzione. Nessun test deve richiedere il wiring completo per verificare
   l'allineamento GOP.

## 3. Check finali

```bash
cd refactored
# 1. niente esecuzione di processi nel composition root
grep -rn "exec.Command\|ffmpeg\|ffprobe" --include=*.go internal/app/wiring/ | grep -v _test
# atteso: nessun output (o solo costanti/documentazione ammesse dall'allowlist)

# 2. niente geometria hardcoded
grep -rn "1920\|1080\|GOP\|gop" --include=*.go internal/app/wiring/*.go | grep -v _test
# atteso: nessun literal di geometria; solo costanti del kernel

# 3. i file sono scesi sotto soglia e il conteggio hotspot è migliorato
wc -l internal/app/wiring/final_job_segment_prepare.go internal/app/wiring/final_job_fragment_pipeline.go \
      internal/app/wiring/final_job_placement.go
find internal/app/wiring -maxdepth 1 -name '*.go' ! -name '*_test.go' | wc -l
# atteso: file <= 150 righe ciascuno dove resta il solo wiring; conteggio <= 70

# 4. la pipeline è testabile senza wiring
go test ./internal/platform/media/finaljob/... ./internal/capabilities/scripts/finaljob/...
# atteso: PASS

# 5. comportamento invariato (golden/parity)
go test ./internal/app/wiring/ -run 'FinalJob|SegmentPrepare' -v
# atteso: PASS; nessuna differenza nei byte prodotti (test di parità o hash)

# 6. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: spostare codice che dipende da variabili di wiring (path, config) richiede
  iniettare la config nell'adapter. Mitigazione: parametri espliciti, nessun `os.Getenv` nel
  platform (la config arriva dal wiring).
- **Rischio**: la pipeline è su un percorso caldo (final job); un cambio di ordine delle
  operazioni cambia i tempi. Mitigazione: spostamento verbatim, nessuna ottimizzazione in
  questo ticket (le perf hanno le loro schede).
- **Rollback**: revert; RA-02 resta aperto ma la baseline non peggiora.
