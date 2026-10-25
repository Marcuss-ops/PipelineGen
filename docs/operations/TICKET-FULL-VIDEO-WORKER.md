# TICKET-FULL-VIDEO-WORKER

**Stato:** ARCHITETTURA CONFERMATA — l'assembly finale resta sul worker remoto
51. Da implementare: riduzione del lavoro e dell'I/O del worker, senza spostare
l'assembly dei video completi su questa macchina. La cache per-hash dipende da
`TICKET-S3-ASSET-LIBRARY`.
**Origine:** il flusso «risolve e scarica stock/clips/overlay/voiceover,
poi assembla sul 51» resta il contratto voluto. L'assembly locale sul worker
di rendering è escluso: concentrare qui tutti i video completi aumenterebbe
la pressione su RAM e rischierebbe OOM. Il lavoro è ridurre lookup, download
ripetuti e attese sul 51 mantenendo lì il final assembly.
**Ordine consigliato:** dopo la libreria S3, misurare e ottimizzare il final
worker remoto prima di aumentare il numero di job concorrenti.

## Principio

Il worker 51 resta il proprietario dell'assembly finale. Il worker di rendering
produce gli artefatti intermedi; il 51 li risolve e li assembla come oggi.
Alleggerire il 51 significa evitare risoluzioni Drive e download duplicati,
riusare una cache locale per-hash e ridurre le attese di I/O, non trasferire
l'assembly dei master su questa macchina. Nessun nuovo percorso deve caricare
qui in memoria o assemblare l'intero video.

## Problema misurato

- Il final worker remoto (51) risolve `drive_file_id`/web link da Drive e
  materializza stock, clip, overlay e voiceover; il costo da ridurre è il
  lavoro di lookup, trasferimento e I/O che precede l'assembly.
- La barriera «Drive ID certificati prima del final render» e l'outbox
  overlay (98–139 s/run di `drive_publish` asincrono) alimentano l'handoff;
  rimuoverle o cambiarne la semantica richiede mantenere un riferimento
  immutabile e risolvibile dal 51.
- Il 51 resta necessario per l'assembly finale. L'obiettivo non è eliminarlo
  né duplicarne il lavoro sulla macchina di rendering, ma ridurre il tempo
  non-assembly e limitare la pressione di memoria e I/O sul worker remoto.

## Cambio (fasi)

1. **Mantenere il final assembly sul 51:** `final_job` resta il percorso
   standard; nessun assembly locale dei master completi e nessun nuovo
   requisito di RAM sulla macchina di rendering.
2. **Riferimenti per-hash e cache sul 51:** il payload delle scene usa hash
   immutabili risolti dalla cache locale/libreria S3. Il 51 riusa i byte già
   presenti; `drive_file_id` può restare metadata storica, ma non deve essere
   necessario per riscaricare un asset già certificato.
3. **Ridurre le attese di input:** prefetch limitato e concorrente degli asset
   mancanti sul 51, con limiti espliciti di memoria/concorrenza; preservare il
   percorso packet-copy e il mux audio esistente, evitando transcodifiche non
   necessarie. Misurare prima i tempi di lookup, download, assembly e upload.
4. **Drive fuori dalle dipendenze evitabili:** rendere non bloccante solo il
   mirror Drive quando gli artefatti sono già persistiti e verificabili in un
   altro store; non rimuovere il passaggio richiesto dal contratto del 51 senza
   una sostituzione verificata.
5. **Bootstrap del 51:** warm-up della cache per-hash e registrazione del
   worker; la cache deve avere limiti e backpressure per evitare OOM.

## Gate di accettazione

- [ ] Il final job continua ad assemblare sul 51; il percorso non carica né
      assembla il master completo sulla macchina di rendering.
- [ ] Run golden sul percorso remoto: parità pixel/audio e identità degli
      artefatti rispetto alla baseline del 51.
- [ ] Dopo il warm-up, gli asset già in cache sul 51 non generano nuove
      richieste Drive; misurare cache hit/miss e byte trasferiti per job.
- [ ] Prefetch/cache rispettano i limiti configurati di RAM e concorrenza;
      stress test con più job non causa crescita illimitata né OOM.
- [ ] Misurare separatamente lookup/prefetch, assembly, mux e pubblicazione;
      dimostrare una riduzione del wall time del final worker senza regressioni
      di parità o affidabilità.
- [ ] Retry idempotente: stesso manifest + stessi hash ⇒ nessun download o
      assembly duplicato oltre quanto richiesto per riprendere un job fallito.

## Dipendenze e rischi

- Dipende da TICKET-S3-ASSET-LIBRARY per riferimenti per-hash e cache
  condivisa; il 51 deve poter leggere gli asset in modo verificato.
- Il prefetch deve essere limitato: parallelismo non bounded o cache senza
  eviction possono spostare il collo su RAM/disco e causare OOM sul 51.
- Preservare il contratto del final job e l'assembly remoto durante la
  migrazione; non disabilitare Drive finché ogni riferimento usato dal 51 non
  è risolvibile e verificato dal nuovo store.
- La destinazione del master finito resta il contratto di delivery esistente;
  un eventuale cambio di destinazione è separato da questo ticket.
