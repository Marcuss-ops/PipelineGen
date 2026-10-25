# TICKET-BATCH-SUPERJOB (P4)

**Stato:** IMPLEMENTAZIONE LOCALE PASS / LIVE PARTIAL (25/10/2026) — writer segmentato, finalizzazione/receipt per segmento e retry isolato sono collegati nel working tree e coperti da test. Gate A live passa soltanto per il piano fused a singolo MP4. Mancano certificazione live N-MP4, parità per segmento sul golden, retry indotto live e misura ≤1,1 s/item sul percorso segmentato.
**Origine:** idee di paradigma di `TICKET-OVERLAY-THROUGHPUT-100-MIN.md`
(paradigma #1 «un piano = una sessione render»), approvate dall'utente il
24/10/2026. Complementare a `TICKET-RESIDENT-RENDER-ENGINE` (P2).
**Ordine consigliato:** dopo la raccolta delle trace P3; può essere valutato
come leva per il target throughput, ma il suo contributo al 100/min non è
ancora misurato né va dedotto dai proxy gate A.

## Principio

Un piano = una sessione render: gli item di un piano (o di una scena) sono
**segmenti di una sola sessione** — uno startup, una sessione encoder, un
ciclo di receipt per batch — invece di un job per item. Il costo fisso è
pagato una volta per batch, non una volta per artefatto.

## Problema misurato

- Evidenza storica v17: servizio medio per item **2,509 s** (p50 2,309,
  max 5,362); nel golden v22–v27 il gate di servizio P4 resta a 1,603–1,826
  s/item fused, oltre il limite ≤1,1 s.
- L'overhead fisso storico v22–v27 è ~465 ms/item; è un dato del percorso
  item-singolo, non una misura del writer segmentato.
- La proiezione render-loop+marginali (~1,1 s/item, ~110/min su due lane) è
  un'ipotesi di progetto, non un risultato del batch. Non dichiarare capacità
  o guadagno prima del gate live segmentato.

## Cambio

1. Il worker RenderingGen presenta N item come un piano-batch; il daemon
   Chronon apre una sessione (device, pipeline, encoder) e produce N
   segmenti MP4 (uno per item, contratto preservato).
2. Un ciclo di verifica per batch: probe/digest per segmento, receipt per
   batch + voci per segmento.
3. Retry idempotente per segmento: un segmento fallito non invalida la
   sessione né gli altri segmenti (checkpoint per segmento).

## Gate di accettazione

- [x] Gate A proxy: rapporto live 0,291 sul piano fused, singolo MP4, prima
      del writer segmentato. È una misura del proxy e non del percorso N-MP4.
- [ ] Servizio medio per item ≤ 1,1 s su piano golden segmentato: NOT RUN.
- [ ] N MP4 distinti, receipt identity univoche, frame window e durata corretti:
      test locali PASS; E2E live NOT RUN.
- [ ] Parità pixel/audio per segmento contro render indipendenti sul golden:
      test locali di packet/pixel e ordine PASS; confronto live NOT RUN.
- [x] Retry isolato locale: test Chronon `RENDER_BATCH` 1/20 e RenderingGen
      `TestFinalizeSegmentsRetriesOnlyTheFailedSegment` PASS.
- [ ] Retry indotto nel percorso live N-MP4, con prova che gli altri segmenti
      non vengano rielaborati: NOT RUN.

## Evidenza e rischi

### Verifica closeout 2026-10-25

- **Misura live del proxy Gate A (25/10):** prefisso
  `freebuff-p4batch-20261025T1100Z`; dieci job singoli `[945, 871, 878, 850,
  938, 881, 849, 893, 1183, 945]` ms, mediana **887 ms**. Piano fused unico
  da dieci overlay: **2583 ms totali / 258 ms per item**, rapporto **0,291**
  contro il limite 0,40. La run genera un solo MP4 e precede l'integrazione
  del data plane segmentato: prova esclusivamente il proxy Gate A.
- La prova live produce un solo MP4 per il piano e non attraversa il contratto
  di output segmentati; quindi non certifica N MP4 distinti, verifica per
  segmento, retry idempotente, né il gate medio ≤1,1 s sul contratto finale.
  Il writer è stato collegato al worker nel working tree dopo la misura: stato
  live P4 **PARTIAL** fino alla prova aggiornata.

- **Misura storica / proxy locale:** `TestBatchTenServiceCostAB` misura il
  servizio di un piano con dieci overlay sequenziali rispetto a dieci piani
  singoli. Non prova dieci MP4 distinti né retry idempotente per segmento.
- **Secondo proxy live Gate A (25/10):** sul worker col preset statico
  `IMPORTANT_PHRASE` (`freebuff-closeout-20261025T1035Z`), il servizio mediano
  dei dieci job singoli è 1136 ms; il piano fused da dieci item misura 3803 ms
  totali, 380 ms/item (rapporto **0,335** contro soglia 0,40). Crea un solo
  MP4: non certifica il percorso segmentato.
- **Blocco tecnico storico, superato nel working tree 25/10:** chiudere un MP4
  inviava EOF al codec e riaprire la stessa sessione falliva al primo frame;
  il riuso per-item restava quindi fail-closed. Il writer segmentato descritto
  sotto risolve il caso intra-piano con un encoder non drenato, EOF singolo e
  mux indipendenti. Non riabilita il riuso encoder cross-job P2.
- **Audit del seam IPC:** Chronon ha già `RENDER_BATCH` (`decode_render_batch_payload`,
  massimo 64 job), ma `ipc_render_batch` chiama in sequenza
  `handle_render_job_request` per ciascun elemento. Mantiene il dominio device
  caldo e restituisce uno stato per job; non condivide il `NativeAvEncoder`,
  quindi ogni MP4 continua ad aprire e finalizzare un codec separato. Nel
  codice RenderingGen corrente non risultano chiamate a `RENDER_BATCH`; il
  batch P4 verificato usa invece un piano fuso che produce un solo MP4.
  Riferimenti: `Chronon3d/apps/chronon3d_cli/daemon/daemon_service_ipc_render_job.cpp`
  e `RenderingGen/README.md` (§ «Overlay timeline»).
- **Contratto implementato nel working tree, non ancora provato live:** gli
  intervalli frame arrivano al writer da una singola sessione encoder con PTS
  globali; il primo frame di ogni segmento è intra, i packet vengono instradati
  per PTS e l'EOF è inviato una sola volta. Ogni MP4 viene verificato e ricevuta
  separatamente; writer e finalizzatore falliscono chiusi su boundary non-key,
  packet/range non validi o artifact incompleto. Il seam `RENDER_BATCH` resta
  distinto e non è il percorso usato per condividere questo encoder.
- **Building block Chronon3D, aggiornato 25/10:** `MuxOutputSegmentWriter`
  instrada una timeline codificata verso mux MP4 indipendenti, azzera i
  PTS/DTS per segmento e rifiuta packet fuori range, boundary senza keyframe,
  PTS non crescenti o conteggi di frame incompleti. Il seam è collegato a
  `NativeAvEncoder`, `FfmpegPipeOptions`, `RenderJobRequest`, IPC e normalizer;
  ogni inizio segmento forza un frame intra e l'encoder riceve EOF una volta
  sola. Verifiche rieseguite sul worktree: `chronon3d_media_video_tests`
  writer 3 casi/115 assertion PASS (hash pixel identici, mirror della timeline
  completa e rifiuto del boundary senza keyframe); `chronon3d_cli_tests`
  `NativeAvEncoder` 1/45 e `RENDER_BATCH` retry 1/20 PASS. Il test finalizer/
  receipt già documentato resta 1/42 assertion PASS (due MP4 e receipt distinti,
  cleanup al secondo segmento invalido).
  **Finalizzazione Chronon aggiornata:** il pipe exporter mette ogni segmento
  in staging, verifica l'intero insieme prima della pubblicazione, rifiuta
  output finali già esistenti e passa SHA/probe/frame window distinti al
  contesto delle ricevute. I due receipt writer emettono una ricevuta per
  segmento. Test mirato sul finalizer/receipt: un caso/42 assertion, inclusi
  publish di due MP4, receipt per entrambi e rollback/cleanup se il secondo
  segmento è invalido.
- **RenderingGen integrato nel working tree:** `gpu_run.go` invia le finestre;
  `finalizeSegments` verifica identità e frame count, ritenta una sola volta
  solo il segmento fallito e conserva ogni segmento nel content store.
  Verificati nel checkpoint corrente: planner + retry mirato
  (`GOMAXPROCS=2 go test ./internal/processor -run
  'TestPlanSegmentOutputs|TestFinalizeSegmentsRetriesOnlyTheFailedSegment'
  -count=1 -v`) PASS; `internal/chronon` segment transport/capability PASS;
  package completi processor+chronon PASS; overlay ordering/layout in PipelineGen
  PASS. `GOMAXPROCS=2 go test ./... -count=1` passa sull'intero modulo
  RenderingGen.
- **Test package e workspace:** `GOMAXPROCS=2 go test ./internal/processor
  -count=1` aveva rivelato che il test riflessivo del probe pretendeva che
  `outputFactsFromProbe` valorizzasse `BatchSegments`, che invece nasce dalla
  verifica per-item. Il test ora esclude correttamente quel campo dal mapping
  probe e verifica a parte la proiezione delle facts batch. `GOMAXPROCS=2 go
  test ./... -count=1` passa su tutti i package RenderingGen.
- **P4 resta LIVE PARTIAL:** i test locali dimostrano writer, MP4 multipli,
  receipts e retry isolato; non dimostrano il percorso live con worker/queue
  aggiornati, la parità per-segmento contro render indipendenti sul mix golden,
  né il limite ≤1,1 s/item. La prova live disponibile misura un MP4 solo.
  Nessun guadagno di throughput è attribuito al writer fino a una prova live
  N-MP4 con receipt identity distinta e retry indotto.

- Il contratto MP4-per-item con il final worker è preservato (ogni segmento
  resta un artefatto proprio); il cambiamento è solo nel ciclo di vita della
  sessione render.
- La certificazione VRAM copre oggi 2 daemon: il batching riduce la
  concorrenza richiesta, non la aumenta.
- `drive_publish` per item resta (2,4–3,0 s/item asincroni, fuori finestra):
  è il bersaglio di P5, non di questo ticket.
