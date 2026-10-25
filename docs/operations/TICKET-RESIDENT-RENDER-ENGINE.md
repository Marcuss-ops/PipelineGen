# TICKET-RESIDENT-RENDER-ENGINE (P2)

**Stato:** PARTIAL (25/10/2026) — downscale/cache immagini e test di identità presenti; la sessione encoder cross-item non è certificabile e resta disattivata.
**Origine:** idee di paradigma di `TICKET-OVERLAY-THROUGHPUT-100-MIN.md`,
approvate dall'utente il 24/10/2026. Unifica le fix 4/5/6 della
profilazione per-item (23/10) in un unico ciclo di vita residente.
**Ordine consigliato:** dopo P1, prima della misura golden live.

## Principio

Il daemon Chronon è un motore residente (paradigma game-engine): atlas MTSDF
in VRAM, immagini già ridotte a canvas e caricate in una LRU content-addressed
in VRAM, pool caldi, sessione encoder aperta. Il job arriva come **delta di
scena**, non come progetto da capo.

## Problema misurato

Costi per-item che un motore residente pagherebbe una volta (dati
`TICKET-PERFORMANCE-AUDIT-2026-10.md` §4-5 e golden v6/v17):

| Voce | Costo per item | Natura |
| --- | --- | --- |
| startup render | ~186 ms | fisso, ripetuto per item |
| `encoder_open_nvenc` + `encode_close` | ~259 ms | ciclo vita encoder per artefatto |
| decode + promozione GPU immagini a risoluzione sorgente | ~300 MB/item (8 upload, 242 MB byte immagini) | lavoro unico-per-item, è la radice reale del «font_preflight» (1.862 ms = 1.823 ms `asset_decode`) |
| pool warmup | ~65 ms | chiuso da fix 4(b) (top-up-only) |

Le fix 4/5/6 sono oggi pezzi separati e incompleti; la proiezione
«prepare −310 ms/item» è stata ri-attribuita: il residuo è il decode di
immagini a risoluzione sorgente, non i font.

## Cambio

1. **Texture residenti canvas-sized:** downscale a canvas al decode, LRU
   content-addressed in VRAM (`GpuAssetCache` estesa), eviction sotto budget
   `vramprobe`. Unifica fix 6 + fix 4.
2. **Sessione encoder cross-item:** una sessione di encode sopravvive a più
   item (open/close una volta per sessione, non per artefatto). Unifica fix 5.
3. **Warmup cross-job:** pool/atlas/font già caldi alla ricezione del job
   (il `FullGraphSession` per-device già sopravvive ai job; estendere il
   concetto alle risorse immagine e alla sessione encoder).

## Gate di accettazione

- [ ] Overhead fisso per-item ≤ 100 ms su 100 item consecutivi (somma
      startup + prepare non-asset + open/close encoder dal sidecar).
- [ ] Parità pixel/audio con golden (le lane certificate impongono parità
      byte Vulkan↔software; il downscale richiede re-baseline dei golden
      pixel-oracle dichiarato e rivisto).
- [ ] Picco VRAM dentro il budget certificato (evidenza oggi su 2 daemon;
      `vramprobe` per 3 lane dal 23/10).

## Dipendenze e rischi

### Verifica closeout 2026-10-25

- **PASS locale (CTest mirato 25/10):** decode display-bounded, chiave
  distinta per bounds e GPU content digest sono coperti da
  `test_image_cache_display_bound_downscale.cpp`.
- **PARTIAL / NOT RUN:** il test di riuso encoder ha riprodotto
  `avcodec_send_frame: End of file` dopo la finalizzazione del primo MP4.
  `avcodec_flush_buffers()` non riapre un encoder drenato. Per evitare artefatti
  corrotti, `codec_session_reuse_enabled()` ora fallisce chiuso e i tre daemon
  sono stati riavviati senza il drop-in che attivava il flag.
- **NOT RUN:** gate ≤100 ms su 100 item, picco VRAM sul golden completo e parità
  pixel/audio su 100 item. Il codice di riuso non va riattivato finché il writer
  batch non possiede un unico encoder non drenato e confini MP4 verificati.

- Vincolo VRAM: è il limite che ha imposto la serializzazione dei job render.
- Il downscale non è byte-preserving: serve il re-baseline dei golden con
  gate di parità aggiornati (stessa exit condition del fix 6 nella
  profilazione per-item).
- Sessione encoder cross-item cambia il ciclo di vita del muxer: uscita da un
  item non deve invalidare gli artefatti già certificati.
