# `/api/clips/render` — benchmark comparabile — 2026-11-27

## Stato

Misura **eseguita sul deploy live**, non sul working tree: il binario in ascolto
è il commit `df04e0e47669` (build `2026-11-27T08:49:14Z`, sha256
`9ba3ff166469…`), mentre il checkout è `c91fa8cae` con le modifiche CRA-03,
CRA-07 e CRA-08 non ancora distribuite. Questo artefatto fotografa quindi il
**deploy corrente**; non attribuisce alcun guadagno alle modifiche locali e non
pubblica percentuali di speedup.

- GPU: **NVIDIA RTX A4000 16 376 MiB**, driver `595.99.02`, compute capability 8.6.
- Host: Intel Xeon E3-12xx v2, 24 CPU, 29 GiB RAM.
- Lane GPU configurate: **2** (`renderinggen-queue`, lease 10m).
- Policy di verifica: **fast (1)**, `verification_passed=1` su tutte le misure.
- Payload: stessa costruzione di settembre (`plain` e `background`), 1920×1080,
  24/1 fps, `foreground_scale_percent=80`, destinazione
  `1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K`, `require_gpu=true`.
- Ambiente completo: [`clip-render-20261127/environment.json`](clip-render-20261127/environment.json).

## Perché non è confrontabile con settembre

La baseline certificata è `clip-render-final-20260911.md`. Il suo source
canonico (`yt_T6x-kDiQsWM_203_252_v1`) **non è più scaricabile**: il primo
tentativo con il payload di settembre è fallito con
`googleapi: Error 404: File not found` (evidenza:
[`clip-render-20261127/september-source-404.json`](clip-render-20261127/september-source-404.json)).
Il confronto riga per riga con settembre è quindi **non comparabile per
costruzione**: cambia la sorgente (49 s → 18 s) e la durata dei frame.

Per non perdere la misura, è stata usata una sorgente locale disponibile,
`yt_a9Lr_OQJ984_2097_2115_v1` (18,0 s, 432 frame, transcript `en` READY), e il
plate `classic1`. Questa è una **nuova baseline**, non uno speedup.

| Misura | Settembre 2026-09-11 | 2026-11-27 | Unità | Confronto |
|---|---:|---:|---|---|
| Sorgente | `yt_T6x-kDiQsWM_203_252_v1`, 49 s | `yt_a9Lr_OQJ984_2097_2115_v1`, 18 s | — | non comparabile |
| Frame | 1 176 | 432 | frame | non comparabile |
| Total job | 4 887 | 5 796 | ms | non comparabile |
| Render wall | 3 666 | 5 761 | ms | non comparabile |
| Render loop | 1 531 | 1 592 | ms | non comparabile |
| Render loop FPS | 767,9 | 271,4 | fps | non comparabile |
| Realtime factor | 26,4× | 3,11× | × | non comparabile |
| Fallback / readback | 0 / 0 | 0 / 0 | eventi | uguale |
| NV12→RGBA / RGBA→NV12 | 0 / 0 | 432 / 432 | frame | diverso (sorgente nuova) |

> La colonna «2026-11-27» è la riga **plain cold** qui sotto. Il `render_loop_ms`
> è misurato dal motore, non derivato dal wall.

## Cold — cache miss, lavoro GPU reale

Payload: [`plain-payload.json`](clip-render-20261127/plain-payload.json) e
[`background-payload.json`](clip-render-20261127/background-payload.json).
Entrambe le POST hanno risposto **202 QUEUED** con job id (nessuna cache hit).

| Percorso | Durata | Frame | Render wall | Chronon queue wait | Chronon service | Encode | Publish | Settle total | Render FPS | Realtime | Asset |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Plain | 18,0 s | 432 | 5 761 ms | 4 ms | 2 136 ms | 52 ms | 17 ms | 5 796 ms | 271,4 | 3,11× | 4 762 516 B |
| Background `classic1` | 18,0 s | 432 | 7 764 ms | 4 ms | 3 395 ms | 191 ms | 25 ms | 7 808 ms | 147,4 | 2,32× | 4 769 064 B |

Evidenze grezze:
[`cold-plain-child.json`](clip-render-20261127/cold-plain-child.json),
[`cold-background-child.json`](clip-render-20261127/cold-background-child.json).

Il `total_ms` è il wall del job **settle**; non è la latenza end-to-end della
POST. Il deploy non espone ancora `request_to_artifact_ms` (CRA-01 non è in
questo binario), quindi qui non viene rivendicata una misura end-to-end.

Il warm misurato vale per il binario `df04e0e`. Il working tree include CRA-03
con il bump `RendererLoweringVersion = cliprender-lowering-2026-11-27`: dopo il
deploy la cache parte fredda una volta, perché le righe precedenti non hanno il
digest degli input.

## Warm — cache hit, zero render GPU

Stessa POST, stessi byte, cache già popolata dalla misura cold.

| Percorso | HTTP | Wall handler | Stato | Render GPU | Fingerprint |
|---|---:|---:|---|---:|---|
| Plain | 200 | **8,7 ms** | `CACHED` | 0 | `c01a99ed8dc7…` |
| Background `classic1` | 200 | **12,5 ms** | `CACHED` | 0 | `cdb04511119f…` |

