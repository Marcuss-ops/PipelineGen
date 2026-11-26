# Ops hardening — Taglio 5 payload + Taglio 6 bonifica (2026-11-26)

**Stato:** **DONE con residui dichiarati** (payload già conforme; bonifica
operativa eseguita; retention schedulata attiva; V3 resta epica separata).

## 1. Taglio 5 — Payload “Subito”

Il piano era già implementato nel tree; non è stato riscritto.

| Requisito | Evidenza | Stato |
| --- | --- | --- |
| 6 decode permissivi → strict | `kernel/script/strict_decode.go` + `jsonwire.DecodeStrict` in `output_spec_json.go`, `audio_spec.go`, `MediaPlanSpec.UnmarshalJSON`, `VideoBackgroundSpec.UnmarshalJSON` | **DONE (già presente)** |
| `envelope.Preset` validato | `generation_validation.go` `Preset.IsKnown()` | **DONE (già presente)** |
| `VideoBackgroundSpec.{BlurPX,Zoom}` cablati | `localizedrender` li propaga e li testa | **DONE (già presente)** |
| `final_job_remote` rimosso | decode strict lo rifiuta; `builder_test.go` lo pinna | **DONE (già presente)** |
| check semantici `payloadlint` | `payloadcheck.SemanticErrors/Warnings`: `explicit_only + entity_images`, `output.audio` doppio, `max_phrase_overlays > 15`, `target_words` drift | **DONE (già presente)** |
| V3: 55 campi → 7 blocchi + adapter | non iniziato: richiede un contratto V3 e un adapter al bordo HTTP | **NOT STARTED (epica separata)** |

Test eseguiti:

```text
go test ./cmd/payloadlint ./internal/capabilities/scripts/payloadcheck → ok
go run ./cmd/payloadlint --dir ../ops/jobs                          → exit 0
```

Il corpus reale produce solo warning legacy (`FORCE_REFRESH_DUPLICATE`,
`TARGET_WORDS_WIDE`); non sono errori di contratto.

## 2. Taglio 6 — Bonifica operativa

### 2.1 One-shot “esauriti”: NON rimossi

Verifica prima della cancellazione:

- `cmd/admin/internal/drive/drive_migrate_artlist.go` → subcomando registrato
  `migrate-artlist-assets` (`cmd/admin/subcommands.go:78`);
- `cmd/admin/internal/database/migrate_legacy_cache.go` → subcomando
  `db-migrate-legacy-cache` (`subcommands.go:65`);
- `internal/kernel/digest/legacy_wire.go` → usato da 4 package per payload
  pre-migrazione;
- `internal/capabilities/scripts/finaljob/shims.go` → alias di compilazione
  della foglia.

Sono vivi: cancellarli avrebbe rotto comandi o compatibilità. **Nessuna
rimozione.**

### 2.2 Disco e worktree

- Rimossi 4 worktree temporanei già mancanti (`git worktree prune`).
- Rimosso il worktree pulito `refactored-hotfix-20261125` (**402M**).
- I tre worktree con modifiche non committate restano:
  `refactored-gop03` (142 file), `.planonly-deploy` (16), `.alias-facade` (550).
  Patch/status salvati in `ops/cleanup-2026-11-26/` (file nascosti
  `.planonly-deploy.*`, `.alias-facade.*` inclusi); non distrutti senza
  conferma.
- Backup DB pre-retention compresso:
  `refactored/data/backups/pre-retention-20261126.tar.gz` (**356M**).
- I dump citati (`downloaded_drive_images`, `short_phrase_v1_plans`) non sono
  presenti nel data store di produzione corrente: nessuna rimozione utile.
- Il consumo principale resta il data store di produzione:
  `refactored/data` **34G** (CAS 19G, final-job-fragments 6.2G, media 2.3G).
  Non è stato toccato: è dato di produzione, non cache.

### 2.3 Systemd / CLI

- Installato `/usr/local/bin/chronon3d_cli` dal build validato
  `linux-video-test`; SHA-256 identico
  (`dfab4de6…f21103f9`), `--help` exit 0.
- Drop-in ridotti da 11 a **2**:
  `10-runtime.conf` (ExecStart `/usr/local/bin/chronon3d_cli`, slot, reuse,
  VK ICD) + `30-vram-budget.conf` (hard/soft VRAM).
- Tutti gli altri drop-in salvati in
  `/etc/systemd/system/chronon3d.service.d.bak-20261126/`.
- `chronon3d-b` e `chronon3d-c` (inactive/disabled) spostati in
  `/etc/systemd/system/chronon3d-retired-20261126/`; non compaiono più tra le
  unit.
- `systemctl daemon-reload` eseguito. **Nessun restart di chronon3d**: la
  config è efficace al prossimo restart, per non interrompere render in corso.
- Archiviati e rimossi **36** backup `renderinggen.yaml.*` e **5** `.bak-*` di
  pipelinegen:
  `/var/backups/renderinggen-config-20261126.tar.gz` (3.8K),
  `/var/backups/pipelinegen-dropins-20261126.tar.gz` (2.4K).

