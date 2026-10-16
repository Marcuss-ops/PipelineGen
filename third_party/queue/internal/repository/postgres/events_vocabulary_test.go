package postgres

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
)

// queueRoot resolves the queue module root (<RenderingGen>/queue) from this
// package (queue/internal/repository/postgres).
func queueRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
}

// reviewRoot resolves <RenderingGen>, the repository that also carries the
// infra/ and contracts/ carriers of the same vocabulary.
func reviewRoot(t *testing.T) string {
	t.Helper()
	return filepath.Dir(queueRoot(t))
}

func eventVocabulary() map[string]bool {
	known := map[string]bool{}
	for _, e := range client.AllRenderEvents() {
		known[e] = true
	}
	return known
}

// TestEventAliasesMatchWireVocabulary pins the storage layer's aliases to the
// canonical wire constants, so a local re-declaration can never drift.
func TestEventAliasesMatchWireVocabulary(t *testing.T) {
	pairs := map[string]string{
		eventJobCreated:   client.EventJobCreated,
		eventJobClaimed:   client.EventJobClaimed,
		eventLeaseRenewed: client.EventLeaseRenewed,
		eventJobCompleted: client.EventJobCompleted,
		eventJobFailed:    client.EventJobFailed,
		eventJobRequeued:  client.EventJobRequeued,
		eventJobRendered:  client.EventJobRendered,
		eventJobCancelled: client.EventJobCancelled,
	}
	for got, want := range pairs {
		if got != want {
			t.Errorf("event alias %q != canonical wire value %q", got, want)
		}
	}
	if len(client.AllRenderEvents()) != len(pairs) {
		t.Errorf("AllRenderEvents() has %d entries, storage aliases %d: the ledger is missing an event", len(client.AllRenderEvents()), len(pairs))
	}
}

// TestMigrationCommentNamesLiveEvents pins migration 005's documentation to the
// live vocabulary. It used to advertise RENDER_STARTED, an event that is
// written by nothing: a reader trusting the schema comment would query a
// ledger row that can never exist. The pinned carrier is documentation, so a
// renamed event cannot leave a stale name behind.
func TestMigrationCommentNamesLiveEvents(t *testing.T) {
	path := filepath.Join(queueRoot(t), "migrations", "005_render_events.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	known := eventVocabulary()
	token := regexp.MustCompile(`\b(?:JOB|LEASE|RENDER|ATTEMPT)_[A-Z_]+\b`)
	for _, match := range token.FindAllString(string(raw), -1) {
		if !known[match] {
			t.Errorf("%s advertises event %q, which no writer produces (vocabulary: %v)", filepath.Base(path), match, sortedEvents(known))
		}
	}
}

// TestEndToEndScriptsUseLiveEvents pins the shell verification carriers to the
// same vocabulary. The scripts assert event counts with literal SQL
// (`event_type = 'JOB_CREATED'`), a carrier the Go constants cannot reach, so
// a future rename would silently turn those assertions into "count == 0"
// failures that point at the render rather than at the rename.
func TestEndToEndScriptsUseLiveEvents(t *testing.T) {
	script := filepath.Join(reviewRoot(t), "infra", "e2e", "golden-overlay-verify.sh")
	raw, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	known := eventVocabulary()
	literal := regexp.MustCompile(`event_type\s*=\s*'([A-Z_]+)'`)
	matches := literal.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatalf("%s no longer pins any event_type literal; the vocabulary check would be vacuous", filepath.Base(script))
	}
	seen := map[string]bool{}
	for _, m := range matches {
		seen[m[1]] = true
		if !known[m[1]] {
			t.Errorf("%s asserts event_type %q, which is not in the canonical vocabulary %v", filepath.Base(script), m[1], sortedEvents(known))
		}
	}
	// The canary must keep certifying the three lifecycle events the README
	// documents; dropping one would weaken the gate silently.
	for _, want := range []string{client.EventJobCreated, client.EventJobClaimed, client.EventJobCompleted} {
		if !seen[want] {
			t.Errorf("%s no longer asserts %s; the golden canary must certify the created/claimed/completed chain", filepath.Base(script), want)
		}
	}
}

