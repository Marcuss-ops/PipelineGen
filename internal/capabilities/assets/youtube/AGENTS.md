# Istruzioni per il flusso YouTube clip

Queste istruzioni valgono per `POST /api/clips/process`, il download e il taglio
dei segmenti YouTube, la trascrizione e la pubblicazione su Drive. Il riferimento
operativo del repository è
[`docs/operations/youtube-live-testing-runbook.md`](../../../../docs/operations/youtube-live-testing-runbook.md).

## Contratto HTTP

- L'endpoint canonico è `POST /api/clips/process`; accoda un job asincrono e
  risponde con `{ok, message, job_id}`. Non esegue il taglio nella richiesta HTTP.
- Prima di accodare, interrogare `GET /api/clips/exists?url=...`. Se `exists` è
  `true`, controllare l'asset e i suoi file Drive prima di decidere se serve un
  nuovo job. Un errore del probe è un errore: non trattarlo come `exists:false`.
- Il JSON editoriale con `video_url`, `title`, `clips_60s` e `hooks_15s` non è il
  payload dell'endpoint. Convertirlo in `url` e `segments`. Per ogni segmento
  mappare `start`/`end`, `topic` → `name`, `summary` → `summary`, titolo video →
  `source_title` e `vector_description` → parole chiave in `topics` o `tags`.
  `clip_id` è solo un riferimento editoriale: non è un campo del segmento API.
- Mettere ogni opzione di destinazione dentro `destination`, mai alla radice:

  ```json
  {
    "url": "https://www.youtube.com/watch?v=VIDEO_ID",
    "segments": [
      {
        "start": "00:22:07",
        "end": "00:23:01",
        "name": "Titolo del segmento",
        "source_title": "Titolo del video",
        "summary": "Sintesi fornita",
        "topics": ["argomento", "parole chiave"],
        "tags": ["parola chiave"]
      }
    ],
    "destination": {
      "folder_id": "ID_CARTELLA_VERIFICATO",
      "group": "Gratteri"
    }
  }
  ```

- Verificare URL e video ID, intervalli `start < end`, durata e ordine dei
  segmenti. Clip principali e hook possono sovrapporsi per scelta editoriale;
  conservarli come segmenti distinti.
- Verificare nome e ID della destinazione Drive prima del job. Con `group` o
  `subfolder_name` la pipeline può creare sottocartelle; con un `folder_id`
  esplicito senza opzioni di sottocartella la destinazione resta piatta. Usare
  solo cartelle già identificate per il lavoro richiesto.

## Esecuzione: un video/job alla volta

1. Preparare e validare il payload di un solo video.
2. Consultare l'endpoint di esistenza.
3. Accodare un solo `POST /api/clips/process`, salvare il `job_id` restituito
   nell'ACK e non inviare il video successivo finché questo job non è terminale.
4. Interrogare il job direttamente tramite `GET /api/jobs/{job_id}/full` (o
   `GET /api/jobs/{job_id}` se serve solo lo stato), con autenticazione admin.
   Non cercare il job con query generiche e non usare SQL come poller.
5. Accettare il job come riuscito solo dopo aver verificato stato `SUCCEEDED`,
   `result.stats` e gli elementi del risultato. Controllare che
   `requested = processed + skipped + failed`, che `failed` sia zero e che ogni
   elemento atteso abbia stato `processed` e un `drive_link`/`drive_file_id`.
   Investigare gli `skipped`; non contarli come file nuovi salvati.
6. Solo dopo la verifica, passare al video successivo. Se un job è fallito,
   leggere errore e log pertinenti, correggere la causa e verificare lo stato
   prima di ritentare: niente job duplicati o retry alla cieca.

Autenticarsi senza stampare segreti. Il loader canonico è
`scripts/with-velox-auth`; usare il bearer `VELOX_ADMIN_TOKEN` caricato da esso.
Non leggere o fare `cat`, `sed` o `env` sul file dei segreti; non copiare token,
password sudo o contenuti di `.env` nei log, nell'output o nei payload.

## Diagnostica yt-dlp / FFmpeg

- Il servizio deve usare `YTDLP_PATH` puntato al wrapper del repository
  `scripts/yt-dlp-pipeline`, che gestisce anche HOME, plugin POT e runtime Node.
- yt-dlp cerca FFmpeg in `PATH`: non basta che l'applicazione abbia
  `FFMPEG_PATH` configurato. Il wrapper prepende la directory di un
  `FFMPEG_PATH` assoluto e valido, così downloader e taglierina usano lo stesso
  binario. Non rimuovere o aggirare questo comportamento.
- Sul deployment verificato il binario `/usr/local/bin/ffmpeg` 7.0.2 andava in
  segmentation fault durante i tagli HLS; `/usr/bin/ffmpeg` 4.4.2 ha superato
  una prova con output H.264 + audio. È una diagnosi specifica di quel sistema:
  prima di cambiare configurazione, controllare il binario effettivo e
  riprodurre il problema con una singola clip.
- Se il job mostra `ffmpeg exited with code -11`, fermarsi: verificare
  `FFMPEG_PATH`, la directory iniziale di `PATH` vista da yt-dlp e i log del job.
  Riprovare una volta dopo aver corretto la causa, poi verificare la clip
  risultante con `ffprobe` (stream video e audio, durata plausibile). Non
  sostituire FFmpeg o riavviare il servizio senza evidenza che serva.
- Distingui il job `youtube_clip.extract` dai job asincroni successivi
  `asset.text.materialize`: questi ultimi possono continuare dopo il salvataggio
  delle clip e non indicano da soli un errore di download/taglio.

## Report di completamento

Riportare per ogni video titolo/ID, `job_id`, conteggi richiesti/elaborati/
saltati/falliti e link della cartella Drive. Indicare esplicitamente ogni
segmento fallito o saltato. Non dichiarare il lavoro completo solo perché la
richiesta HTTP è stata accodata o il job ha raggiunto il 100%.
