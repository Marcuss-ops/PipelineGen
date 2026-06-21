# PipelineGen — Single Source of Truth residuo

## Obiettivo

Portare la codebase alla regola:

> **un concetto = un owner = un import path = una dichiarazione = un punto di registrazione**

`asset.Metadata`, i modelli asset/job/script e il provider registry esistono già come fondamenta. Questo documento elenca soltanto i controlli e le rimozioni ancora necessari per rendere la regola verificabile automaticamente.

## Checklist residua

### SOT.0 — Ownership matrix definitiva

- [ ] Elencare ogni tipo, contratto, mapper, serializer, registry, resolver, sampler e default condiviso.
- [ ] Assegnare a ciascun concetto un owner canonico e un solo import path.
- [ ] Marcare DTO transport e strutture raw provider come non canoniche e private al proprio adapter.
- [ ] Bloccare nuovi concetti condivisi senza owner dichiarato.

Ownership minima attesa:

| Concetto | Owner canonico |
|---|---|
| Asset e metadata | `internal/domain/asset` |
| Job, status, lease e store contract | `internal/domain/job` |
| Script model | `internal/domain/script` |
| Provider dispatch | `internal/application/assets/providers` |
| Config e default runtime | `internal/infrastructure/config` |
| SQLite asset/job/script | `internal/infrastructure/database/sqlite/*` |
| FFmpeg/process execution | `internal/infrastructure/media/ffmpeg` e process runner canonico |
| Composition | `internal/app` |
| Route attive | router runtime + documentazione generata |

### SOT.1 — Audit metadata

- [ ] Verificare una sola dichiarazione canonica di `asset.Metadata`.
- [ ] Eliminare tipi equivalenti come `ClipMetadata`, `MediaMetadata`, `AssetProperties` quando rappresentano lo stesso concetto.
- [ ] Eliminare alias permanenti `type Metadata = ...`.
- [ ] Classificare ogni `map[string]any` in domain/application e sostituire gli usi che rappresentano metadata asset.
- [ ] Centralizzare getter, normalizzazione, encode/decode e round-trip persistence.
- [ ] Mantenere private le strutture raw YouTube, Artlist, images e voiceover.
- [ ] Testare provider raw → `asset.Metadata` → SQLite → `asset.Metadata`.

```bash
rg '^type\s+.*Metadata\b' internal --glob '*.go'
rg 'map\[string\](any|interface\{\})' internal/domain internal/application --glob '*.go'
rg 'ParseMetadata|NormalizeMetadata|Get(String|Int|Float|Bool)' internal --glob '*.go'
```

### SOT.2 — Eliminare alias, re-export e wrapper

- [ ] Eliminare alias job/composition ancora presenti.
- [ ] Eliminare `var NewX = other.NewX`, sentinel re-export e costanti duplicate.
- [ ] Eliminare wrapper pass-through che non aggiungono policy, mapping, transazione o osservabilità.
- [ ] Fare importare ai consumer direttamente il contratto o owner canonico.
- [ ] Consentire compatibilità temporanea solo con owner, data di rimozione ed exit gate; lo stato finale richiede zero compatibilità.

```bash
rg '^type\s+\w+\s*=\s*' internal --glob '*.go'
rg '^var\s+(New\w+|Err\w+)\s*=\s*' internal --glob '*.go'
```

### SOT.3 — Correggere import e layer

- [ ] Portare a zero SQL e SQLite dentro `internal/domain`.
- [ ] Portare a zero SDK, SQL, Gin, FFmpeg e `os/exec` dentro `internal/application`.
- [ ] Portare a zero SQL, processi e costruzione service dentro `internal/api`.
- [ ] Verificare che solo `internal/app` costruisca adapter concreti.
- [ ] Eliminare cicli risolti tramite service locator, alias o late binding evitabile.

```bash
rg 'database/sql|internal/infrastructure/database/sqlite' internal/domain
rg 'database/sql|gin-gonic|google.golang.org/api/drive|os/exec' internal/application
rg 'database/sql|os/exec|internal/infrastructure/database/sqlite' internal/api
```

### SOT.4 — Registry, resolver e sampler unici

- [ ] Eliminare switch provider/source fuori dal registry canonico.
- [ ] Eliminare mappe locali parallele di provider, handler, generator o strategy.
- [ ] Centralizzare risoluzione destinazioni Drive, asset lookup e metadata normalization.
- [ ] Centralizzare ranking/scoring quando la policy è condivisa.
- [ ] Testare duplicate registration, unknown key e assenza di fallback nascosto.

```bash
rg 'switch\s+.*(source|provider|type)|case\s+"(youtube|artlist|stock)' internal --glob '*.go'
rg 'map\[string\].*(Provider|Handler|Resolver|Generator)' internal --glob '*.go'
```

### SOT.5 — Default, config, errori e logging

- [ ] Portare timeout, retry, path, limiti, preset e port in un owner unico.
- [ ] Eliminare valori di default duplicati tra handler, service, worker e adapter.
- [ ] Wrappare gli errori con `%w` e mantenerne la classificazione tramite `errors.Is/As`.
- [ ] Loggare ogni errore una sola volta al bordo applicativo.
- [ ] Eliminare errori ignorati salvo policy best-effort esplicita e testata.

### SOT.6 — Implementare `archcheck -strict`

- [ ] Aggiungere flag `-strict` senza baseline permissiva.
- [ ] Implementare detector AST/import graph per alias, wrapper, forbidden imports e duplicazioni canoniche.
- [ ] Aggiungere controllo specifico metadata.
- [ ] Aggiungere controllo provider switch fuori registry.
- [ ] Aggiungere controllo SQL fuori infrastructure database.
- [ ] Aggiungere controllo `map[string]any` in domain/application.
- [ ] Aggiungere self-test per ogni detector.
- [ ] Eseguire strict mode in CI.

Metriche finali obbligatorie:

```text
forbidden_internal_roots = 0
legacy_imports = 0
type_aliases = 0
compatibility_wrappers = 0
pass_through_services = 0
sql_outside_infrastructure_database = 0
gin_outside_api = 0
os_exec_outside_process_adapter = 0
map_string_any_metadata_duplicates = 0
provider_switch_outside_registry = 0
architecture_exceptions = 0
```

### SOT.7 — Evidenza finale

- [ ] Allegare ownership matrix al report PR5.
- [ ] Allegare liste vuote di alias, wrapper, forbidden imports e switch paralleli.
- [ ] Allegare risultato dell'audit metadata.
- [ ] Allegare test duplicate registration e round-trip canonico.
- [ ] Marcare `NOT CERTIFIED` una capability che usa ancora un percorso parallelo.

## Gate per ogni nuova feature

Ogni PR deve rispondere:

1. Qual è l'owner canonico?
2. Quale tipo canonico importa?
3. Quale registry/resolver/sampler usa?
4. Sta duplicando mapper, serializer, default o retry?
5. Sta introducendo alias, wrapper, switch o fallback?
6. Quale test/guardrail impedirà una futura duplicazione?

## Exit gate

Il single source of truth è chiuso quando `archcheck -strict` e i self-test sono verdi, gli indicatori sono tutti a zero e PR5 contiene evidenza riproducibile dell'audit.
