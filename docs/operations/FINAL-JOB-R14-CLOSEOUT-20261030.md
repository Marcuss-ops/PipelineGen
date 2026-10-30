# Final job R14 — closeout (2026-10-30)

## Esito

Il final job R14 è completato end-to-end. Il job Creator
`job_1793381459882887896_34c8657b` e il job remoto Master
`job_9039e45e5bce777b` risultano `SUCCEEDED`; il run Creator è `SUCCEEDED`.
Il worker 51 ha assemblato 11 frammenti e muxato l'audio finale. Il Creator ha
recuperato il risultato del Master tramite HTTPS e lo ha salvato in
`/home/pierone/Videos/Velox/worker51-final-r14.mp4`.

## Causa e correzione

Il primo polling Creator è fallito con HTTP 502 durante il riavvio del Master.
L'indirizzo di pickup del Master era inoltre `http://localhost:8000`: visto dal
worker remoto puntava a sé stesso, causando il 404. Il final job usa ora
`https://veloxmanager.duckdns.org`; gli asset runtime vengono trasferiti via
HTTPS/Objectstore. Il percorso Drive è mantenuto solo per selezionare la
sorgente stock, poi il Creator la pubblica sull'Objectstore prima
dell'handoff.

Il job Creator è stato riaccodato tramite il retry canonico, che ha ripreso il
`remote_job_id` già salvato. Il Master ha completato al primo tentativo
(`attempt=1`), senza rifare la generazione TTS o il rendering dei frammenti.
Il contatore `retry_count` del job Creator è rimasto a zero.

## Verifica dell'artefatto

- SHA-256: `819a0c9654813f09d6fb3c3a4f100d6c8839082600c52abccaab7f187e6a171b`
- Video: H.264, 1920×1080, yuv420p, 24 fps, 1231 frame
- Audio: AAC stereo, 48 kHz
- Durata: 51,292 s; dimensione: 29.892.565 byte
- Il checksum locale coincide con quello dell'artefatto Master.
- Il manifesto posiziona l'inserto video a schermo intero tra 0 e 5,56 s; il
  video stock riprende a 5,56 s. L'audio copre l'intera durata.

La revisione a intervalli regolari dell'intero video conferma il passaggio
dall'inserto alla sorgente stock e la continuità delle scene. Rileva anche un
limite del contenuto smoke: la `PHRASE_CHAIN` iniziale contiene un paragrafo
lungo e il testo è tagliato verticalmente nei primi 5,56 s. R14 certifica il
percorso tecnico di assemblaggio e trasporto; questo MP4 non va considerato un
master editoriale approvato finché il testo iniziale non viene abbreviato o
spezzato in più frasi e il job non viene rilanciato. Il worker ha registrato
zero byte letti direttamente da Drive per questo run.

## Verifiche del codice

- `go test ./internal/capabilities/scripts/... ./internal/app/wiring -count=1` —
  PASS.
- `make verify-agent` — PASS (foundation, formattazione, `go vet`, build e
  componenti impattati).
- `git diff --check` — PASS.

I test del payload verificano il contratto `compiled_render_plan_json` v2 e
non il vecchio campo `scenes[]`. I test audio verificano che il final job
mantenga l'audio certificato locale per la pubblicazione HTTPS durante
l'handoff, senza il round-trip Drive.
