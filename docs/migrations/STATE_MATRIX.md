# State Matrix — vocabolari ufficiali degli stati

**Machine-consumed**: la sezione `state-matrix` è letta dal test sullo schema vivo
(`internal/platform/sqlite/state_matrix_live_test.go`, parser in
`internal/platform/sqlite/statematrix`) e dal gate
`percheck_state_matrix` (`cmd/archcheck/scan/migrations/percheck_state_matrix.go`).
Questa matrice è la SSOT **machine-readable** dei vocabolari di stato dichiarati
dallo schema; la semantica (owner, significato, transizioni) delle colonne di
`media_assets` resta in `internal/platform/sqlite/canonical.go`, che non viene
ripetuta qui.

## Convenzioni normative

1. **Nome campo**: una colonna di stato si chiama `status`, `state` o
   `*_status`/`*_state`. Ogni nuova colonna di stato deve comparire in questa
   matrice **nella stessa modifica che la introduce** (il test live fallisce altrimenti).
2. **Casing**: ogni colonna usa UNA sola famiglia (`UPPER` o `lower`); il casing
   di una colonna non cambia mai. Le transizioni di vocabolario si fanno con
   **view o colonne generate**, mai con migrazioni di rewrite
   (`UPDATE ... SET status/state = ...`): il gate `percheck_state_matrix` respinge
   ogni rewrite migration fuori da `docs/migrations/state-rewrite-allowlist.txt`
   (allowlist storica e chiusa).
3. **Valori terminali** (`terminal`): valori dopo i quali la riga non cambia più
   stato. `-` = non ancora curato; il vocabolario dichiarato resta comunque
   vincolato dal CHECK quando presente.
4. **Scope e formato**: una riga per (tabella, colonna, gruppo-di-piani con la
   stessa identica dichiarazione). Piani ammessi: `sqlite` (primario),
   `sqlite_jobs`, `postgres`. Quando i piani dichiarano default/valori diversi
   compaiono righe separate — la divergenza è visibile, non nascosta.
   `default` = DEFAULT effettivo (`-` = nessun default; cella vuota = `''`);
   `values` = CHECK dichiarato (`-` = non vincolato lato SQL).
   Le copie `legacy_*` (quarantena 263–267) sono fuori matrice: rimosse dalla
   migrazione di demolizione dedicata.

## Drift noti (da non replicare)

- `artifacts.status`: il CHECK effettivo su DB fresco (baseline `000`) è a 6 stati
  senza `STAGED`, mentre la migrazione storica `131` ne installa uno a 7 stati con
  `STAGED` (BC alias di `READY`): due storie diverse per lo stesso vincolo.
- `media_assets.lifecycle_state` ha DEFAULT `'ready'` (lowercase) in SQLite e
  `'ACTIVE'` in Postgres: casing e valore incoerenti tra i piani (il vocabolario
  operativo è quello UPPER documentato in `canonical.go`).

