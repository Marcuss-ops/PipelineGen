# PipelineGen — Single Source of Truth Guardrails

## Scopo

Questo documento definisce il vincolo architetturale trasversale obbligatorio per PR0–PR5:

> **un concetto = un owner = un import path = una dichiarazione = un punto di registrazione**

PipelineGen non è considerato scalabile se lo stesso concetto viene ridefinito in package diversi, se esistono alias permanenti, wrapper pass-through, mapper paralleli, switch provider duplicati o copie locali di metodi canonici.

Il controllo vale per tipi, metodi, costanti, default, mapper, serializer, repository contract, registry, resolver, sampler, route, config e schema database.

## Regola assoluta

Per ogni concetto deve esistere:

1. **un solo owner canonico**;
2. **un solo import path pubblico interno**;
3. **una sola dichiarazione del tipo o contratto**;
4. **un solo costruttore canonico**;
5. **un solo registry/resolver/sampler quando serve dispatch dinamico**;
6. **un solo punto di serializzazione/persistenza per lo stesso modello**;
7. **zero copie locali della stessa logica**.

Una differenza di nome non rende due implementazioni legittime se fanno la stessa cosa.

---

## Ownership canonica minima

| Concetto | Owner canonico | Regola |
|---|---|---|
| Asset, Source, MediaType, LifecycleState | `internal/domain/asset` | Nessuna ridefinizione nei provider, API o infrastructure |
| Metadata asset | `internal/domain/asset.Metadata` | Unico tipo metadata condiviso dagli asset |
| Contratti repository asset | `internal/domain/asset` | Le implementazioni SQLite restano in infrastructure |
| Job, Status, lease e contratti job | `internal/domain/job` | Nessun clone in application o API |
| Modelli script condivisi | `internal/domain/script` | DTO HTTP distinti solo quando il contratto transport lo richiede |
| Provider dispatch | `internal/application/assets/providers` | Nessuno switch Artlist/YouTube fuori dal registry |
| Configurazione | `internal/infrastructure/config` | Nessun default duplicato in handler, worker o script |
| Route attive | router runtime + generatore docs | `docs/api/ACTIVE_API_GENERATED.md` è derivato, non scritto a mano |
| Schema SQLite | `migrations/sqlite` | Test schema solo in `_test.go`, mai schema production duplicato |
| Process execution | `internal/infrastructure/process` | Nessun wrapper `os/exec` parallelo |
| FFmpeg | `internal/infrastructure/media/ffmpeg` | Nessun command builder FFmpeg duplicato |
| Retry | `pkg/retry` | Nessuna implementazione retry locale |
| File, hash e utility filesystem canoniche | `internal/infrastructure/files` | Nessun helper hash/file equivalente in altri package |
| Composition e registry moduli | `internal/app` | Nessun service costruito nei package API |

La tabella deve essere aggiornata quando nasce un nuovo concetto condiviso. Un nuovo concetto non entra nel repository senza owner dichiarato.

---

## Regola specifica per `Metadata`

### Tipo canonico

Deve esistere una sola dichiarazione:

```go
type Metadata map[string]any
```

nell'owner canonico `internal/domain/asset`.

### Vietato

- dichiarare un altro `type Metadata` in YouTube, Artlist, images, voiceover, API o SQLite;
- creare alias permanenti come `type Metadata = asset.Metadata`;
- usare un secondo tipo equivalente con nome diverso, per esempio `AssetMetadata`, `MediaMetadata`, `ClipMetadata`, se rappresenta lo stesso concetto;
- copiare getter come `GetString`, `GetInt`, `GetFloat`, `GetBool` in package diversi;
- copiare serializer JSON per lo stesso metadata model;
- trasformare metadata con mapper paralleli che applicano regole diverse;
- usare `map[string]any` direttamente nei layer application/API quando il concetto è asset metadata.

### Consentito

- strutture raw private e non esportate dentro un adapter provider, per esempio la risposta JSON specifica di YouTube o Artlist;
- conversione raw → `asset.Metadata` nel singolo adapter proprietario;
- un solo normalizzatore condiviso quando più provider applicano la stessa regola;
- DTO HTTP differenti se rappresentano un contratto di trasporto, purché vengano convertiti una sola volta verso il modello canonico.

### Flusso corretto

```text
provider raw response
        ↓
private adapter parser
        ↓
canonical metadata normalizer/resolver
        ↓
asset.Metadata
        ↓
canonical asset repository serializer
```

### Flusso vietato

```text
YouTubeMetadata
ArtlistMetadata
ClipMetadata
MediaMetadata
map[string]any nei service
mapper diversi nei handler
serializer duplicati nei repository
```

---

## Registry, resolver e sampler obbligatori

Ogni nuova feature che introduce varianti, provider o strategie deve entrare nel meccanismo comune appropriato.

### Registry

Usarlo quando il sistema deve scegliere un'implementazione per nome o capability.

Esempi:

- provider YouTube/Artlist;
- generator immagini;
- voiceover provider;
- job handler;
- module registration.

**Vietato:** aggiungere switch o mappe locali parallele fuori dal registry canonico.

