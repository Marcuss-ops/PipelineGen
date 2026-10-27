# Timestamp Convention — un solo formato per i tempi

**Machine-consumed**: il gate `percheck_timestamp_convention`
(`cmd/archcheck/scan/migrations/percheck_timestamp_convention.go`) verifica che
questo documento dichiari l'espressione canonica qui sotto (lockstep doc↔gate)
e applica le regole alle migrazioni. Allowlist storica chiusa:
`docs/migrations/timestamp-convention-allowlist.txt`.

## Formato canonico

Una colonna tempo è una colonna il cui nome termina con `_at`, `_expiry` o
`_until` (es. `created_at`, `lease_expiry`, `deferred_until`).

1. **Formato**: ogni valore tempo è **RFC 3339 UTC** (`2026-10-27T10:56:00.123Z`).
   Il codice Go scrive stringhe RFC 3339 UTC; è l'unico formato ammesso nelle
   colonne TEXT su tutti i piani SQLite.
2. **Default**: se una colonna tempo ha un DEFAULT, questo è esattamente

   ```sql
   DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
   ```

   che produce la stessa forma RFC 3339 UTC del codice (frazioni in
   millisecondi). `DEFAULT (datetime('now'))` produce
   `YYYY-MM-DD HH:MM:SS` — formato diverso, quindi **vietato**: una colonna con
   valori scritti dal codice e valori scritti dal DEFAULT finirebbe con due
   formati non confrontabili come stringhe.
3. **Assenza = NULL**: `DEFAULT ''` sulle colonne tempo è **vietato**. La
   stringa vuota non è un tempo: l'assenza si rappresenta con `NULL` (colonna
   nullable) e si legge con `COALESCE`/`IS NULL`. Lo storico
   `TEXT NOT NULL DEFAULT ''` resta solo nelle dichiarazioni già esistenti
   (allowlist) e viene rimosso dalla contraction Postgres (§ Piani).

## Piani

- **SQLite (`migrations/sqlite`, `migrations/sqlite_jobs`)**: colonne TEXT in
  formato RFC 3339 UTC, come sopra.
- **PostgreSQL media (`migrations/postgres`)**: il piano passa a colonne
  **`TIMESTAMPTZ` tipizzate** (BASELINE_PLAN §5: "going forward the media
  domain grows on typed TIMESTAMPTZ"). Durante la dual-write window le colonne
  legacy TEXT convivono con gli specchi `*_ts TIMESTAMPTZ`; la contraction
  elimina le colonne TEXT e rinomina `*_ts` → nome canonico. Le regole qui
  sopra continuano a applicarsi alle colonne TEXT residue durante la finestra.

## Transizioni

Come per gli stati (`docs/migrations/STATE_MATRIX.md`): nessuna migrazione di
rewrite dei valori tempo. Le transizioni di formato usano colonne generate,
view o la sequenza expand → backfill → cutover → contract (AGENTS.md).

## Drift storico (da non replicare)

- 62 migrazioni usano `datetime('now')` e 31 usano `DEFAULT ''` su colonne
  tempo: sono le dichiarazioni nella allowlist storica, da non imitare. Quando
  un file ripulito non contiene più offender, la sua voce va rimossa dalla
  allowlist nella stessa modifica (il gate fallisce sulle voci stale).