### 2.4 DB: retention eseguita, niente drop

Audit read-only sul DB di produzione `refactored/data`:

| Tabella | Righe prima | Candidati | Righe dopo |
| --- | ---: | ---: | ---: |
| `job_registry_metrics` (>7g) | 530.169 | 529.768 | **401** |
| `run_observability` (>7g) | 5.229 | 2.037 | **3.191** |
| `run_report_snapshots` (>7g) | 9.838 | 3.785 | **6.041** |
| `job_payloads` (>30g, soli job terminali) | 2.428 | 0 | 2.428 |
| `replay_bundles` (>30g) | 0 | 0 | 0 |
| `job_events` (>30g) | 20.985 | 0 | 20.985 |
| `resource_observations` (>7g) | 388.321 | 0 | 388.321 |
| `api_requests` (>7g) | 53.276 | 0 | 53.276 |

- Backup consistente prima delle DELETE (`.backup` SQLite), poi compressione.
- `pipelinegen` fermato senza job RUNNING, `wal_checkpoint(TRUNCATE)` +
  `VACUUM` su jobs/observability/media, riavvio: **active**.
- WAL azzerati (prima 1.1G + 621M); disco 13G → **17G free**.
- Al check finale il disco è a **4.4G free**: un build esterno ha creato
  `BrainRust/target` da **26G** (25G in `target/debug`), non la retention.
  Nessun processo cargo/rustc è attivo al check. Il target esterno non è
  stato toccato.
- Al restart il reconciliation ha segnalato 3 run con checkpoint mancante:
  erano già `report_json = {}` nel backup pre-retention (verificato), quindi
  è un difetto preesistente, non un effetto delle DELETE.
- La policy ora copre anche `job_payloads` (solo job terminali),
  `run_report_snapshots` e `replay_bundles`; il test
  `TestApplyRetentionPlanTerminalJobPayloadsOnly` pinna il filtro di
  sicurezza.
- I file DB restano grandi perché la retention non copre `job_results`
  (**607M**) e il payload di `job_registry_events` (**287M**): sono storico
  risultati/ledger, non le tabelle del piano. Estenderli richiede una scelta
  esplicita sul valore di replay/audit.
- Le migrazioni **263–268 sono rinomine di quarantena** (`legacy_*`), non
  tabelle da droppare: nessun `DROP` eseguito.
- Retention ufficiale resa eseguibile con il binario corretto
  `bin/admin db retention` (`bin/pipelinegen admin …` avvia il server HTTP e
  va in conflitto di porta).
- Env dedicato `/etc/pipelinegen/retention.env` generato dall'ambiente del
  servizio (110 chiavi, `root:pipelinegen-agents 640`), include HMAC e DSN
  PostgreSQL. Va rigenerato se cambia la config del servizio.
- Timer `pipelinegen-retention.timer` **enabled**: ogni giorno alle 03:30,
  `Persistent=true`, ritardo casuale 5 min; servizio oneshot `User=pierone`.
- Test timer con la policy completa: `Result=success`, **11 righe**
  eliminate (2 metrics, 7 run, 2 snapshot), VACUUM ok; `job_payloads` e
  `replay_bundles` a zero candidati.
- Prima esecuzione ufficiale: 67 metrics + 6 run, VACUUM ok.
- Conteggi finali con traffico nuovo: `job_registry_metrics` 400,
  `job_registry_events` 17.885, `job_events` 21.076,
  `job_payloads` 2.428, `run_observability` 3.190,
  `run_report_snapshots` 6.041, `replay_bundles` 0.

## 3. Blocchi e limiti

- La build esterna è stata rotta per parte della sessione
  (`bundle.Identities` / `projectSemanticConsolidation`, poi
  `outcome.outcomes`); alle 16:36 era tornata verde (`go build ./...` → 0,
  `payloadlint` e test → 0). Durante la suite finale il WIP esterno ha rotto
  di nuovo `finaljob` (`final_job_compiled_plan.go`); i test mirati
  `./internal/platform/sqlite -run Retention` passano e il nuovo `bin/admin`
  era già compilato prima della rottura.
- `git diff --check` segnala ancora un blank line at EOF in
  `semantic_production_plan.go` (file esterno in modifica): non è codice di
  questa bonifica e non è stato toccato.
- La retention schedulata è attiva; la copertura
  `job_results`/`job_registry_events` resta **follow-up** decisionale.
- Nessun commit/push.

## 4. Follow-up consigliati

1. Decidere se `job_results` e `job_registry_events` possono essere potati
   oltre i 30 giorni (vale il replay/audit) e aggiungerli a `db_retention.go`.
2. Confermare la rimozione dei tre worktree dirty dopo aver validato le patch
   in `ops/cleanup-2026-11-26/`.
3. Decidere se cancellare `BrainRust/target/debug` (**25G**) dopo il build
   esterno: è l'unica causa del calo a 4.4G free.
4. Ripulire il blank line at EOF esterno e, se richiesto, rieseguire
   `archcheck --strict`.
