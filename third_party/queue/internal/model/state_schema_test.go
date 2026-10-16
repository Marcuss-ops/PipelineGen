package model

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/migrations"
)

// latestCheck extracts the value list of the most recent
// `ADD CONSTRAINT <name> CHECK (<col> IN (...))` across the migration files,
// in lexicographic (apply) order, so the assertion always reads the schema the
// migrator would end up with.
func latestCheck(t *testing.T, constraint string) []string {
	t.Helper()
	names, err := migrations.Names()
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	re := regexp.MustCompile(`(?s)ADD CONSTRAINT\s+` + regexp.QuoteMeta(constraint) + `\s+CHECK\s*\([^()]*?IN\s*\(([^)]*)\)`)
	var values []string
	for _, name := range names {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		m := re.FindSubmatch(raw)
		if m == nil {
			continue
		}
		values = values[:0]
		for _, part := range strings.Split(string(m[1]), ",") {
			v := strings.TrimSpace(part)
			v = strings.Trim(v, "'\"")
			if v != "" {
				values = append(values, v)
			}
		}
	}
	if len(values) == 0 {
		t.Fatalf("no migration defines constraint %q", constraint)
	}
	return values
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// TestJobStateVocabularyMatchesSQLConstraint is the cross-layer pin for the
// job lifecycle states: the Go vocabulary (canonical, defined once in
// queue/client and re-exported here) and the PostgreSQL CHECK constraint must
// list exactly the same states. Without this test, adding a state to Go but
// not to SQL (or vice versa) is a silent runtime failure that only surfaces
// when a job transition is rejected by the database.
func TestJobPublicationMetadataMigrationExists(t *testing.T) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("029_job_publication_metadata.sql")
	if err != nil {
		t.Fatalf("read publication metadata migration: %v", err)
	}
	sql := string(raw)
	for _, required := range []string{"publication_policy TEXT NOT NULL DEFAULT ''", "daemon_admission_wait_ms DOUBLE PRECISION NOT NULL DEFAULT 0", "daemon_admission_wait_nonnegative"} {
		if !strings.Contains(sql, required) {
			t.Errorf("publication metadata migration missing %q", required)
		}
	}
}

func TestJobStateVocabularyMatchesSQLConstraint(t *testing.T) {
	want := make([]string, 0, 8)
	for _, s := range States() {
		want = append(want, string(s))
	}
	got := latestCheck(t, "render_jobs_state_check")
	if strings.Join(sortedStrings(got), ",") != strings.Join(sortedStrings(want), ",") {
		t.Fatalf("render_jobs_state_check %v != Go States() %v", sortedStrings(got), sortedStrings(want))
	}
}

// TestWorkerStatusVocabularyMatchesSQLConstraint pins the worker-status
// vocabulary the same way the job states and the attempt statuses are pinned.
// It was the one lifecycle vocabulary without a cross-layer check: adding a
// status to Go and forgetting migration 004 (or vice versa) only failed when a
// worker tried to register.
func TestWorkerStatusVocabularyMatchesSQLConstraint(t *testing.T) {
	want := make([]string, 0, 8)
	for _, s := range client.AllWorkerStatuses() {
		want = append(want, string(s))
	}
	got := latestCheck(t, "rendering_workers_status_check")
	if strings.Join(sortedStrings(got), ",") != strings.Join(sortedStrings(want), ",") {
		t.Fatalf("rendering_workers_status_check %v != client.AllWorkerStatuses() %v", sortedStrings(got), sortedStrings(want))
	}
}

// TestAttemptStatusVocabularyMatchesSQLConstraint does the same for the
// render_attempts status vocabulary.
func TestAttemptStatusVocabularyMatchesSQLConstraint(t *testing.T) {
	want := make([]string, 0, 8)
	for _, s := range AttemptStatuses() {
		want = append(want, string(s))
	}
	got := latestCheck(t, "render_attempts_status_check")
	if strings.Join(sortedStrings(got), ",") != strings.Join(sortedStrings(want), ",") {
		t.Fatalf("render_attempts_status_check %v != Go AttemptStatuses() %v", sortedStrings(got), sortedStrings(want))
	}
}

// TestStateAliasesMatchClientType pins that the internal aliases are the
// canonical wire values, not a second definition that happens to agree today.
// notifyInList matches the state list of a `NEW.state IN ('a', 'b', ...)`
// predicate inside the notify trigger function.
var notifyInList = regexp.MustCompile(`IN\s*\(([^()]*)\)`)

