# Changelog - 2026-04-30

## Summary

Sessione dedicata al riallineamento del flusso script/timeline con i cataloghi Drive e alla riduzione dei match sbagliati tra stock, clips e Artlist.

## What Changed

### 1. Catalog sync e separazione dei database

- Separazione operativa dei cataloghi in DB distinti:
  - `stock.db.sqlite`
  - `clips.db.sqlite`
  - `artlist.db.sqlite`
- Aggiornamento della configurazione dei root Drive per stock, clips e artlist.
- Aggiunto sync automatico periodico dei cataloghi all’avvio del server.
- Esposto un endpoint manuale di sync cataloghi:
  - `POST /api/artlist/sync-catalogs`

### 2. Resolver candidati per il matching

- Creato l’endpoint:
  - `POST /api/script-docs/association-candidates`
- Il resolver legge i cataloghi locali e restituisce candidati ordinati per score.
- Nessun hardcode su soggetti specifici:
  - il matching usa `topic`, `subject`, `keywords`, `entities` e `narrative`.
- Il resolver privilegia i folder stock esatti quando c'è una corrispondenza forte.

### 3. Timeline planning e normalizzazione

- Rafforzata la pipeline della timeline:
  - segmentazione LLM
  - normalizzazione semantica
  - resolver candidati
  - rendering finale
- Aggiunta normalizzazione del subject per preferire:
  - folder stock esatti
  - entità persona compatibili
  - topic canonico se non ci sono segnali migliori
- Aggiunta cache semantica dei segmenti in `clips.db.sqlite` tramite `segment_embeddings`.
- Invalidation della cache timeline con versioni incrementali per evitare riuso di output vecchi.

### 4. Drive link handling

- Corretto il parsing dei link Drive:
  - supporto sia per `/drive/folders/...`
  - supporto anche per `/drive/u/1/folders/...`
- Corretto il comportamento di normalizzazione dei link:
  - se un record ha già un folder URL valido, viene mantenuto
  - non viene sovrascritto dal `folder_id` del parent
- Risolto il caso in cui il render mostrava il folder padre invece del leaf folder diretto.

### 5. Association priority

- `DriveStockAssociation` ora usa un shortcut diretto per il folder stock esatto.
- `Dynamic Artlist Association` è stato declassato a vero fallback finale.
- Il render della timeline mostra in modo più chiaro:
  - `Drive Stock Association`
  - `Artlist Stock Association`
  - `Clip Drive Association`
  - `Dynamic Artlist Association`
  - `No Association Found`

### 6. LLM prompt e subject cleanup

- Rafforzato il prompt LLM per evitare:
  - file name
  - path fragments
  - soggetti non coerenti col topic
- Rimosse le stopword hardcoded dal tokenizing usato nel percorso timeline.
- Ridotto il drift dei subject generati dall'LLM verso etichette generiche.

### 7. Logging e diagnostica

- Aggiunti log con tempi di esecuzione nei punti critici:
  - generazione timeline
  - candidate matching
  - fallback dinamico
  - creazione documenti
- Migliorata la visibilità dei colli di bottiglia.

## Validation

- Build server eseguita con successo.
- Test dei package toccati eseguiti con successo.
- Verifiche manuali completate su:
  - `Mike Tyson`
  - `Floyd Mayweather`

## Notes

- Il flusso è stato tenuto senza hardcode per soggetti specifici.
- La priorità di associazione ora favorisce il folder stock leaf corretto quando esiste un match esatto.
- Il resolver restituisce il link Drive corretto del folder leaf, non il parent folder.

## Examples

### Example 1: Mike Tyson

Obiettivo:
- associare il segmento `Mike Tyson` al folder stock corretto, senza finire sul parent folder o su un documento Google Docs.

Comportamento finale:
- `Subject: Mike Tyson`
- `Drive Stock Association`
  - `Mike tyson`
  - `Link: https://drive.google.com/drive/folders/1_3g7b1dmJhm33c5-wktPjYOuzZtXyZbO`

Risultato:
- il resolver identifica il leaf folder diretto nel catalogo stock
- il render mostra il folder giusto
- non viene usato il parent `Broke Boxer` come link finale

### Example 2: Quando il subject LLM è troppo generico

Problema visto durante la sessione:
- l'LLM produceva subject come `brooklyn gym beginnings`
- questo faceva deragliare il matching verso cartelle non precise

Correzione introdotta:
- normalizzazione del subject prima del resolver
- preferenza per entità persona compatibili con il topic
- fallback al topic canonico se il subject non è affidabile

Effetto:
- `Mike Tyson` resta `Mike Tyson`
- il downstream non insegue più etichette narrative troppo generiche

### Example 3: Fallback Artlist

Obiettivo:
- usare `Dynamic Artlist Association` solo come ultima risorsa

Comportamento desiderato:
- se esiste un match stock esatto, vince quello
- se esiste un match clips, può vincere quello
- se esiste un match Artlist statico, vince prima del live fallback
- solo se non c’è niente di forte, parte la live search Artlist

Effetto:
- niente live search inutile quando il folder stock è già disponibile
- meno tempo perso in richieste lente

### Example 4: Sync dei cataloghi

Obiettivo:
- mantenere aggiornati i DB locali a partire dai root Drive configurati

Esempio pratico:
- `stock.db.sqlite` si aggiorna dal root stock
- `clips.db.sqlite` si aggiorna dal root clips
- `artlist.db.sqlite` si aggiorna dal root Artlist

Effetto:
- il resolver lavora su cataloghi reali, non su path hardcoded
- i nuovi folder entrano nel matching senza interventi manuali

### Example 5: Link Drive leaf vs parent

Problema visto:
- il render mostrava il folder padre invece del folder leaf diretto

Esempio:
- padre: `Broke Boxer`
- leaf corretto: `Mike tyson`
- leaf link corretto: `https://drive.google.com/drive/folders/1_3g7b1dmJhm33c5-wktPjYOuzZtXyZbO`

Correzione:
- la normalizzazione dei link Drive ora preserva il link folder valido già presente nel record
- il parser riconosce anche i path `/drive/u/1/folders/...`

Effetto:
- il render mostra la cartella corretta associata al subject