### Resolver

Usarlo quando il sistema deve risolvere una destinazione, un asset, una policy o una strategia a partire da input e contesto.

Esempi:

- destinazione Drive;
- metadata normalization;
- asset lookup;
- clip resolution;
- configuration resolution.

**Vietato:** replicare la stessa catena `if/else` in handler, service e worker.

### Sampler/scorer

Usarlo quando il sistema deve scegliere o ordinare candidati.

Esempi:

- clip ranking;
- asset recommendation;
- provider candidate selection;
- visual selection.

**Vietato:** avere ranking/default differenti in Artlist, YouTube e script flow se la policy è concettualmente la stessa.

---

## Direzione degli import

### Domain

Può dipendere da:

- standard library;
- altri contratti domain solo quando indispensabile e senza cicli.

Non può dipendere da:

- API;
- application;
- infrastructure;
- SQLite;
- Gin;
- Google SDK;
- processi esterni;
- filesystem concreto.

### Application

Può dipendere da:

- domain;
- porte applicative;
- utility pure condivise.

Non può dipendere direttamente da:

- `database/sql` per persistenza di business;
- repository SQLite concreti;
- Google Drive SDK;
- `os/exec`;
- FFmpeg command builder;
- Gin;
- package API.

### Infrastructure

Può dipendere da:

- domain;
- porte application/domain;
- SDK e librerie concrete.

Non deve diventare owner dei modelli di business.

### API

Può dipendere da:

- application use case;
- domain quando serve per mapping semplice;
- transport helpers.

Non può:

- costruire repository o service;
- eseguire SQL;
- avviare processi;
- implementare business rules;
- ridefinire modelli canonici.

### App/composition root

È l'unico layer autorizzato a costruire adapter concreti e collegarli alle porte.

---

## Pattern vietati

### Alias permanenti

```go
type Metadata = asset.Metadata
type SQLiteStore = sqljobs.SQLiteStore
```

Gli alias sono ammessi solo durante una migrazione breve, con TODO numerato, owner, data di rimozione ed exit gate. Lo stato finale PR5 richiede zero alias di compatibilità.

### Rebinding di costruttori o funzioni

```go
var NewService = other.NewService
var ParseMetadata = shared.ParseMetadata
```

Importare direttamente l'owner canonico.

### Wrapper pass-through

```go
func (s *Service) Save(ctx context.Context, x X) error {
    return s.inner.Save(ctx, x)
}
```

Un wrapper è valido solo se aggiunge policy, validazione, transazione, osservabilità o mapping appartenente al proprio layer. Se inoltra soltanto, deve essere eliminato.

### Duplicazione semantica con nome diverso

```go
type ClipMetadata map[string]any
type AssetProperties map[string]any
```

Se rappresentano `asset.Metadata`, sono duplicati anche se il nome cambia.

### Default duplicati

Timeout, retry, path, port, limiti, preset e capability devono avere un owner unico. Non copiare valori numerici in handler, worker e client.

### Switch provider locali

```go
switch source {
case "youtube":
case "artlist":
}
```

Fuori dal provider registry è vietato, salvo parser di input che converte stringa → chiave registry senza costruire provider.

---

## Checklist operativa SOT

### SOT.0 — Inventario dei simboli condivisi

- [ ] Elencare tipi esportati in `internal/domain`, `internal/application`, `internal/infrastructure` e `internal/api`.
- [ ] Cercare nomi duplicati: Asset, Metadata, MediaType, Job, Status, Script, Provider, Resolver, Config.
- [ ] Cercare strutture semanticamente equivalenti con nomi diversi.
- [ ] Cercare metodi getter/parser/normalizer duplicati.
- [ ] Creare una ownership matrix con owner e consumer.

### SOT.1 — Audit `Metadata`

- [ ] Verificare che esista una sola dichiarazione `type Metadata` per asset metadata.
- [ ] Verificare che asset, provider e repository importino `internal/domain/asset`.
- [ ] Cercare `map[string]any` e classificare ogni uso.
- [ ] Eliminare tipi metadata equivalenti.
- [ ] Centralizzare getter e conversioni condivise.
- [ ] Centralizzare encode/decode persistence.
- [ ] Testare round-trip metadata provider → domain → SQLite → domain.

Comandi iniziali:

```bash
rg '^type\s+Metadata\b|^type\s+.*Metadata\b' internal --glob '*.go'
rg 'map\[string\](any|interface\{\})' internal --glob '*.go'
rg 'Get(String|Int|Float|Bool).*Metadata|ParseMetadata|NormalizeMetadata' internal --glob '*.go'
```

### SOT.2 — Audit alias e re-export

- [ ] Eliminare alias di compatibilità non più necessari.
- [ ] Eliminare `var NewX = other.NewX`.
- [ ] Eliminare re-export di errori, costanti e DTO quando il consumer può importare l'owner.
- [ ] Aggiornare tutti gli import verso il package canonico.
- [ ] Verificare zero alias finali nelle aree domain/application.

