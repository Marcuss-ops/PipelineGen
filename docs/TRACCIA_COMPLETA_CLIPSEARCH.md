# Traccia Completa ClipSearch (Download -> Process -> Drive -> DB)

Questa guida descrive esattamente:
- cosa cerca il sistema,
- cosa scarica,
- dove salva i file,
- come aggiorna DB locali e Drive.

## 1) Input Keyword

L'input arriva da:
- API / cron / monitor canali
- oppure da test diretto (`cmd/tmp_clipsearch_fresh`).

Esempio test reale eseguito:
- keyword: `Floyd Mayweather interview post fight`
- upload root usata: `1ITO-yB7cX0I5UZ2K75gT_D6DdJQTLIE9` (cartella Floyd)

## 2) Ricerca e Selezione Video (YouTube)

File logica:
- `/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/internal/clipsearch/downloader.go`

Flusso:
1. Prova Artlist.
2. Se Artlist non produce clip valida: ricerca YouTube con `yt-dlp` su query varianti:
   - `keyword`
   - `keyword interview`
   - `keyword highlights`
3. Per ogni candidato calcola score rilevanza su:
   - match token keyword in title/channel/description
   - views
   - recency
   - durata compatibile
4. Hard filter anti off-topic:
   - keyword multi-token: richiede almeno 2 token match (o match frase intera).
5. Seleziona il candidato con score migliore.

## 3) Download e Processing Clip

File logica:
- `/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/internal/clipsearch/downloader.go`
- `/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/internal/clipsearch/processor.go`

Passi:
1. Download video selezionato.
2. Normalizzazione a clip finale `7s` in `1080p`.
3. Calcolo visual hash per deduplica.

## 4) Transcript / Testo Sidecar (.txt)

File logica:
- `/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/internal/clipsearch/downloader.go`
- `/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/internal/clipsearch/service.go`
- `/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/internal/clipsearch/uploader.go`

Passi:
1. Best effort download sottotitoli (`vtt`) da YouTube.
2. Conversione `vtt -> testo pulito`.
3. Generazione file `.txt` con:
   - keyword
   - source
   - video_id/url
   - title/channel/uploader/views/date
   - description
   - transcript (se disponibile)
4. Upload `.txt` nella stessa cartella Drive della clip video.

## 5) Drive Folder Resolution

File logica:
- `/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/internal/clipsearch/uploader.go`
- `/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/internal/clipsearch/utils.go`

Regole:
1. Se la cartella root passata coincide già col nome keyword (es. root = `Floyd Mayweather`), carica direttamente lì.
2. Altrimenti cerca cartelle già esistenti (anche ricorsive) con candidati nome keyword.
3. Solo se non trova nulla, crea nuova cartella.

## 6) Aggiornamento DB Locale

File logica:
- `/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/internal/clipsearch/persister.go`

DB aggiornati:
- `/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/data/stock.db.json`
- `/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/data/artlist_local.db.json`

Campi aggiornati:
- `drive_file_id`, `drive_url`, `folder_id`, `filename`, tag keyword.

## 7) Evidenza Test Reale (Mayweather)

Esecuzione effettuata con:

```bash
cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master
KEYWORD='Floyd Mayweather interview post fight' \
UPLOAD_FOLDER_ID='1ITO-yB7cX0I5UZ2K75gT_D6DdJQTLIE9' \
go run ./cmd/tmp_clipsearch_fresh
```

Output rilevante:
- Video caricato in cartella corretta:
  - `folder_id`: `1ITO-yB7cX0I5UZ2K75gT_D6DdJQTLIE9`
  - `file_id`: `14FwUGugiFAZ3QmmLvRiioiwL0PTZyFT4`
- TXT sidecar caricato:
  - `file_id`: `1z1WiV7sf5VIXDCTu9AvM5imBheLWFG_I`

DB locali aggiornati su entry con:
- `drive_file_id = 14FwUGugiFAZ3QmmLvRiioiwL0PTZyFT4`
- `folder_id = 1ITO-yB7cX0I5UZ2K75gT_D6DdJQTLIE9`

## 8) Punti Ancora Mancanti

- Validazione semantica avanzata (NLP/LLM) del contenuto parlato prima dell'upload.
- Persistenza dedicata dei metadati `.txt` in DB (ora il file viene caricato su Drive, ma non tracciato in schema DB con campo esplicito).
