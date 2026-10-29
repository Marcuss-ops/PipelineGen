# Benchmark P4 packed superjob — capacità sustained reale (2026-10-29)

Obiettivo: **misurare** (non stimare) la capacità sustained del percorso P4
«un piano = una sessione render, N MP4 per item» sul motore live, e chiudere
le voci ancora NOT RUN del `TICKET-BATCH-SUPERJOB`:

| Voce ticket | Come la copre il benchmark |
|---|---|
| Live N-MP4 (una sessione, N per-item MP4 + mirror) | R1 prove engine + verifiche attivazione R2 |
| Parità per segmento contro render indipendenti | R1 (`TestSegmentedSessionMatchesIndependentSingleItemRenders`) |
| Retry indotto live | R1 (`TestFinalizeSegmentsLiveInducedRetryRendersOnlyTheFailedSegment`) |
| ≤1,1 s/item sul percorso segmentato | R1 misura diretta `P4 service cost` + servizio per item R2 |

## Esito tentativo 1 (2026-10-29) — interrotto prima della misura

Primo tentativo di esecuzione: **chiuso senza alcun numero prodotto**. Cronologia
e decisioni in `results/attempt-1-interrupted.md`. In sintesi:

- 13:35 build esterna `-j12` finita; 13:39 il driver attivo con precheck PASS
  (binario release = stesso profilo dei 3 daemon).
- 13:47 la finestra si chiude da fonti esterne alla sessione (`gitleaks` +
  rebuild Chronon3d): driver fermato immediatamente (solo processi propri).
- 14:00 nuova ondata di build parallela (load1 32,8, 22 compilatori): nessuna
  run avviabile. R1/R2 NON eseguite.
- Provenienza binario al rilancio: `linux-video-release` (binario ricostruito
  13:35:13; i daemon potrebbero girare sull'immagine precedente — al rilancio
  con `systemctl show -p ExecStart` + `stat` del binario va deciso se serve un
  restart dei daemon).

Le regole di misura (contratto «nessun altro carico pesante») restano invariate;
questa sezione è solo il registro del tentativo.

## Come lanciare

```bash
cd refactored
ops/jobs/p4_superjob_sustained_20261029/run_p4_benchmark.sh 3 p4-sustained
```

Il driver è una sequenza live: apre daemon (R1) e sottomette job reali (R2).
Va eseguito **solo** nella finestra autorizzata. Precheck bloccanti: 3 daemon
`chronon3d`/`-b`/`-c` attivi, nessun build concorrente, ffmpeg presente,
binario Chronon3d individuabile (`CHRONON_BINARY` vince sul default del
pacchetto).

Il driver NON attende da solo che la finestra si liberi: il precheck interrompe
immediatamente se la macchina è occupata. Le **guardie di attesa** (verificate
prima del lancio, e per tutto il tempo della catena R2):

```bash
# Guardia ripetibile: load1 < 2,5 E nessun compilatore attivo
for i in $(seq 0 55); do
  BUSY=$(pgrep -c -f '[c]c1plus|[n]inja -j|[g]itleaks' 2>/dev/null || true)
  LOAD1=$(awk '{print $1}' /proc/loadavg)
  [ "${BUSY:-0}" = "0" ] && awk -v l="$LOAD1" 'BEGIN{exit !(l < 2.5)}' && { echo QUIET; break; }
  sleep 10
done
```

Carichi validi come escludenti (storico 29/10): `gitleaks detect --source .`
(~14 core), `ninja -j12 chronon3d_cli` e il rebuild Chronon3d (fino a 22
processi compilatori, load 1-min 32,8 alle 14:00).
Attenzione al decay del loadavg: dopo una build locale il load1 può scendere
sotto 2,5 mentre gli effetti su cache/gcode restano. Per il contratto di
misura la finestra è davvero libera solo con anche load5 < 3.

## R1 — prove engine (binario live, gateway synthetic)

- `TestDaemonSegmentedServiceCostIsBelowPerItemSessions`: una sessione
  segmentata (1 startup, 1 encoder, N per-item MP4) contro N sessioni singole
  dello stesso contenuto, log `P4 service cost: segmented X ms/item vs Y
  ms/item`. Direzione attesa X < Y; il numero NON è goldensummary né
  throughput: è il costo di servizio per item del percorso segmentato sul
  piano sintetico (320×180, floor 3 frame/item).
- `TestSegmentedSessionMatchesIndependentSingleItemRenders`: parità
  frame-per-frame dopo decode YUV tra segmento e render indipendente.
- `TestFinalizeSegmentsLiveInducedRetryRendersOnlyTheFailedSegment`: il retry
  indotto tocca solo il segmento fallito.

Evidenze: `results/probe-engine-service-cost.log`,
`results/probe-parity-retry.log`, estratto `results/probe-service-cost.txt`.

## R2 — catena e2e golden nomaps (payload v1-nomaps, senza mappe)

N ripetizioni (default 3) di `run_golden_nomaps.sh <label>` con monitor di
carico (`load_monitor.tsv` per run; un campione con build attivo durante la
finestra invalida la run come capacità). Per ogni run riuscita il driver:

1. estrae il job_id dal `submit.log`,
2. cerca nel journal dei worker la riga `P4 segmented output` riferita al
   job → `p4-activation-<label>.log`. **La riga assente = percorso fused
   manifest: la run NON vale come rung P4** (l'attivazione del P4 è
   strutturale, non un flag: finestra item che tilano la canvas, render
   whole-plan, nessun audio muxed, non finale, lane native che dichiara
   `SupportsOutputSegments`),
3. genera `queue-service-<label>.tsv` da `run_operation_observations`
   (avg duration / queue_wait per operazione chronon/render).

Summary per-run con `goldensummary`: `ops/jobs/overlay_throughput_golden_100_20261022/results/<label>/summary.json`.

## Criteri d'accettazione

1. **R1:** service cost segmentato < sessioni singole (direzione), parità
   pixel PASS, retry indotto PASS. Servizio per item segmentato registrato
   come stima superiore del costo engine.
2. **R2:** mediana di 3 run riuscite con attivazione P4 verificata:
   - item/min sulla finestra overlay (indicatore di confronto con la mediana
     A5 49,5 item/min di §9.5 del `TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE`),
   - servizio per item batch (target ticket ≤1,1 s),
   - `queue_wait_ms` medio per batch (se scende servendo le stesse lane, il
     residuo stringe al costo di servizio/frames, conferma o falsa §9.7).

## Caveat di comparabilità (obbligatori nella narrazione dell'esito)

- Le run R2 usano il payload v1-nomaps e2b: confronto valido solo con le run
  dello stesso payload/binario (`split-per-scene-20261029-r1`, r2/r3/r4 di §9.5).
- Il numero di item materializzati è 65–70 per run (dipende dai provider
  live): il throughput va letto per run e per mediana, mai idealizzato a 100.
- «P4 attivo» va dimostrato per OGNI run con la riga di journal; senza riga,
  la run misurò il fused manifest con batching fused: numero valido ma
  diverso contratto.
- Le run R1 sono floor sintetici (320×180): non estrapolarli a item/min e2e.

## Rischi noti e mitigazioni già coperte

- Il flap «unit attive» va rivisto se viene cambiata topologia lane
  (es. `gpu_lanes=4`): in tal caso le run NON sono confrontabili con la
  serie workers=3 di §9.5.
- Conflitto con fasi generate/TTS: R2 misura la finestra overlay; wall job
  completo resta utile ma non è il primario del rung P4.