Entrambe le risposte non hanno `job_id` né child settle: nessun lavoro GPU,
nessun publish. Evidenze:
[`warm-plain.json`](clip-render-20261127/warm-plain.json),
[`warm-background.json`](clip-render-20261127/warm-background.json).

## Batch 5 clip e limite lane

Tutte le richieste sono cold (202 + job id), sorgente 18 s, background
`classic1`, stessa destinazione con subfolder univoca per forzare un fingerprint
nuovo. Il server è rimasto a **2 lane configurate**.

| Richieste concorrenti | Batch wall | Media totale | Throughput realtime | Σ render wall / batch wall | Queue wait per job | Esito |
|---:|---:|---:|---:|---:|---|---|
| 1 | 7,328 s | 18 s | 2,46× | 0,79 | 7 ms | 1 job, nessuna contesa |
| 2 | 11,419 s | 36 s | 3,15× | 1,54 | 5 / 3 874 ms | 2 lane occupate, il secondo attende |
| 4 | 17,938 s | 72 s | 4,01× | 2,50 | 5 / 7 462 / 10 473 / 3 574 ms | coda, nessun OOM |
| 5 | 24,513 s | 90 s | 3,67× | 3,15 | 4 569 / 48 / 16 382 / 9 654 / 13 539 ms | coda, nessun OOM |

Dettaglio del batch 5, una riga per richiesta (i valori vengono da
[`batch-5.json`](clip-render-20261127/batch-5.json); `render_wall` include
l'attesa in coda):

| Richiesta | Render wall | Chronon queue | Chronon service | Settle total | Render FPS | Esito |
|---:|---:|---:|---:|---:|---:|---|
| 0 | 12 854 ms | 4 569 ms | 4 499 ms | 12 953 ms | 103,9 | SUCCEEDED |
| 1 | 7 655 ms | 48 ms | 3 797 ms | 7 719 ms | 124,9 | SUCCEEDED |
| 2 | 21 764 ms | 16 382 ms | 2 771 ms | 21 803 ms | 179,1 | SUCCEEDED |
| 3 | 15 730 ms | 9 654 ms | 3 379 ms | 15 774 ms | 142,4 | SUCCEEDED |
| 4 | 19 124 ms | 13 539 ms | 3 115 ms | 19 180 ms | 154,1 | SUCCEEDED |

Evidenze: [`lane-1.json`](clip-render-20261127/lane-1.json),
[`lane-2.json`](clip-render-20261127/lane-2.json),
[`lane-4.json`](clip-render-20261127/lane-4.json),
[`batch-5.json`](clip-render-20261127/batch-5.json).

**Esito esplicito sul limite lane:**

- 1 e 2 richieste sono state misurate con la configurazione live a 2 lane.
- 4 e 5 richieste concorrenti sono state misurate **contro le stesse 2 lane**:
  il throughput non scala linearmente e la `chronon_queue_wait_ms` cresce fino
  a 10–16 s. Non è comparso OOM.
- La configurazione a **4 lane non è stata riverificata**: richiede il restart
  di RenderingGen con una config diversa, non eseguito in questa sessione. Il
  verdetto operativo resta quindi quello di settembre: **non alzare oltre 2
  lane** finché una prova dedicata a 4 lane non mostra un guadagno.

## CRA-08 — effetto su clip lunghe

Non misurato in questo artefatto: tutte le clip usate durano 18 s e il render
rientra nel budget di settle di 60 s, quindi non viene prodotto alcun deferral.
La misura del backoff breve richiede una clip con render >60 s e una coda
carica; resta un test live da fare, non un risultato di questo benchmark.

## Checklist CRA-02

- [x] Nuovo `ops/benchmarks/clip-render-20261127.md` con commit e hardware dichiarati.
- [x] Plain e batch 5 clip riportati con colonne e unità esplicite; il confronto
      con settembre è dichiarato **non comparabile** perché la sorgente
      canonica è 404.
- [x] Cold e warm separati; il warm dichiara zero render GPU.
- [x] Nessuna percentuale di speedup pubblicata: solo numeri misurati e rapporti
      calcolati dai due numeri.
- [x] Limite lane: 1/2 misurati sulla config live; 4/5 concorrenti misurati
      contro 2 lane; 4 lane configurate **non** riverificate (limite dichiarato).
- [x] JSON/metriche grezze allegati nella directory `clip-render-20261127/`.

## Riproduzione

```sh
# payload e ambiente
cat ops/benchmarks/clip-render-20261127/plain-payload.json
cat ops/benchmarks/clip-render-20261127/background-payload.json
cat ops/benchmarks/clip-render-20261127/environment.json

# POST (admin token richiesto), poi GET /api/jobs/<id> fino a SUCCEEDED
curl -X POST http://127.0.0.1:8000/api/clips/render \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  --data @ops/benchmarks/clip-render-20261127/plain-payload.json
```

Il primo POST restituisce il job **submit**; il child `settle` è in
`result.child_job_id` e contiene `result.render.metrics_v2`. Il warm si misura
ripetendo lo stesso POST: deve rispondere `200 CACHED` senza `job_id`.