// TestMigrationsReferOnlyToLiveEvents generalizes the migration-005 pin to
// EVERY migration. Migrations are the schema of record: an event name quoted
// in any of them (a comment, a backfill, a defensible CHECK) is documentation
// the next reader will trust, so a retired event must not survive there.
func TestMigrationsReferOnlyToLiveEvents(t *testing.T) {
	dir := filepath.Join(queueRoot(t), "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	known := eventVocabulary()
	token := regexp.MustCompile(`\b(?:JOB|LEASE|RENDER|ATTEMPT)_[A-Z_]+\b`)
	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range token.FindAllString(string(raw), -1) {
			checked++
			if !known[match] {
				t.Errorf("%s names event %q, which no writer produces (vocabulary: %v)", entry.Name(), match, sortedEvents(known))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no migration names an event token; this pin has become vacuous")
	}
}

// TestREADMEsReferOnlyToLiveEvents pins the prose carriers of the event
// vocabulary. A README that documents an event nobody appends sends an
// operator to query a ledger row that can never exist.
func TestREADMEsReferOnlyToLiveEvents(t *testing.T) {
	known := eventVocabulary()
	token := regexp.MustCompile(`\b(?:JOB|LEASE|RENDER|ATTEMPT)_[A-Z_]+\b`)
	checked := 0
	for _, path := range []string{
		filepath.Join(reviewRoot(t), "README.md"),
		filepath.Join(queueRoot(t), "README.md"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range token.FindAllString(string(raw), -1) {
			checked++
			if !known[match] {
				t.Errorf("%s documents event %q, which no writer produces (vocabulary: %v)", filepath.Base(path), match, sortedEvents(known))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no README names an event token; this pin has become vacuous")
	}
}

// jobStatesEverDeclared returns the union of every job state the migration
// chain has ever enumerated in render_jobs_state_check, in apply order. The
// union (not the latest list) is what makes a RETIRED state detectable: 023
// dropped retry_wait, so the union keeps naming it while the live vocabulary
// does not.
func jobStatesEverDeclared(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(queueRoot(t), "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?s)render_jobs_state_check\s*(?:DROP[^;]*;\s*ALTER TABLE render_jobs ADD CONSTRAINT render_jobs_state_check\s*)?CHECK\s*\([^()]*?IN\s*\(([^)]*)\)`)
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllSubmatch(raw, -1) {
			for _, part := range strings.Split(string(m[1]), ",") {
				v := strings.Trim(strings.TrimSpace(part), "'\"")
				if v != "" {
					seen[v] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// TestREADMEsDocumentEveryLiveStateAndNoRetiredOne pins the state vocabulary in
// the API reference in BOTH directions: every live state must be documented
// (a state an operator cannot find is a state they will not handle), and no
// retired state may survive in prose (retry_wait was dropped by migration 023
// and must never be advertised again).
func TestREADMEsDocumentEveryLiveStateAndNoRetiredOne(t *testing.T) {
	readmes := []string{
		filepath.Join(reviewRoot(t), "README.md"),
		filepath.Join(queueRoot(t), "README.md"),
	}
	contents := make(map[string]string, len(readmes))
	for _, path := range readmes {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		contents[path] = string(raw)
	}

	queueReadme := filepath.Join(queueRoot(t), "README.md")
	for _, state := range client.AllStates() {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(string(state)) + `\b`)
		if !re.MatchString(contents[queueReadme]) {
			t.Errorf("%s does not document live state %q", filepath.Base(queueReadme), state)
		}
	}

	ever := jobStatesEverDeclared(t)
	if len(ever) == 0 {
		t.Fatal("the migration chain no longer enumerates render_jobs_state_check; this pin has become vacuous")
	}
	live := map[string]bool{}
	for _, s := range client.AllStates() {
		live[string(s)] = true
	}
	retiredChecked := 0
	for _, state := range ever {
		if live[state] {
			continue
		}
		retiredChecked++
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(state) + `\b`)
		for path, body := range contents {
			if re.MatchString(body) {
				t.Errorf("%s documents state %q, which the migration chain retired; remove it from the prose", filepath.Base(path), state)
			}
		}
	}
	if retiredChecked == 0 {
		t.Logf("no retired job state to check (the migration chain currently declares only live states)")
	}
}

func sortedEvents(known map[string]bool) []string {
	out := make([]string, 0, len(known))
	for e := range known {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// TestNoSecondEventVocabularyInStorage pins that the storage layer holds no
// literal event strings of its own: every value must come from the wire
// package. A private literal is exactly how the vocabulary drifted before.
func TestNoSecondEventVocabularyInStorage(t *testing.T) {
	dir := filepath.Join(queueRoot(t), "internal", "repository", "postgres")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	literal := regexp.MustCompile(`"(?:JOB|LEASE_RENEWED|RENDER)_[A-Z_]+"`)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "events.go" {
			continue // the alias table is the single legitimate carrier
		}
		if hit := literal.FindString(string(raw)); hit != "" {
			t.Errorf("%s declares the event literal %s; use the queue/client alias instead", name, hit)
		}
	}
}
