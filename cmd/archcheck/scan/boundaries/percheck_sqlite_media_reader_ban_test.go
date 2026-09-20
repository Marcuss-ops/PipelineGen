// Package scan — companion test for percheck_sqlite_media_reader_ban.go.
//
// Pins:
//
//	(a) "violation trip" — a NEW non-test Go file outside the grandfathered
//	    legacy read plane that reads media_assets emits a violation.
//	(b) "grandfathered zone exempt" — the legacy SQLite/admin read plane
//	    (internal/platform/sqlite, cmd/admin) is exempt, and a zone converted
//	    from a prefix to an exact-file inventory stops auto-exempting.
//	(c) "grandfathered file exempt" — the explicit debt-register entries are
//	    exempt by exact path (a sibling file in the same package is NOT).
//	(d) "PostgreSQL reader exempt" — the canonical media SSOT readers are
//	    correct by construction, and the dialect discrimination extends that
//	    exemption to PostgreSQL reads in any path (the counter is SQLITE
//	    readers, so a $N statement is never debt).
//	(e) "comment-only exempt" — naming the table in prose is not a read.
//	(f) "test file exempt" — *_test.go is out of scope.
//	(g) "write-only not double-reported" — INSERT/UPDATE/DELETE on
//	    media_assets belong to percheck_media_assets_writer_canonical.
//	(h) the REAL repository tree is clean under this gate, and the debt
//	    register has no ghost entries.
package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func scanMediaReader(t *testing.T, root string) *report.Report {
	t.Helper()
	r := &report.Report{}
	ScanSQLiteMediaReaderBan(root, &policy.Policy{}, r)
	return r
}

func mediaReaderViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == sqliteMediaReaderRule {
			out = append(out, v)
		}
	}
	return out
}

// allRegisteredMediaReaderFiles is the union of every exact-file register:
// the production debt register, the degrade-only register, and the inventory of
// converted zones. Tests iterate it so a new entry in ANY of them is
// automatically covered by the existence, staleness and exemption pins.
func allRegisteredMediaReaderFiles() []string {
	maps := []map[string]bool{
		sqliteMediaReaderGrandfatheredFiles,
		sqliteMediaReaderDegradeOnlyFiles,
		sqliteMediaReaderInventoriedZoneFiles,
	}
	total := 0
	for _, m := range maps {
		total += len(m)
	}
	out := make([]string, 0, total)
	for _, m := range maps {
		for rel := range m {
			out = append(out, rel)
		}
	}
	return out
}

// TestSQLiteMediaReaderRegistersAreDisjoint pins that the registers encode
// different facts: production split-brain debt vs. a degrade path selected only
// when the media SSOT is closed vs. the inventory of a converted zone. An entry
// in two of them would mean nobody decided which one it is — and, worse, an
// entry moved into the converted-zone inventory while the debt register still
// held it would hide the migration that emptied it.
func TestSQLiteMediaReaderRegistersAreDisjoint(t *testing.T) {
	seen := map[string]string{}
	for rel := range sqliteMediaReaderGrandfatheredFiles {
		seen[rel] = "the debt register"
	}
	for rel := range sqliteMediaReaderDegradeOnlyFiles {
		if first, ok := seen[rel]; ok {
			t.Errorf("%q is in BOTH %s and the degrade-only register — pick one (it is either wrong and must be migrated, or it is a documented media-disabled path)", rel, first)
			continue
		}
		seen[rel] = "the degrade-only register"
	}
	for rel := range sqliteMediaReaderInventoriedZoneFiles {
		if first, ok := seen[rel]; ok {
			t.Errorf("%q is in BOTH %s and the converted-zone inventory — pick one (a converted zone lists the readers that are still there, not the debt that was migrated out)", rel, first)
		}
	}
}

