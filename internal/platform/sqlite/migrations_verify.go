package sqlite

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"go.uber.org/zap"
)

// Boot-time declared-vs-live verification.
//
// The runner's ledger is the authority on what has been applied: a file whose
// version is recorded is skipped, and its checksum is compared so an edit is
// rejected. That protects a migration's SQL from being rewritten — but it does
// NOT protect the DATABASE when an object that an applied migration declares is
// absent (an operator restore, a partially rebuilt file, a dropped table, a
// name taken by a quarantined copy). Observed in production on 2026-09-28:
// version 216 was recorded with a byte-matching checksum while `job_checkpoints`
// did not exist, so every durable checkpoint write failed with "no such table"
// and resume silently fell back to the best-effort path. Nothing in the runner
// noticed.
//
// This check closes that gap: after the apply loop, every TABLE and INDEX
// declared by an in-scope APPLIED migration must exist in the live schema, and a
// declared index must be owned by the table that declares it — SQLite treats an
// index name as unique per database, so `CREATE INDEX IF NOT EXISTS x ON t` is a
// SILENT no-op when `x` already belongs to another table, which is exactly how
// the restored `job_checkpoints` ended up without its index.
//
// A gap is reported at error level with the declaring file and the remedy (a NEW
// numbered migration — editing an applied file cannot work, because the ledger
// skips it). It is deliberately NOT fatal: a drifted production database must
// still boot so the operator can apply the forward migration, and a boot-time
// hard fail would turn a missing table into an outage.

// declaredTable is one table a migration file declares, with its origin so the
// report can name the file an operator has to look at.
type declaredTable struct {
	Table    string
	Filename string
	Version  int
}

// declaredIndex is one index a migration file declares, together with the table
// it was declared ON — the ownership is the part that can silently break.
type declaredIndex struct {
	Index    string
	Table    string
	Filename string
	Version  int
}

