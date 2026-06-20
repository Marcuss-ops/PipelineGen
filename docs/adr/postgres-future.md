# ADR: PostgreSQL non implementato

**Stato**: PostgreSQL NON è in uso.

**Decisione**: Velox è SQLite-first (WAL mode, busy_timeout=5000ms).
Il supporto PostgreSQL sarà implementato su un branch dedicato quando
richiesto da requisiti di scala, non prima.

**Motivazione**:
- Lo scaffolding `internal/store/postgres/` era codice morto: zero
  import, zero path runtime, tutti i metodi non connessi.
- Mantenerlo aggiungeva manutenzione ingiustificata ad ogni cambio
  d'interfaccia.
- SQLite soddisfa i requisiti attuali di concorrenza e throughput.

**Rimosso**: commit slim-5, Giugno 2026.