// TestSQLiteMediaReaderRegistersAreEmpty pins the terminal state of the P2-9
// Phase 2 read ratchet: BOTH exact-file registers are empty. A new entry means a
// file still reads media_assets from SQLite (or claims a media-disabled degrade
// path); the correct response is to migrate the read, not to keep the entry.
func TestSQLiteMediaReaderRegistersAreEmpty(t *testing.T) {
	if len(sqliteMediaReaderGrandfatheredFiles) != 0 {
		t.Errorf("sqliteMediaReaderGrandfatheredFiles must be EMPTY (terminal ratchet state); got %d entr(ies): %v", len(sqliteMediaReaderGrandfatheredFiles), sqliteMediaReaderGrandfatheredFiles)
	}
	if len(sqliteMediaReaderDegradeOnlyFiles) != 0 {
		t.Errorf("sqliteMediaReaderDegradeOnlyFiles must be EMPTY (terminal ratchet state); got %d entr(ies): %v", len(sqliteMediaReaderDegradeOnlyFiles), sqliteMediaReaderDegradeOnlyFiles)
	}
}

func writeGoFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestScanSQLiteMediaReaderBan_ViolationTrip is the load-bearing test: a NEW
// reader of media_assets outside the grandfathered plane MUST fail the gate.
// Without it the scanner could be a silent no-op and the promoted
// SQLITE_MEDIA_READERS counter would still be unverified.
func TestScanSQLiteMediaReaderBan_ViolationTrip(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/reader.go", `package nouveau

func load(db any) {
	const q = "SELECT id, name FROM media_assets WHERE source = ?"
	_ = q
}
`)

	r := scanMediaReader(t, tmp)
	got := mediaReaderViolations(r)
	if len(got) != 1 {
		t.Fatalf("violations = %d, want exactly 1: %+v", len(got), got)
	}
	if got[0].MatchedRule != "forbidden_sql_read_media_assets" {
		t.Errorf("MatchedRule = %q, want forbidden_sql_read_media_assets", got[0].MatchedRule)
	}
	if got[0].Line == 0 {
		t.Errorf("expected a non-zero line number, got %+v", got[0])
	}
}

// TestScanSQLiteMediaReaderBan_JoinIsAlsoARead pins that the JOIN form is
// caught, not just the leading FROM clause.
func TestScanSQLiteMediaReaderBan_JoinIsAlsoARead(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/join.go", `package nouveau

const q = "SELECT t.term FROM clip_search_terms t JOIN media_assets m ON m.id = t.clip_id"
`)

	if got := mediaReaderViolations(scanMediaReader(t, tmp)); len(got) != 1 {
		t.Fatalf("JOIN media_assets must be flagged, got %d violations: %+v", len(got), got)
	}
}

// TestScanSQLiteMediaReaderBan_GrandfatheredZoneExempt pins the legacy read
// plane exemption.
func TestScanSQLiteMediaReaderBan_GrandfatheredZoneExempt(t *testing.T) {
	tmp := t.TempDir()
	body := `package whatever

const q = "SELECT id FROM media_assets WHERE id = ?"
`
	for _, rel := range []string{
		"internal/platform/sqlite/assets/imagesregistry/store.go",
		"internal/platform/sqlite/control_plane.go",
	} {
		writeGoFile(t, tmp, rel, body)
	}
	if got := mediaReaderViolations(scanMediaReader(t, tmp)); len(got) != 0 {
		t.Fatalf("grandfathered zones must be exempt, got %d violations: %+v", len(got), got)
	}
}

// TestScanSQLiteMediaReaderBan_ConvertedZoneIsExactFile pins the zone-conversion
// ratchet: a zone that has been enumerated is exact-file, so the files listed in
// sqliteMediaReaderInventoriedZoneFiles are exempt while a NEW sibling in the
// same package is a violation.
//
// Without this property the conversion would be cosmetic: a package could stay
// a prefix by another name, and the forward prevention the conversion exists to
// buy (a new SQLite media reader cannot land unnoticed) would not exist.
func TestScanSQLiteMediaReaderBan_ConvertedZoneIsExactFile(t *testing.T) {
	cases := []struct {
		zone    string
		listed  string
		sibling string
	}{
		{
			zone:    "internal/platform/qdrant/indexing/",
			listed:  "internal/platform/qdrant/indexing/clipindexer/indexing_state.go",
			sibling: "internal/platform/qdrant/indexing/clipindexer/brand_new.go",
		},
		{
			zone:    "cmd/admin/",
			listed:  "cmd/admin/internal/audit/clip_drive_audit.go",
			sibling: "cmd/admin/internal/audit/brand_new.go",
		},
	}
	for _, tc := range cases {
		t.Run(tc.zone, func(t *testing.T) {
			tmp := t.TempDir()
			body := `package x

const q = "SELECT id FROM media_assets WHERE id = ?"
`
			if !sqliteMediaReaderInventoriedZoneFiles[tc.listed] {
				t.Fatalf("%s is no longer in the converted-zone inventory — this test pins the conversion of %s", tc.listed, tc.zone)
			}
			// The zone prefix must be gone, or the listing below would be exempt
			// for the wrong reason and this test would pass without the register.
			for _, zone := range sqliteMediaReaderGrandfatheredZones {
				if strings.HasPrefix(tc.listed, zone) {
					t.Fatalf("zone %q still covers the converted package — the prefix must be dropped in the same change as the inventory", zone)
				}
			}
			writeGoFile(t, tmp, tc.listed, body)
			writeGoFile(t, tmp, tc.sibling, body)

			got := mediaReaderViolations(scanMediaReader(t, tmp))
			if len(got) != 1 {
				t.Fatalf("expected exactly the unlisted sibling to be flagged, got %d: %+v", len(got), got)
			}
			if !strings.HasSuffix(got[0].File, filepath.Base(tc.sibling)) {
				t.Errorf("flagged %q, want the unlisted sibling %s", got[0].File, filepath.Base(tc.sibling))
			}
		})
	}
}

// withRegisteredMediaReaderFile installs a synthetic debt-register entry for
// the lifetime of one test and restores the register afterwards.
//
// The register is EMPTY today (see TestSQLiteMediaReaderRegistersAreEmpty), so
// the exact-path property has to be exercised against an injected entry rather
// than against the live inventory — otherwise reaching the terminal ratchet
// state would silently stop testing the mechanism that keeps it empty.
func withRegisteredMediaReaderFile(t *testing.T, rel string) {
	t.Helper()
	_, existed := sqliteMediaReaderDegradeOnlyFiles[rel]
	sqliteMediaReaderDegradeOnlyFiles[rel] = true
	t.Cleanup(func() {
		if existed {
			return
		}
		delete(sqliteMediaReaderDegradeOnlyFiles, rel)
	})
}

// TestScanSQLiteMediaReaderBan_GrandfatheredFileExemptByExactPath pins that
// the debt register is exact-file: the listed file is exempt, a NEW sibling in
// the same package is not.
func TestScanSQLiteMediaReaderBan_GrandfatheredFileExemptByExactPath(t *testing.T) {
	tmp := t.TempDir()
	body := `package x

const q = "SELECT id FROM media_assets WHERE id = ?"
`
	const listed = "internal/capabilities/nouveau/listed.go"
	withRegisteredMediaReaderFile(t, listed)
	writeGoFile(t, tmp, listed, body)
	writeGoFile(t, tmp, filepath.ToSlash(filepath.Join(filepath.Dir(listed), "brand_new.go")), body)

	got := mediaReaderViolations(scanMediaReader(t, tmp))
	if len(got) != 1 {
		t.Fatalf("expected exactly the unlisted sibling to be flagged, got %d: %+v", len(got), got)
	}
	if !strings.HasSuffix(got[0].File, "brand_new.go") {
		t.Errorf("flagged %q, want the unlisted sibling brand_new.go", got[0].File)
	}
}

// TestScanSQLiteMediaReaderBan_PostgresReaderExempt pins that the canonical
// PostgreSQL media read authority is never flagged.
func TestScanSQLiteMediaReaderBan_PostgresReaderExempt(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/platform/postgres/media/media_repository.go", `package media

const q = "SELECT id, name, source FROM media_assets WHERE id = $1"
`)
	if got := mediaReaderViolations(scanMediaReader(t, tmp)); len(got) != 0 {
		t.Fatalf("PostgreSQL media readers must be exempt, got %d: %+v", len(got), got)
	}
}

// TestScanSQLiteMediaReaderBan_PostgresDialectOutsidePostgresPath pins the
// dialect discrimination that makes the promoted counter honest: the
// certificate counter is SQLITE_MEDIA_READERS=0, so a PostgreSQL read of
// media_assets in ANY path (a composition-root adapter legitimately querying
// the media SSOT) is correct by construction, not debt.
//
// Without this property the gate would report a correct new PostgreSQL reader
// as a violation, which is how a gate starts collecting bogus exemptions
// instead of routing reads to the SSOT.
func TestScanSQLiteMediaReaderBan_PostgresDialectOutsidePostgresPath(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/pg_reader.go", `package nouveau

const q = "SELECT COALESCE(source_version,'') FROM media_assets WHERE id=$1"
`)
	if got := mediaReaderViolations(scanMediaReader(t, tmp)); len(got) != 0 {
		t.Fatalf("a PostgreSQL-dialect media read must not be flagged as SQLite debt, got %d: %+v", len(got), got)
	}
}

// TestScanSQLiteMediaReaderBan_MixedFileReportsOnlySQLite pins precision: in a
// file holding both a PostgreSQL read and a SQLite fallback, exactly the
// SQLite read is reported. This was the real shape of
// internal/capabilities/assets/artifacts/clips_adapter.go while its SQLite
// branch existed; that branch has since been deleted (its reads now fail closed
// on the canonical media committer), so the shape is pinned here synthetically
// instead of by the live tree.
func TestScanSQLiteMediaReaderBan_MixedFileReportsOnlySQLite(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/mixed.go", `package nouveau

const pgQuery = "SELECT id FROM media_assets WHERE phash = $1 LIMIT 1"
const sqliteQuery = "SELECT id FROM media_assets WHERE phash = ? LIMIT 1"
`)
	got := mediaReaderViolations(scanMediaReader(t, tmp))
	if len(got) != 1 {
		t.Fatalf("a mixed file must report exactly its SQLite read, got %d: %+v", len(got), got)
	}
	if got[0].Line != 4 {
		t.Errorf("flagged line %d, want line 4 (the SQLite statement)", got[0].Line)
	}
}

// TestScanSQLiteMediaReaderBan_ConcatenatedPostgresStatement pins that the
// dialect is read from the whole concatenation expression: long SQL is
// routinely split into adjacent literals, so a $N placeholder living in a
// different piece than the FROM clause must still mark the statement
// PostgreSQL.
func TestScanSQLiteMediaReaderBan_ConcatenatedPostgresStatement(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/concat_pg.go", `package nouveau

func q(f string) string {
	return "SELECT id FROM media_assets WHERE " + f + " AND id = $1"
}
`)
	if got := mediaReaderViolations(scanMediaReader(t, tmp)); len(got) != 0 {
		t.Fatalf("a concatenated PostgreSQL statement must not be flagged, got %d: %+v", len(got), got)
	}
}

// TestScanSQLiteMediaReaderBan_CommentOnlyExempt pins that prose naming the
// table (this scanner's own docs, architecture diagrams in comments) is not a
// read.
func TestScanSQLiteMediaReaderBan_CommentOnlyExempt(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/doc.go", `package nouveau

// History: this package used to run SELECT id FROM media_assets directly.
// It now reads the PostgreSQL media SSOT instead.
const q = "SELECT id FROM pg_media_projection"
`)
	if got := mediaReaderViolations(scanMediaReader(t, tmp)); len(got) != 0 {
		t.Fatalf("comment-only reference must not be a violation, got %d: %+v", len(got), got)
	}
}

// TestScanSQLiteMediaReaderBan_TestFileExempt pins that test files are out of
// scope (fixtures legitimately build SQLite media tables).
func TestScanSQLiteMediaReaderBan_TestFileExempt(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/reader_test.go", `package nouveau

import "testing"

func TestLoad(t *testing.T) {
	_ = "SELECT id FROM media_assets WHERE id = ?"
}
`)
	if got := mediaReaderViolations(scanMediaReader(t, tmp)); len(got) != 0 {
		t.Fatalf("test files must be exempt, got %d: %+v", len(got), got)
	}
}

// TestScanSQLiteMediaReaderBan_WriteOnlyNotFlagged pins the single-owner
// split: writes belong to percheck_media_assets_writer_canonical, so this
// scanner must not double-report them.
func TestScanSQLiteMediaReaderBan_WriteOnlyNotFlagged(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/writer.go", `package nouveau

const (
	q1 = "INSERT INTO media_assets (id) VALUES (?)"
	q2 = "UPDATE media_assets SET name = ? WHERE id = ?"
	q3 = "DELETE FROM media_assets WHERE id = ?"
)
`)
	if got := mediaReaderViolations(scanMediaReader(t, tmp)); len(got) != 0 {
		t.Fatalf("writes must be owned by the writer gate, got %d read violations: %+v", len(got), got)
	}
}

// TestSQLiteMediaReaderGrandfatheredFilesExist guards the debt register
// against ghost entries: every listed file must still exist, so a removed
// consumer forces its entry out in the same change.
func TestSQLiteMediaReaderGrandfatheredFilesExist(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range allRegisteredMediaReaderFiles() {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("grandfathered media-reader entry %q no longer exists: %v (delete the entry in the same change)", rel, err)
		}
	}
}

// TestSQLiteMediaReaderRegisterHasNoStaleEntries is the ratchet pin. Existing
// is not enough: an entry whose SQLite read has been migrated must be deleted,
// otherwise the register silently becomes a permanent allowlist and the
// promoted SQLITE_MEDIA_READERS counter stops meaning anything.
//
// This fired for real on 2026-09-13: internal/app/wiring/registry_adminconsole_api.go
// was migrated to root.MediaAssetStore()/root.MediaAssetVersionStore() by a
// concurrent change, leaving its entry stale. The file-existence test above
// did not notice.
func TestSQLiteMediaReaderRegisterHasNoStaleEntries(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repository root not resolvable from the test cwd: %v", err)
	}
	for _, rel := range allRegisteredMediaReaderFiles() {
		live, err := sqliteMediaReaderRegisterEntryIsLive(root, rel)
		if err != nil {
			t.Errorf("debt-register entry %q is unreadable: %v", rel, err)
			continue
		}
		if !live {
			t.Errorf("debt-register entry %q is STALE — it has no remaining SQLite-dialect read of media_assets; delete the entry (the register must ratchet to zero, never grow into a permanent allowlist)", rel)
		}
	}
}

// TestSQLiteMediaReaderRegisterEntryIsLive pins the staleness predicate itself:
// a SQLite read is live, an all-PostgreSQL file is stale, and a mixed file is
// live for its SQLite branch.
func TestSQLiteMediaReaderRegisterEntryIsLive(t *testing.T) {
	tmp := t.TempDir()
	cases := []struct {
		rel  string
		body string
		want bool
	}{
		{"sqlite.go", "package x\n\nconst q = \"SELECT id FROM media_assets WHERE id = ?\"\n", true},
		{"postgres.go", "package x\n\nconst q = \"SELECT id FROM media_assets WHERE id = $1\"\n", false},
		{"mixed.go", "package x\n\nconst pg = \"SELECT id FROM media_assets WHERE id = $1\"\nconst lite = \"SELECT id FROM media_assets WHERE id = ?\"\n", true},
		{"gone.go", "package x\n\nconst q = \"SELECT id FROM pg_media_projection\"\n", false},
	}
	for _, tc := range cases {
		writeGoFile(t, tmp, tc.rel, tc.body)
		got, err := sqliteMediaReaderRegisterEntryIsLive(tmp, tc.rel)
		if err != nil {
			t.Fatalf("%s: %v", tc.rel, err)
		}
		if got != tc.want {
			t.Errorf("%s: live = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

// TestScanSQLiteMediaReaderBan_RepoTreeIsClean is the integration pin: the
// real repository must satisfy the promoted gate. A failure here means a new
// SQLite media reader landed (or a grandfathered consumer came back).
func TestScanSQLiteMediaReaderBan_RepoTreeIsClean(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repository root not resolvable from the test cwd: %v", err)
	}
	got := mediaReaderViolations(scanMediaReader(t, root))
	if len(got) == 0 {
		return
	}
	var b strings.Builder
	for _, v := range got {
		b.WriteString("\n  " + v.File + ":" + itoaInt(v.Line))
	}
	t.Fatalf("%d NEW SQLite media_assets reader(s) outside the grandfathered plane:%s", len(got), b.String())
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