var (
	// Named distinctly from the schema-contract test's own patterns (same
	// package): the test parses for column-level drift, this parses for
	// object-level existence, and the two must be free to evolve apart.
	declaredTablePattern = regexp.MustCompile(
		`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)`)
	removedTablePattern = regexp.MustCompile(
		`(?is)DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)`)
	renamedTablePattern = regexp.MustCompile(
		`(?is)ALTER\s+TABLE\s+["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `\]]?\s+RENAME\s+TO\s+["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)`)
	declaredIndexPattern = regexp.MustCompile(
		`(?is)CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `\]]?\s+ON\s+(?:[A-Za-z_][A-Za-z0-9_]*\s*\.\s*)?["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)`)
	droppedIndexPattern = regexp.MustCompile(
		`(?is)DROP\s+INDEX\s+(?:IF\s+EXISTS\s+)?["` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)`)
)

// reservedIdentifiers are SQL keywords that can follow CREATE TABLE in prose
// but are never table names. With comments stripped these should be unreachable,
// which is the point: if one still shows up, the pattern mis-parsed some DDL
// shape nobody anticipated and reporting a table named "if" would be worse than
// saying nothing.
var reservedIdentifiers = map[string]bool{
	"if": true, "not": true, "exists": true, "temp": true, "temporary": true,
	"unique": true, "virtual": true, "without": true, "strict": true, "as": true,
}

// sqlSchemaText strips SQL comments so the declared/removed patterns only ever
// see real DDL. Migration files document themselves heavily; several explain
// idempotency with the prose "CREATE TABLE IF NOT EXISTS" and no table name, and
// parsing that prose produced a phantom table literally called "if" — which the
// drift check then reported as missing on a perfectly healthy database.
func sqlSchemaText(content string) string {
	var b strings.Builder
	b.Grow(len(content))
	inLine, inBlock, inString := false, false, false
	for i := 0; i < len(content); i++ {
		c := content[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				b.WriteByte(c)
			}
		case inBlock:
			if c == '*' && i+1 < len(content) && content[i+1] == '/' {
				inBlock = false
				i++
			}
		case inString:
			b.WriteByte(c)
			if c == '\'' {
				inString = false
			}
		case c == '\'':
			inString = true
			b.WriteByte(c)
		case c == '-' && i+1 < len(content) && content[i+1] == '-':
			inLine = true
			i++
		case c == '/' && i+1 < len(content) && content[i+1] == '*':
			inBlock = true
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// declaredTablesIn returns the lowercased table names a migration file creates.
func declaredTablesIn(content string) []string {
	matches := declaredTablePattern.FindAllStringSubmatch(sqlSchemaText(content), -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		name := strings.ToLower(match[1])
		if reservedIdentifiers[name] || strings.HasPrefix(name, "sqlite_") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// removedTablesIn returns the lowercased table names a migration file takes away,
// either by dropping them or by renaming them to something else.
func removedTablesIn(content string) []string {
	text := sqlSchemaText(content)
	names := make([]string, 0)
	for _, match := range removedTablePattern.FindAllStringSubmatch(text, -1) {
		names = append(names, strings.ToLower(match[1]))
	}
	for _, match := range renamedTablePattern.FindAllStringSubmatch(text, -1) {
		names = append(names, strings.ToLower(match[1]))
	}
	return names
}

// declaredIndexesIn returns the (index, table) pairs a migration file creates.
func declaredIndexesIn(content string) [][2]string {
	matches := declaredIndexPattern.FindAllStringSubmatch(sqlSchemaText(content), -1)
	pairs := make([][2]string, 0, len(matches))
	for _, match := range matches {
		index, table := strings.ToLower(match[1]), strings.ToLower(match[2])
		if reservedIdentifiers[index] || reservedIdentifiers[table] {
			continue
		}
		pairs = append(pairs, [2]string{index, table})
	}
	return pairs
}

// eventKey orders two statements: by migration version first, and within one
// file by position. Both halves are needed. Version alone cannot decide the two
// shapes this check has to tell apart, because each happens INSIDE a single
// migration file:
//
//	table rebuild   CREATE jobs_new … ALTER TABLE jobs_new RENAME TO jobs
//	                => the scratch name ends the file removed, not declared
//	restore + index DROP INDEX idx … CREATE INDEX idx ON <table>
//	                => the name ends the file declared, not removed
type eventKey struct {
	version int
	pos     int
}

func (k eventKey) after(other eventKey) bool {
	if k.version != other.version {
		return k.version > other.version
	}
	return k.pos > other.pos
}

// objectEvent is one create or remove statement for a named object.
type objectEvent struct {
	name    string
	key     eventKey
	declare bool
}

// tableEventsIn returns the create/drop/rename statements for tables, in file
// order, tagged with the migration version.
func tableEventsIn(content string, version int) []objectEvent {
	text := sqlSchemaText(content)
	events := make([]objectEvent, 0)
	for _, match := range declaredTablePattern.FindAllStringSubmatchIndex(text, -1) {
		name := strings.ToLower(text[match[2]:match[3]])
		if reservedIdentifiers[name] || strings.HasPrefix(name, "sqlite_") {
			continue
		}
		events = append(events, objectEvent{name: name, key: eventKey{version, match[0]}, declare: true})
	}
	for _, match := range removedTablePattern.FindAllStringSubmatchIndex(text, -1) {
		events = append(events, objectEvent{name: strings.ToLower(text[match[2]:match[3]]), key: eventKey{version, match[0]}})
	}
	for _, match := range renamedTablePattern.FindAllStringSubmatchIndex(text, -1) {
		// The renamed-away name is the one whose existence ends here; the new
		// name is handled by whatever created it.
		events = append(events, objectEvent{name: strings.ToLower(text[match[2]:match[3]]), key: eventKey{version, match[0]}})
	}
	return events
}

// indexEventsIn is tableEventsIn for indexes.
func indexEventsIn(content string, version int) []objectEvent {
	text := sqlSchemaText(content)
	events := make([]objectEvent, 0)
	for _, match := range declaredIndexPattern.FindAllStringSubmatchIndex(text, -1) {
		index, table := strings.ToLower(text[match[2]:match[3]]), strings.ToLower(text[match[4]:match[5]])
		if reservedIdentifiers[index] || reservedIdentifiers[table] {
			continue
		}
		events = append(events, objectEvent{name: index, key: eventKey{version, match[0]}, declare: true})
	}
	for _, match := range droppedIndexPattern.FindAllStringSubmatchIndex(text, -1) {
		events = append(events, objectEvent{name: strings.ToLower(text[match[2]:match[3]]), key: eventKey{version, match[0]}})
	}
	return events
}

// droppedIndexesIn returns the lowercased index names a migration file drops, so
// a deliberate rename (drop + create under a new name) is not reported as drift.
func droppedIndexesIn(content string) []string {
	matches := droppedIndexPattern.FindAllStringSubmatch(sqlSchemaText(content), -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, strings.ToLower(match[1]))
	}
	return names
}

// schemaOwnership records the reasons a declared object may legitimately be
// absent, so the check reports drift and only drift.
type schemaOwnership struct {
	// declaredAt / removedAt hold the newest statement about each object, as an
	// eventKey (version, then position inside the file). Removal is ORDER-AWARE:
	// 273 drops and recreates the same index name in one file, an unconditional
	// drop set would treat it as deliberately gone forever, and a version-only
	// comparison cannot tell that apart from a rebuild's scratch table.
	declaredAt map[string]eventKey
	removedAt  map[string]eventKey
	// sanctionedArchive: the table is one the out-of-band archival documented by
	// migration 265 was allowed to move out of this database. The set is EXPLICIT
	// (see executionPlaneArchivedTables) because the migration corpus cannot
	// express it: both planes declare these names, and `job_checkpoints` is
	// declared by both planes while the primary runtime still writes to its
	// primary copy — so "a sibling plane declares it" is NOT a licence to stay
	// quiet, and guessing would have hidden the very drift this check exists for.
	sanctionedArchive map[string]bool
	// recordedDropWithoutFile: an APPLIED ledger row whose file is no longer in
	// the corpus, and whose recorded filename names a drop of this object. The
	// ledger is the authority that the intent existed; the file is unavailable,
	// so the drop cannot be read from the corpus (253_drop_assembly_sessions.sql,
	// removed from the tree, is the live example).
	recordedDropWithoutFile map[string]bool
}

// schemaOwnershipFor assembles the exclusion sets. It is pure apart from reading
// the migration files, which the caller already has in hand.
func schemaOwnershipFor(migrations []migrationFile, applied map[int]appliedRecord, targetDB string) schemaOwnership {
	own := schemaOwnership{
		declaredAt:              map[string]eventKey{},
		removedAt:               map[string]eventKey{},
		sanctionedArchive:       map[string]bool{},
		recordedDropWithoutFile: map[string]bool{},
	}
	for name := range executionPlaneArchivedTables {
		own.sanctionedArchive[name] = true
	}
	// The corpus is scanned WITHOUT the ledger gate on purpose: exclusions are
	// statements of intent, and a fresh database records long ranges of history as
	// "covered by the baseline", so a drop declared in one of those skipped files
	// would otherwise be invisible and its table reported as drift forever.
	for _, m := range migrations {
		if !migrationAppliesToTargetDB(m.scope, targetDB) {
			continue
		}
		content, err := os.ReadFile(m.path)
		if err != nil {
			continue
		}
		events := tableEventsIn(string(content), m.version)
		events = append(events, indexEventsIn(string(content), m.version)...)
		for _, event := range events {
			target := own.removedAt
			if event.declare {
				target = own.declaredAt
			}
			if event.key.after(target[event.name]) {
				target[event.name] = event.key
			}
		}
	}

	// Applied rows whose file is gone: only the recorded filename remains, and
	// for the historical drop migrations that name IS the statement of intent.
	onDisk := map[string]bool{}
	for _, m := range migrations {
		onDisk[m.filename] = true
	}
	for _, record := range applied {
		if onDisk[record.filename] {
			continue
		}
		own.recordedDropWithoutFile[strings.ToLower(strings.TrimSuffix(record.filename, ".sql"))] = true
	}
	return own
}

// recordedAsDropped matches a ledger filename of an unreadable migration against
// an object name: "253_drop_assembly_sessions" covers the table
// "assembly_sessions".
func (o schemaOwnership) recordedAsDropped(name string) bool {
	for recorded := range o.recordedDropWithoutFile {
		if strings.HasSuffix(recorded, "_drop_"+name) || strings.HasSuffix(recorded, "_"+name) {
			return true
		}
	}
	return false
}

// expectedAbsent reports whether the absent object `name` has a declared reason.
func (o schemaOwnership) expectedAbsent(name string) bool {
	return o.removedAt[name].after(o.declaredAt[name]) || o.sanctionedArchive[name] || o.recordedAsDropped(name)
}

// executionPlaneArchivedTables is the finite set of execution-plane tables that
// the data-plane migration script is allowed to have archived out of a primary
// database, by renaming them to legacy_<name> after verifying the jobs-database
// copy. Migration 265 declares the split and states that SQL migrations must not
// rename tables blindly because a fresh and an older primary database must both
// stay migratable — which is why every one of these names is ALSO declared by
// `migrations/sqlite_jobs/` (see
// TestSanctionedArchiveListMatchesTheJobsPlaneCorpus, which keeps this list
// anchored to that corpus).
//
// `job_checkpoints` is deliberately ABSENT. It is declared by the jobs plane too,
// but the primary runtime still opens its primary copy (the durable checkpoint
// resolver wired at app/wiring/script_generation_runtime.go), so its absence IS
// drift — the case this whole check was written for. Adding a name here is a
// claim that nobody reads the primary copy any more; the runbook says so.
var executionPlaneArchivedTables = map[string]bool{
	"artifact_stages":             true,
	"dead_letter_jobs":            true,
	"job_events":                  true,
	"job_registry_events":         true,
	"job_registry_metrics":        true,
	"job_results":                 true,
	"job_steps":                   true,
	"preparation_attempts":        true,
	"preparation_claim_snapshots": true,
	"preparation_dependencies":    true,
	"preparation_job_units":       true,
	"preparation_units":           true,
}

// verifyDeclaredTables applies the ledger rule to the SCHEMA: for every applied
// migration in scope for targetDB, the tables and indexes it declares must exist.
func verifyDeclaredTables(db queryable, targetDir, targetDB string, log *zap.Logger) error {
	migrations, err := discoverMigrations(targetDir)
	if err != nil {
		return fmt.Errorf("verify declared tables: discover migrations: %w", err)
	}
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		return fmt.Errorf("verify declared tables: load applied migrations: %w", err)
	}
	live, err := liveTableNames(db)
	if err != nil {
		return fmt.Errorf("verify declared tables: read live schema: %w", err)
	}
	indexOwners, err := liveIndexOwners(db)
	if err != nil {
		return fmt.Errorf("verify declared tables: read live indexes: %w", err)
	}

	missingTables := missingDeclaredTables(migrations, applied, live, targetDB)
	missingIndexes := missingDeclaredIndexes(migrations, applied, live, indexOwners, targetDB)
	if len(missingTables) == 0 && len(missingIndexes) == 0 {
		if log != nil {
			log.Debug("migration schema verification: every applied migration's objects exist",
				zap.String("target_db", targetDB))
		}
		return nil
	}
	remedy := "add a NEW numbered migration that recreates it; editing an already-applied file is skipped by the ledger"
	for _, m := range missingTables {
		if log == nil {
			continue
		}
		log.Error("applied migration declares a table that does not exist in this database",
			zap.String("table", m.Table),
			zap.String("declared_by", m.Filename),
			zap.Int("version", m.Version),
			zap.String("target_db", targetDB),
			zap.String("remedy", remedy),
		)
	}
	for _, m := range missingIndexes {
		if log == nil {
			continue
		}
		log.Error("applied migration declares an index that the live schema does not carry on its table",
			zap.String("index", m.Index),
			zap.String("declared_on", m.Table),
			zap.String("live_owner", indexOwners[m.Index]),
			zap.String("declared_by", m.Filename),
			zap.Int("version", m.Version),
			zap.String("target_db", targetDB),
			zap.String("remedy", remedy),
		)
	}
	return nil
}

// missingDeclaredTables is the pure core: the set of tables an applied, in-scope
// migration declares that the live schema does not contain.
func missingDeclaredTables(migrations []migrationFile, applied map[int]appliedRecord, live map[string]bool, targetDB string) []declaredTable {
	own := schemaOwnershipFor(migrations, applied, targetDB)

	// Newest numbered declaring file per table, over APPLIED migrations in
	// scope. Newest rather than earliest on purpose: the report has to name the
	// file an operator can act on, and that is the most recent migration that
	// PROMISED the table — the restore migration in the production case, whose
	// ledger row is also the newest. The baseline (version 0) is only named when
	// no numbered file declares the table at all.
	declared := map[string]declaredTable{}
	for _, m := range migrations {
		if !migrationAppliesToTargetDB(m.scope, targetDB) {
			continue
		}
		if _, ok := applied[m.version]; !ok {
			continue
		}
		content, err := os.ReadFile(m.path)
		if err != nil {
			continue
		}
		for _, name := range declaredTablesIn(string(content)) {
			if prev, seen := declared[name]; seen && prev.Version >= m.version {
				continue
			}
			declared[name] = declaredTable{Table: name, Filename: m.filename, Version: m.version}
		}
	}

	gaps := make([]declaredTable, 0)
	for name, d := range declared {
		if live[name] || own.expectedAbsent(name) {
			continue
		}
		gaps = append(gaps, d)
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].Version != gaps[j].Version {
			return gaps[i].Version < gaps[j].Version
		}
		return gaps[i].Table < gaps[j].Table
	})
	return gaps
}

// missingDeclaredIndexes is the index half of the same contract: an applied,
// in-scope migration declares `index ON table`, so the live schema must carry
// that index ON THAT TABLE. An index that exists under the right name but on a
// different table is drift too — that is the silent-skip shape (SQLite keeps
// index names unique per database, so `IF NOT EXISTS` hides the collision).
func missingDeclaredIndexes(migrations []migrationFile, applied map[int]appliedRecord, live map[string]bool, owners map[string]string, targetDB string) []declaredIndex {
	own := schemaOwnershipFor(migrations, applied, targetDB)

	declared := map[string]declaredIndex{}
	for _, m := range migrations {
		if !migrationAppliesToTargetDB(m.scope, targetDB) {
			continue
		}
		if _, ok := applied[m.version]; !ok {
			continue
		}
		content, err := os.ReadFile(m.path)
		if err != nil {
			continue
		}
		for _, pair := range declaredIndexesIn(string(content)) {
			index, table := pair[0], pair[1]
			if prev, seen := declared[index]; seen && prev.Version >= m.version {
				continue
			}
			declared[index] = declaredIndex{Index: index, Table: table, Filename: m.filename, Version: m.version}
		}
	}

	gaps := make([]declaredIndex, 0)
	for index, d := range declared {
		// The owning table's absence is already reported by the table check;
		// reporting every index of a moved or dropped table would bury it.
		if !live[d.Table] || own.expectedAbsent(d.Table) || own.expectedAbsent(index) {
			continue
		}
		if owners[index] == d.Table {
			continue
		}
		gaps = append(gaps, d)
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].Version != gaps[j].Version {
			return gaps[i].Version < gaps[j].Version
		}
		return gaps[i].Index < gaps[j].Index
	})
	return gaps
}

func liveTableNames(db queryable) (map[string]bool, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names[strings.ToLower(name)] = true
	}
	return names, rows.Err()
}

// liveIndexOwners maps each live index name to the table that owns it. SQLite's
// automatic indexes (`sqlite_autoindex_*`) are omitted: no migration declares
// them.
func liveIndexOwners(db queryable) (map[string]string, error) {
	rows, err := db.Query(`SELECT name, tbl_name FROM sqlite_master WHERE type = 'index'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := map[string]string{}
	for rows.Next() {
		var name, table string
		if err := rows.Scan(&name, &table); err != nil {
			return nil, err
		}
		name = strings.ToLower(name)
		if strings.HasPrefix(name, "sqlite_autoindex_") {
			continue
		}
		owners[name] = strings.ToLower(table)
	}
	return owners, rows.Err()
}
