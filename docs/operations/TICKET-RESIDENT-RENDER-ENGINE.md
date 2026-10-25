# TICKET-RESIDENT-RENDER-ENGINE (P2)

**Stato:** PARTIAL (25/10/2026) — fix locali di cache/prepare, downscale display-bound e test presenti; riuso encoder cross-job non implementato/attivo e non certificato. Non confondere il writer multi-segmento P4 (sessione entro un piano, più MP4) con una sessione residente cross-job.
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

Le fix 4/5/6 sono pezzi separati e non equivalgono al motore residente
completo. La proiezione «prepare −310 ms/item» è stata ri-attribuita: il
residuo è il decode di immagini a risoluzione sorgente, non i font; non è un
guadagno misurato sul percorso residente.

## Cambio

1. **Texture residenti canvas-sized:** downscale al decode display-bound e
   cache content-addressed hanno building block e test locali; LRU GPU con
   eviction sotto budget `vramprobe` va verificata nel percorso golden. Unifica
   fix 6 + parte del fix 4.
2. **Sessione encoder cross-item/cross-job:** obiettivo non implementato nel
   percorso produttivo; una sessione non deve essere riusata dopo EOF senza
   contratto e certificazione del muxer. Il writer segmentato P4 ha scope
   intra-piano e non chiude questo requisito. Unifica fix 5.
3. **Warmup cross-job:** pool/atlas/font e risorse immagine calde tra job;
   `FullGraphSession` per-device sopravvive già ai job, ma il comportamento
   richiesto dal motore residente va misurato end-to-end.

### Verifica mirata — 25/10/2026

- I test Chronon per downscale display-bound e identità ImageCache citati nel
  closeout erano prove precedenti; in questo checkpoint non è stato possibile
  ricostruire il binario C++ `linux-video-test` per rieseguirli. Stato di questa
  sessione: **NOT RUN** (non retroclassificare come nuova verifica).
- I test Go P4 sul writer intra-piano passano nel modulo RenderingGen, ma non
  implementano né attestano il riuso encoder fra job. Il codec resta drained
  dopo EOF; P2 cross-job è ancora **NOT IMPLEMENTED**.
- La suite mirata di pipeline è mista: i test di batching/manifest/trace
  selezionati passano; la suite larga scripts+overlays+observability fallisce
  su cinque regressioni di fixture/registry già visibili nel working tree
  (builder, editorial manifest, provider Artlist, `esri_light`). Questi FAIL
  non forniscono evidenza per il gate P2.
- Gate ≤100 ms/100 item, parità pixel/audio sul golden residente e picco VRAM
  del mix da 100 item restano **NOT RUN**; richiedono percorso residente
  cross-job, build coerente e finestra di misura sul golden.

## Gate di accettazione

- [ ] Overhead fisso per-item ≤ 100 ms su 100 item consecutivi, misurato sul
      percorso residente cross-job. NOT RUN: i dati storici v22–v27 misurano
      ~465 ms/item (oltre soglia), ma non esercitano il percorso residente;
      non usarli come misura della nuova architettura.
- [ ] Parità pixel/audio con golden sul percorso residente; downscale e
      riuso encoder richiedono confronto sul golden e re-baseline dichiarato.
- [ ] Picco VRAM entro il budget sul mix completo da 100 item e sul percorso
      residente. L'evidenza di 2 daemon non costituisce questa misura.

## Dipendenze e rischi

### Verifica closeout 2026-10-25

- **PASS locale (25/10):** i quattro casi in
  `test_image_cache_display_bound_downscale.cpp` passano nel binario test
  corrente (**4/4 casi, 32/32 assertion**): decode ridotto ai bounds,
  conservazione della media premultiplied-linear, nessun upscale e chiave/
  digest GPU distinti per rappresentazione. È una prova del building block,
  non del gate throughput/VRAM di P2.
- **PASS locale, non certificazione P2:** la cache/prepare e il downscale
  display-bound hanno i test indicati nel closeout; l'impatto byte/pixel e
  VRAM sul mix golden non è misurato dal test unitario.
- **NOT IMPLEMENTED / NOT RUN:** il riuso encoder cross-job non è attivo: dopo
  EOF il codec è drenato e `avcodec_flush_buffers()` non lo riapre. Non
  riattivare quel percorso senza un nuovo contratto e test dedicati.
- **P4 separato:** il writer segmentato tiene un encoder non drenato entro un
  singolo piano, invia EOF una volta e instrada packet in MP4 distinti. I test
  P4 locali non dimostrano una sessione residente tra job, né un risparmio di
  overhead P2.
- **NOT RUN:** gate ≤100 ms su 100 item, picco VRAM sul golden completo e parità
  pixel/audio su 100 item. Il writer batch non sostituisce questi gate; servono
  misura e certificazione live sul mix golden.

- Vincolo VRAM: è il limite che ha imposto la serializzazione dei job render.
- Il downscale non è byte-preserving: serve un re-baseline dei golden con
  gate di parità aggiornati prima di abilitarlo come percorso certificato.
- Sessione encoder cross-item cambia il ciclo di vita del muxer: uscita da un
  item non deve invalidare gli artefatti già certificati.
