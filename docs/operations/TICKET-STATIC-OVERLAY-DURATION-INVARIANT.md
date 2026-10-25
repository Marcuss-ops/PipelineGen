# TICKET-STATIC-OVERLAY-DURATION-INVARIANT (P1)

**Stato:** PASS LIMITATO (25/10/2026) — nel preset statico `IMPORTANT_PHRASE` / `static_text_smoke`, backend Vulkan/NVENC, la run finale ha rapporto live 0,684× e parità pixel 720/720. Non certifica altri preset, classi di overlay o backend; il ticket è chiuso solo per questo scope.
**Origine:** idee di paradigma di `TICKET-OVERLAY-THROUGHPUT-100-MIN.md`
(sezione «Idee di paradigma»), approvate dall'utente il 24/10/2026.
**Ordine consigliato:** dopo B2 (`Chronon3d/docs/tickets/TICKET-100X-PERF-WAVES.md`),
prima di P2.

## Principio

Obiettivo architetturale: ridurre il costo marginale della durata quando
l'overlay resta visivamente statico. Il gate attualmente certificato è la
misura 30 s / 3 s e la parità pixel del preset dedicato; non dimostra da sola
O(1) asintotico né una sola codifica per tutti i frame.

## Problema misurato

- **Problema storico, pre-fast-path:** una phrase card statica da 30 s produceva
  900 frame identici valutati/rasterizzati/compositi/encodati uno per uno; il
  servizio scalava linearmente con la durata anche quando
  `template_reuses = frames - 1`. La run conclusiva sotto misura il fast path
  introdotto per il preset dedicato.
- Nel golden v17 `text_phrase` era 20/40 item e **45,2% del servizio render**
  (avg 2,266 s su clip brevi); dati pre-fix, non misura del preset certificato
  corrente. Card lunghe, mappe e schede entità restano classi da validare.
- **Baseline storica pre-fix:** `DirectYuvProgram` cacheava il
  `DirectYuvTemplate` su $[cue\_from, cue\_until)$, ma il loop emetteva comunque
  un frame renderizzato+encodato per tick. La run conclusiva sotto prova il
  fast path solo per il preset statico indicato; non estendere la conclusione
  a tutti i template o backend.

## Cambio

1. Riconoscere item statici per l'intera durata: composizione con
   `Animation == nil`, text/style/position costanti (stessa detection degli
   intervalli statici già usata da `DirectYuvProgram`).
2. Sul percorso statico: renderizzare il template **una volta**, poi replicare
   il risultato. Due varianti candidate, scelte dalla misura:
   - **(a) segmento encodato + stream-copy:** encodare il primo segmento
     statico (pochi secondi) e replicare i pacchetti con concat stream-copy
     fino alla durata richiesta; costo O(1) nella durata, struttura GOP
     ripetuta.
   - **(b) feed della stessa superficie all'encoder:** nessuna ri-composizione
     per frame, resta un encode per frame (costo lineare ridotto, non O(1)).
3. Parità pixel: contenuto statico ⇒ ogni frame decodificato deve essere
   identico al golden (stesso encoder, stessi parametri); la variante (a)
   cambia la struttura GOP dell'MP4 → **verificare prima il contratto di
   stitching del final worker (packet-copy)**.

## Gate di accettazione

- [x] Sul preset statico certificato `IMPORTANT_PHRASE` / `static_text_smoke`,
      la run live misura 30 s / 3 s = 0,684× (soglia ≤1,2×).
- [x] Parità pixel sul caso certificato: 720/720 frame decodificati uguali;
      tre output ripetuti byte-identici.
- [x] Contratto media verificato sul caso certificato: MP4 H.264, durata e
      numero frame attesi; non equivale a copertura di tutti i preset.
- [ ] Estendere la certificazione agli altri preset statici e ai backend non
      coperti prima di fare un claim generale O(1) per la durata.

## Dipendenze e rischi

- Detection statica: disponibile (B1); l'affermazione assoluta sulla
  ricomposizione per tick qui sopra descrive la baseline storica. Il fast path
  finale è provato soltanto per il preset statico certificato.
- L'implementazione live usa il fast path di packet duplicati/anchor; non
  afferma che il segmento venga encoded una sola volta in modo O(1) assoluto.
  Il gate certifica il rapporto di servizio misurato (30 s costa meno del 3 s)
  e pixel parity sul caso, non una complessità asintotica per ogni input.
- Variante (a): dipende dal contratto con il final worker (packet-copy stitch)
  — negoziare prima di adottare.
- Interazione con `TextAnimators`/word-reveal: restano sul percorso per-frame
  (genuinamente animati), come già previsto dalla wave B1.

### Verifica closeout 2026-10-25

#### Run conclusiva dopo anchor packet fix

- Build Chronon3d release aggiornata PASS; test mirato
  `ExecutionResolver preserves proven packet duplicates on NV12 output` PASS
  (1 caso, 6 assertion).
- Test live sul worker RenderingGen aggiornato, preset statico e prefisso
  `freebuff-p1keyrun-20261025T1057Z`: mediane `render_ms` 3 s
  **1810 ms** e 30 s **1238 ms**, rapporto **0,684×** (limite ≤1,2×).