```bash
rg '^type\s+\w+\s*=\s*' internal --glob '*.go'
rg '^var\s+New\w+\s*=|^var\s+\w+\s*=\s*\w+\.' internal --glob '*.go'
```

### SOT.3 — Audit wrapper pass-through

- [ ] Identificare service/facade che inoltrano senza aggiungere policy.
- [ ] Eliminare facade delegate-fn non necessarie.
- [ ] Iniettare direttamente la porta canonica.
- [ ] Conservare wrapper solo con responsabilità documentata e testata.

### SOT.4 — Audit registry/resolver/sampler

- [ ] Cercare switch su provider/source/type fuori dai registry canonici.
- [ ] Cercare mappe locali di handler/provider.
- [ ] Cercare resolver duplicati per Drive, metadata, asset e clip.
- [ ] Cercare ranking/scoring duplicati.
- [ ] Migrare ogni variante nel registry/resolver/sampler comune.
- [ ] Aggiungere test di duplicate registration e unknown key.

```bash
rg 'switch\s+.*(source|provider|type)|case\s+"(youtube|artlist|stock|voiceover|image)' internal --glob '*.go'
rg 'map\[string\].*(Provider|Handler|Resolver|Generator)' internal --glob '*.go'
```

### SOT.5 — Audit import graph

- [ ] Verificare domain senza import application/infrastructure/API.
- [ ] Verificare application senza SQLite, Gin, SDK Drive, `os/exec` e package API.
- [ ] Verificare API senza SQL, processi e costruttori concreti.
- [ ] Verificare che solo app costruisca adapter concreti.
- [ ] Eliminare cicli risolti con alias o service locator.

```bash
go list -deps ./internal/domain/...
go list -deps ./internal/application/...
rg 'internal/(api|infrastructure)' internal/domain --glob '*.go'
rg 'database/sql|gin-gonic|google.golang.org/api/drive|os/exec|internal/api' internal/application --glob '*.go'
rg 'database/sql|os/exec|internal/infrastructure/database/sqlite' internal/api --glob '*.go'
```

### SOT.6 — Aggiungere guardrail automatici

- [ ] Estendere `scripts/archcheck` con controllo duplicati canonici.
- [ ] Aggiungere detector alias/re-export.
- [ ] Aggiungere detector wrapper pass-through semplice.
- [ ] Aggiungere forbidden-import matrix.
- [ ] Aggiungere controllo provider switch fuori registry.
- [ ] Aggiungere controllo specifico `Metadata`.
- [ ] Aggiungere self-test per ogni detector.
- [ ] Eseguire i guardrail in CI.

Il detector deve fallire su nuove violazioni. Non deve limitarsi a contare stringhe in commenti: usare AST/import graph dove possibile.

### SOT.7 — Test di identità canonica

- [ ] Testare che provider diversi producano lo stesso `asset.Metadata` normalizzato per campi equivalenti.
- [ ] Testare un solo registry entry per nome/capability.
- [ ] Testare unknown provider senza fallback nascosto.
- [ ] Testare che API DTO venga convertito una sola volta.
- [ ] Testare round-trip dei modelli canonici.
- [ ] Testare assenza di divergenza tra default server, worker e client.

### SOT.8 — Verifica finale PR5

- [ ] Eseguire tutti i detector in modalità strict.
- [ ] Allegare ownership matrix al report E2E.
- [ ] Allegare lista alias residui; deve essere vuota.
- [ ] Allegare lista wrapper pass-through residui; deve essere vuota o motivata.
- [ ] Allegare lista forbidden imports; deve essere vuota.
- [ ] Allegare lista switch provider fuori registry; deve essere vuota.
- [ ] Allegare risultato audit `Metadata`; una sola dichiarazione canonica.
- [ ] Marcare la capability `NOT CERTIFIED` se usa ancora un percorso parallelo.

---

## Gate per ogni nuova feature

Prima di aggiungere codice, la PR deve rispondere:

1. Qual è l'owner canonico?
2. Quale registry/resolver/sampler esistente usa?
3. Quale tipo canonico importa?
4. Sta creando un nuovo mapper, default o serializer già esistente?
5. Sta aggiungendo uno switch che dovrebbe stare nel registry?
6. Sta introducendo un alias o wrapper temporaneo?
7. Quale guardrail impedirà una futura duplicazione?

Se non esiste una risposta chiara, la feature non entra nel repository.

---

## Exit gate finale

PipelineGen rispetta il single source of truth soltanto quando:

- `asset.Metadata` ha una sola dichiarazione canonica;
- Asset, Job e Script hanno un solo owner ciascuno;
- zero alias permanenti e zero re-export inutili;
- zero wrapper pass-through senza responsabilità;
- zero provider switch fuori dal registry;
- zero resolver/sampler paralleli per la stessa policy;
- zero default/config duplicati;
- import graph conforme ai layer;
- schema production definito solo dalle migration;
- route docs generate dal router reale;
- archcheck strict e self-test sono verdi;
- PR5 include evidenza dell'audit SOT.8.

Questa verifica è bloccante per dichiarare PipelineGen scalabile e operativo al 100%.