<!-- state-matrix:begin -->
| table | column | planes | default | values | casing | terminal |
|---|---|---|---|---|---|---|
| artifact_stages | state | sqlite | STAGED | STAGED,PUBLISHED,SUCCEEDED,FAILED_PERMANENT | UPPER | SUCCEEDED,FAILED_PERMANENT |
| artifact_stages | state | sqlite_jobs | STAGED | - | UPPER | SUCCEEDED,FAILED_PERMANENT |
| artifacts | status | sqlite | STAGING | STAGING,VERIFYING,READY,FAILED,QUARANTINED,DELETED | UPPER | FAILED,QUARANTINED,DELETED |
| artlist_download_audit | status | sqlite | pending | - | lower | - |
| artlist_queries | sync_status | sqlite | never | never,running,succeeded,failed | lower | succeeded,failed |
| artlist_runs | status | sqlite | queued | - | lower | - |
| assembly_sessions | status | sqlite | - | - | - | - |
| asset_artifacts | status | sqlite | pending | pending,uploaded,verified,deleted | lower | deleted |
| asset_index | status | sqlite | ready | - | lower | - |
| asset_processing | status | sqlite,postgres | pending | pending,running,completed,failed | lower | completed,failed |
| asset_releases | status | sqlite | pending | - | lower | - |
| asset_render_variants | status | sqlite | - | PENDING,READY,FAILED | UPPER | FAILED |
| asset_subtitle_artifacts | status | sqlite | - | PENDING,READY,FAILED,STALE | UPPER | FAILED,STALE |
| asset_text_tracks | status | sqlite | READY | READY,PENDING,FAILED | UPPER | FAILED |
| asset_text_tracks | status | postgres | READY | - | UPPER | FAILED |
| backup_registry | status | sqlite | - | - | - | - |
| benchmark_batch_jobs | status | sqlite |  | - | - | - |
| canonical_mutations | status | sqlite | - | IN_PROGRESS,COMPLETED | UPPER | COMPLETED |
| content_objects | integrity_status | sqlite | - | - | - | - |
| deliveries | status | sqlite | PENDING | PENDING,LEASED,RUNNING,RETRY_WAIT,SUCCEEDED,FAILED,BLOCKED_AUTH,CANCELLED | UPPER | SUCCEEDED,FAILED,CANCELLED |
| drive_folder_catalog | status | sqlite | active | - | lower | - |
| entity_image_catalog_candidates | status | sqlite | fresh | fresh,active,stale,broken,retired | lower | broken,retired |
| entity_image_catalog_candidates | semantic_status | sqlite | unknown | unknown,accepted,rejected | lower | - |
| entity_image_catalog_entities | refresh_status | sqlite | never | never,running,succeeded,failed | lower | succeeded,failed |
| entity_image_catalog_materializations | status | sqlite | pending | pending,materialized,failed | lower | materialized,failed |
| execution_steps | status | sqlite | pending | - | lower | - |
| idempotency_keys | status | sqlite | in_flight | - | lower | - |
| job_attempts | status | sqlite | - | - | - | - |
| job_checkpoints | status | sqlite,sqlite_jobs | - | - | - | - |
| job_steps | status | sqlite,sqlite_jobs | - | - | - | - |
| jobs | status | sqlite | - | QUEUED,LEASED,RUNNING,SUCCEEDED,RETRY_WAIT,FAILED,CANCELLED | UPPER | SUCCEEDED,FAILED,CANCELLED |
| jobs | status | sqlite_jobs | QUEUED | - | UPPER | SUCCEEDED,FAILED,CANCELLED |
| media_assets | lifecycle_state | sqlite | ready | - | lower | DELETED,ARCHIVED |
| media_assets | lifecycle_state | postgres | ACTIVE | - | UPPER | DELETED,ARCHIVED |
| media_assets | index_state | postgres,sqlite | DISCOVERED | - | UPPER | INDEXED,INDEX_FAILED,DELETED |
| media_assets | enrich_state | postgres,sqlite | PENDING | - | UPPER | - |
| media_assets | rights_status | postgres,sqlite | review_required | - | lower | - |
| media_assets | asset_state | postgres,sqlite | DISCOVERED | - | UPPER | DELETED |
| media_assets | review_status | postgres,sqlite | none | - | lower | - |
| media_bindings | approval_status | sqlite | - | - | - | - |
| media_candidates | rights_status | sqlite | - | - | - | - |
| media_candidates | discovery_status | sqlite | - | - | - | - |
| media_candidates | materialization_status | sqlite | - | - | - | - |
| monitor_enqueue_outbox | state | sqlite | pending | - | lower | - |
| monitored_sources | status | sqlite | active | - | lower | - |
| operations | state | sqlite,sqlite_jobs | - | - | - | - |
| outbox_events | status | postgres,sqlite,sqlite_jobs | pending | - | lower | - |
| performance_runs | status | sqlite | - | RUNNING,SUCCEEDED,FAILED | UPPER | SUCCEEDED,FAILED |
| performance_steps | status | sqlite | - | RUNNING,SUCCEEDED,FAILED | UPPER | SUCCEEDED,FAILED |
| pipeline_runs | status | sqlite | PENDING | - | UPPER | - |
| preparation_attempts | status | sqlite | - | RUNNING,READY,FAILED,CANCELLED,PREEMPTED,HIT | UPPER | FAILED,CANCELLED,PREEMPTED,HIT |
| preparation_attempts | status | sqlite_jobs | - | - | - | FAILED,CANCELLED,PREEMPTED,HIT |
| preparation_job_units | adoption_state | sqlite,sqlite_jobs | PENDING | - | UPPER | - |
| preparation_units | state | sqlite | - | PLANNED,RUNNING,READY,FAILED,STALE | UPPER | FAILED,STALE |
| preparation_units | state | sqlite_jobs | - | - | - | FAILED,STALE |
| projection_registry | status | sqlite | - | BUILDING,VALIDATED,ACTIVE,RETIRED,FAILED,FAILED_CLEANED | UPPER | RETIRED,FAILED,FAILED_CLEANED |
| publication_intents | state | sqlite | PREPARED | PREPARED,UPLOADING,PUBLISHED,COMMITTED,ORPHANED,CLEANUP_PENDING,CLEANED,FAILED | UPPER | COMMITTED,ORPHANED,CLEANED,FAILED |
| qdrant_cleanup_audit | status | sqlite | - | - | - | - |
| qdrant_collections | status | sqlite | created | created,indexing,verified,active,reindexing,in_use,retired | lower | retired |
| qdrantprojection_checkpoints | status | sqlite | running | running,succeeded,failed,abandoned | lower | succeeded,failed,abandoned |
| registry_runs | status | sqlite | - | - | - | - |
| run_child_observations | status | sqlite | - | - | - | - |
| run_observability | status | sqlite | - | RUNNING,SUCCEEDED,FAILED,CANCELLED,ABANDONED | UPPER | SUCCEEDED,FAILED,CANCELLED,ABANDONED |
| run_operation_observations | status | sqlite | - | - | - | - |
| run_operation_observations | cache_status | sqlite | - | - | - | - |
| run_stage_observations | status | sqlite | - | - | - | - |
| run_stage_observations | cache_status | sqlite | - | - | - | - |
| script_localizations | status | sqlite | pending | pending,running,ready,failed | lower | ready,failed |
| script_sections | status | sqlite | completed | - | lower | - |
| scripts | status | sqlite | completed | - | lower | - |
| source_identity_registry | verification_status | sqlite | UNVERIFIED | - | UPPER | - |
| stock_artifacts | status | sqlite | PLANNED | PLANNED,EXTRACTING,EXTRACTED,COMPOSING,COMPOSED,PUBLISHING,PUBLISHED,VERIFIED,RETRY_WAIT,FAILED_PERMANENT,QUARANTINED | UPPER | VERIFIED,FAILED_PERMANENT,QUARANTINED |
| stock_batch_groups | status | sqlite | PLANNED | PLANNED,RUNNING,SUCCEEDED,FAILED,RETRY_WAIT | UPPER | SUCCEEDED,FAILED |
| stock_batches | status | sqlite | PLANNED | PLANNED,RUNNING,SUCCEEDED,FAILED,RETRY_WAIT | UPPER | SUCCEEDED,FAILED |
| upload_intents | status | sqlite | - | pending,uploaded,finalized,completed,failed | lower | completed,failed |
| voiceovers | status | sqlite | pending | - | lower | - |
| worker_nodes | status | sqlite | - | - | - | - |
| workflow_steps | status | sqlite | - | - | - | - |
| workflows | status | sqlite | - | - | - | - |
| youtube_discoveries | state | sqlite | pending | - | lower | - |
| job_stage_status | status | sqlite_jobs | - | - | - | - |
| media_assets | lifecycle_status | postgres | ACTIVE | - | UPPER | - |
<!-- state-matrix:end -->