- Parità pixel: **720/720 frame** decodificati uguali. Tre MP4 da 30 s
  ripetute byte-identiche, SHA-256
  `bbc4b21e572637e1ae1a0302e73545e93bfe3058793e793d88b6669c3f3fa2f2`.
- Sidecar: 719 frame riusati via fast path, un frame NVENC, 720 timestamp.
  Il gate P1 è quindi PASS sul preset e sul percorso Vulkan/NVENC misurati.
  Le prove fallite precedenti sotto riportate restano storia diagnostica e sono
  superate da questo run; non estendono la certificazione ad altri preset.

#### Prove diagnostiche precedenti (superate dal run conclusivo sopra)

- Build e regressione C++ mirata PASS; il test encoder conferma che il riuso
  dopo finalizzazione MP4 resta disabilitato (EOF dal codec).
- Il primo canary era mal configurato: `classic_phrase` dichiara un moto
  `classic_playfair_baseline_rise` di 72 frame. Il canary corretto usa
  `IMPORTANT_PHRASE` + `static_text_smoke`, senza `MotionID` né parametri animati.
- A/B live corretto, tre repliche, backend `vulkan`, codec H.264: mediana 3 s
  **1083 ms** ([1173, 1083, 1029]); mediana 30 s **3715 ms**
  ([3750, 3674, 3715]); rapporto **3,430×**, quindi sopra la soglia 1,2×.
- Parità pixel FAIL: frame decodificato 1 diverso dal frame 0. I tre artefatti
  da 30 s sono byte-identici tra loro, SHA-256
  `31846992021f6657dc4ff19c9ef9b375e2a718286c14973573028c3699b36adf`.
  `ffprobe` mostra il frame 0 come I e il frame 1 come P; il comportamento live
  non corrisponde all'anchor intra atteso. Anche col worker aggiornato (entry/exit
  disabilitato per il preset statico) il rapporto live è 3,279× e il pixel frame
  1 differisce: quella prova era FAIL e lasciava P1 aperto sul percorso
  operativo Vulkan/NVENC. È superata dal run conclusivo sopra soltanto per il
  preset certificato; resta valida come diagnostica storica, non come stato
  corrente né come certificazione degli altri preset.

#### Inventario copertura e riesecuzione locale — 25/10/2026

- Inventario catalogo RenderingGen: **11 preset ufficiali** — tre text
  (`static_text_smoke`, `phrase_default`, `classic_phrase`) e otto image. La
  presenza in catalogo e i test di materializzazione non provano che preset
  animati o image siano statici per la durata; oltre al preset smoke non è
  stato trovato un secondo preset statico qualificato e certificato in questo
  checkout.
- Il test Chronon `P1 static run replicates the anchor packet with exact pixel
  parity` esercita 24 frame constant-input con libx264 nel software pipe; il
  test `ExecutionResolver preserves proven packet duplicates on NV12 output`
  verifica il contratto di routing NV12. Sono regressioni deterministiche,
  non misure di servizio, Vulkan/NVENC live o A/B 3 s/30 s.
- In questo checkpoint il preset CMake `linux-video-test` è stato configurato
  con successo, ma il build mirato è scaduto a 600 s senza produrre i binari
  cache/render-graph/media-video. Pertanto i due test Chronon sopra non sono
  stati rieseguiti ora (**NOT RUN**); i risultati della run live 25/10 alle
  10:57 rimangono l'unica prova di durata e pixel parità per il backend
  certificato.
- Estendere la certificazione agli altri preset statici e ai backend/software
  o NVIDIA aggiuntivi richiede fixture esplicite e prove equivalenti; non
  generalizzare il PASS corrente.

#### Drain CUDA prima del frame duplicato — 25/10/2026

- Il golden `after-cpp-live-20261025T135536Z` ha esposto un race di sequenza:
  `duplicate frame 30 breaks the presentation cursor 28`. Il writer aspettava
  solo il primo frame CUDA pendente; gli altri potevano rimanere in flight.
- La correzione locale ripete il drain sincrono fino a svuotare la coda CUDA,
  prima di convalidare il cursore del frame duplicato.
- Build `linux-video-release --target chronon3d_cli -j12` PASS; il test
  `P1 static run replicates the anchor packet with exact pixel parity` passa
  nel binario `linux-video-test` (1 caso, 60 assertion).
- Il fix non è ancora caricato sui daemon e non è verificato su Vulkan/NVENC:
  P1 resta certificato solo nello scope live precedente; la regressione CUDA
  attende restart e rerun golden.

#### Verifica del fix sul live golden — 25/10/2026

- La build release con il fix è stata caricata sui tre daemon; SHA-256 del
  binario attivo `0fb96618bc1c0cda80e1f26c96fd991381145b4ed378f3697fe33127f1255817`.
- Il test software P1 passa ancora (1 caso, 60 assertion). Il golden NVENC
  diagnostico e un rerun pulito hanno completato 74/74 overlay; la prima run
  pulita, intermedia, si è fermata al batch item 010 con exit code 1 senza
  causa Chronon specifica nel dettaglio IPC. Il fix passa quindi una run live
  pulita, ma l'anomalia resta da chiarire prima di dichiarare affidabilità
  ripetibile; non generalizzare la prova al mix completo da 100 item.