// latestNotifyStateSet extracts the state list of the most recent
// `CREATE OR REPLACE FUNCTION notify_rendering_jobs()` across the migration
// files, in lexicographic (apply) order — the list the migrated database ends
// up with. Every IN(...) occurrence inside that migration must agree; the
// function carries the same predicate in the INSERT and the UPDATE branch.
func latestNotifyStateSet(t *testing.T) []string {
	t.Helper()
	names, err := migrations.Names()
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	var latest [][]string
	for _, name := range names {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(raw), "notify_rendering_jobs") {
			continue
		}
		matches := notifyInList.FindAllStringSubmatch(string(raw), -1)
		if len(matches) == 0 {
			continue
		}
		lists := make([][]string, 0, len(matches))
		for _, m := range matches {
			var values []string
			for _, part := range strings.Split(m[1], ",") {
				v := strings.TrimSpace(part)
				v = strings.Trim(v, "'\"")
				if v != "" {
					values = append(values, v)
				}
			}
			if len(values) > 0 {
				lists = append(lists, values)
			}
		}
		if len(lists) > 0 {
			latest = lists
		}
	}
	if len(latest) == 0 {
		t.Fatal("no migration defines the notify_rendering_jobs trigger function")
	}
	want := strings.Join(sortedStrings(latest[0]), ",")
	for _, got := range latest[1:] {
		if joined := strings.Join(sortedStrings(got), ","); joined != want {
			t.Fatalf("notify_rendering_jobs has divergent state lists: %q vs %q", want, joined)
		}
	}
	return latest[0]
}

// TestNotifyTriggerVocabularyMatchesWireStates pins the state vocabulary the
// PostgreSQL NOTIFY trigger carries to the canonical wire vocabulary.
//
// The trigger wakes long-poll waiters on the states that (a) make a job
// claimable (pending, rendered — the set ClaimState selects) and (b) end a
// producer wait (the terminal states, owned by client.IsTerminalState). It is a
// second carrier of the same decision the Go service makes, and nothing else
// compares the two: adding or renaming a state in Go without updating the
// trigger silently degrades cross-replica wake-ups to the bounded re-poll, so
// the drift is a latency bug, not a loud failure. This is that comparison.
func TestNotifyTriggerVocabularyMatchesWireStates(t *testing.T) {
	got := latestNotifyStateSet(t)

	wantSet := map[string]bool{
		string(client.StatePending):  true, // claimable
		string(client.StateRendered): true, // claimable (publication-only retry)
	}
	for _, s := range client.AllStates() {
		if client.IsTerminalState(s) {
			wantSet[string(s)] = true
		}
	}
	want := make([]string, 0, len(wantSet))
	for s := range wantSet {
		want = append(want, s)
	}

	if strings.Join(sortedStrings(got), ",") != strings.Join(sortedStrings(want), ",") {
		t.Fatalf("notify_rendering_jobs states %v != claimable ∪ terminal %v (owned by ClaimState and client.IsTerminalState)",
			sortedStrings(got), sortedStrings(want))
	}
}

// TestMaxAttemptsDefaultMatchesMigration pins DefaultMaxAttempts to the
// `render_jobs.max_attempts DEFAULT` in the migration chain. The CLI flag
// default, both repository backends and the historical schema all derive from
// this one value; without the pin, changing the Go constant leaves the column
// default on the old policy for every job inserted without an explicit value.
func TestMaxAttemptsDefaultMatchesMigration(t *testing.T) {
	names, err := migrations.Names()
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	re := regexp.MustCompile(`max_attempts\s+INTEGER\s+NOT\s+NULL\s+DEFAULT\s+(\d+)`)
	var found bool
	for _, name := range names {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		m := re.FindSubmatch(raw)
		if m == nil {
			continue
		}
		found = true
		got, err := strconv.Atoi(string(m[1]))
		if err != nil {
			t.Fatalf("%s: parse max_attempts default: %v", name, err)
		}
		if got != DefaultMaxAttempts {
			t.Fatalf("%s: max_attempts DEFAULT %d != model.DefaultMaxAttempts %d", name, got, DefaultMaxAttempts)
		}
	}
	if !found {
		t.Fatal("no migration declares render_jobs.max_attempts DEFAULT")
	}
}

func TestIsCancelableMatchesTerminalState(t *testing.T) {
	for _, state := range append(States(), State("unknown")) {
		if got, want := IsCancelable(state), !IsTerminalState(state); got != want {
			t.Errorf("IsCancelable(%q) = %t, want %t", state, got, want)
		}
	}
}

func TestStateAliasesMatchClientType(t *testing.T) {
	pairs := map[string]struct{ alias, canonical string }{
		"pending":    {string(StatePending), "pending"},
		"running":    {string(StateRunning), "running"},
		"finalizing": {string(StateFinalizing), "finalizing"},
		"completed":  {string(StateCompleted), "completed"},
		"failed":     {string(StateFailed), "failed"},
		"cancelled":  {string(StateCancelled), "cancelled"},
		"rendered":   {string(StateRendered), "rendered"},
	}
	for name, p := range pairs {
		if p.alias != p.canonical {
			t.Errorf("%s: alias %q != canonical %q", name, p.alias, p.canonical)
		}
	}
}
