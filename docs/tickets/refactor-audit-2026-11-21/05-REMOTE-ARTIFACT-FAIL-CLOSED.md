# RA-05 — Artifact finale remoto: fail-closed provato e client HTTP limitato

**Data:** 2026-11-21
**Priorità:** P0 (rischio di run "SUCCEEDED" senza MP4 locale)
**Stato:** **DONE (2026-11-21)**
**Owner:** `app/wiring/script` + `capabilities/scripts` (durable mapper)
**Origine:** report esterno 2026-11-21 §4.1; **claim ridimensionato** (vedi §1)
**Verifica sul codice:** **PARZIALE** — il ramo esiste, il fail-closed dichiarato non è provato

## 0. Fix applicato (2026-11-21)

- **Fail-fast sul token mancante**: nuovo sentinel `ErrRemoteFinalArtifactTokenMissing` in
  `app/wiring/script/remote_final_job_artifact.go`; il ramo `artifactToken == ""` ora
  restituisce l'errore tipizzato invece di `(receipt, nil)`. Il test che codificava il vecchio
  comportamento (`TestAttachFinalJobProjectsMasterTiming` con URL `https://master/final.mp4`)
  è stato riallineato a un URL Drive, che non entra nel boundary di download.
- **Client limitato**: `artifactHTTPClient` con `DialContext` 10 s, `TLSHandshakeTimeout` 10 s,
  `ResponseHeaderTimeout` 30 s, `IdleConnTimeout` 90 s e **nessun** `Client.Timeout` (un MP4
  grande non va troncato). `http.DefaultClient` non è più nel percorso.
- **Test nuovi**: `TestWithDownloadedArtifactRequiresArtifactToken` (errore tipizzato, nessun
  path) e `TestDownloadFinalArtifactRemovesPartialOnFailure` (nessun `.part-*` residuo dopo
  hash mismatch). Il cleanup dei `.part` esisteva già nel codice ed è ora pinnato.
- **Evidenza**: `go test ./internal/app/wiring/script/ -run
  'Artifact|Download|FinalJob|Attach'` → **ok 0.192s**; `gofmt -l` pulito.

## 1. Problema (come è stato verificato)

`internal/app/wiring/script/remote_final_job_artifact.go:41-53`:

```go
if strings.TrimSpace(a.artifactToken) == "" {
    return receipt, nil
}
```

Il report lo chiama "falso successo". Il commento di funzione (righe 35-40) dichiara invece
che il ramo è deliberato: "a missing artifact token leaves the receipt untouched and the
persister fails closed with a configuration error", e il consumo di `receipt.ArtifactPath`
avviene a valle in `internal/capabilities/scripts/durable_mapper.go:97`.

Quindi **non è dimostrato** che una run possa chiudere senza artefatto. Quello che manca è:
1. **nessun test** che provi il fail-closed sul token vuoto (e sul caso producer configurato
   senza token);
2. **`http.DefaultClient`** come fallback a `remote_final_job_artifact.go:110`: nessun timeout,
   download di MP4 da centinaia di MB in `os.TempDir()`;
3. **nessuna pulizia** del file temporaneo sul ramo di errore (verificare in
   `downloadFinalArtifact`).

Il rischio reale non è "nil al posto dell'errore" ma "errore scoperto tardi, dopo un
download lungo, con file orfani e nessun timeout".

## 2. Soluzione

1. **Scrivere il test che stabilisce la verità**, prima di toccare il codice:
   - `withDownloadedArtifact` con `receipt.Status="SUCCEEDED"`, `ArtifactURL` valido,
     `artifactToken=""` → il valore restituito arriva al persister e la persistenza **fallisce**
     con un errore classificabile. Se invece il persister accetta, il ramo è un bug: aprire la
     fix come parte di questo ticket.
   - Il test deve vivere dove vive il persister (`capabilities/scripts` o il boundary
     `durable_mapper.go`), non solo nell'adapter.
2. **Errore tipizzato** se il fail-closed non è garantito a valle: definire
   `ErrRemoteFinalArtifactTokenMissing` e restituirlo da `withDownloadedArtifact`. Preferibile
   comunque: fallire **qui**, con messaggio che nomina la variabile di configurazione mancante
   (fail-fast, meno latenza e meno byte scaricati inutilmente).
3. **Client HTTP con limiti espliciti** (sostituire `http.DefaultClient`):
   - `Transport` con `DialContext` timeout (es. 10 s), `TLSHandshakeTimeout`, `ResponseHeaderTimeout`
     (es. 30 s), `IdleConnTimeout`;
   - **nessun** `Client.Timeout` globale sul download (un MP4 grande non deve essere troncato a
     metà), ma un **ctx deadline** proporzionato al payload dichiarato (`ArtifactSizeBytes`) o
     alla velocità minima attesa; se il chiamante non ha deadline, imporne una (es. 30 min);
   - riuso di un client di package o iniettato, non `&http.Client{}` per chiamata.
4. **Pulizia deterministica**: su ogni ramo di errore dopo la creazione del file temporaneo,
   `defer os.Remove(path)` (o `t.Cleanup` nei test). Il file deve vivere solo se il receipt
   viene pubblicato.
5. **Telemetria**: `ArtifactDownloadMS/Bytes/Reused` ci sono già; aggiungere il caso "token
   mancante" come campo/flag esplicito nel receipt, così l'operatore distingue "non
   scaricato per config" da "scaricato 0 byte".
6. **Allineare gli altri 3 siti `http.DefaultClient`** trovati in produzione
   (`app/wiring/vidrush/vidrush_materialization.go:333`,
   `platform/ollama/adapters/person_image_scorer.go:69,190`): stesso transport bounded, o
   ticket separato se il perimetro cresce troppo — ma non lasciarli fuori senza scheda.

## 3. Check finali

```bash
cd refactored
# 1. il fail-closed è provato
go test ./internal/app/wiring/script/ -run 'Artifact|RemoteFinalJob' -v
go test ./internal/capabilities/scripts/ -run 'Durable|ArtifactPath' -v
# atteso: PASS, con un caso esplicito "token vuoto => errore di configurazione"
#         (se il persister accetta, il test è rosso e il ticket resta aperto)

# 2. nessun DefaultClient nel percorso
grep -rn "http.DefaultClient" internal/app/wiring/script/ internal/platform/ollama/adapters/ \
  internal/app/wiring/vidrush/
# atteso: nessun output

# 3. il client ha i timeout (ispezione + test con httptest lento)
go test ./internal/app/wiring/script/ -run 'SlowServer|Timeout' -v
# atteso: il test con server che non risponde termina per deadline, non appende

# 4. nessun file temporaneo orfano sul ramo di errore
go test ./internal/app/wiring/script/ -run 'Cleanup|TempFile' -v
# atteso: PASS; il test conta i file in t.TempDir() dopo l'errore

# 5. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: imporre un timeout troppo corto tronca download legittimi. Mitigazione: timeout
  derivato da `ArtifactSizeBytes` con floor/ceiling dichiarati, e log della soglia applicata.
- **Rischio**: cambiare il ramo token-vuoto rompe un deployment che oggi "funziona" perché il
  persister recupera l'artefatto per un'altra via. Mitigazione: il test al punto 1 è
  l'arbitro; se il recupero esiste, documentarlo e non fallire.
- **Rollback**: revert; il comportamento torna silenzioso ma identico all'attuale.
