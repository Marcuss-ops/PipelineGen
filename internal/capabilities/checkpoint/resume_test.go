package checkpoint

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// ── CanResume ─────────────────────────────────────────────────────────

func TestCanResumeRequiresEveryDimension(t *testing.T) {
	base := validCheckpoint()
	expected := ExpectedInput{InputFingerprint: base.InputFingerprint, ProcessorVersion: base.ProcessorVersion}
	artifact := ArtifactStatus{Exists: true, SHA256Matches: true}
	notCompleted := base
	notCompleted.Status = "RUNNING"
	changedFingerprint := base
	changedFingerprint.InputFingerprint = hash64('9')
	changedProcessor := base
	changedProcessor.ProcessorVersion = "rust-render/v4"

	cases := map[string]struct {
		cp       *Checkpoint
		expected ExpectedInput
		artifact ArtifactStatus
	}{
		"valid checkpoint":                {cp: &base, expected: expected, artifact: artifact},
		"nil checkpoint":                  {cp: nil, expected: expected, artifact: artifact},
		"not completed":                   {cp: &notCompleted, expected: expected, artifact: artifact},
		"fingerprint changed":             {cp: &base, expected: ExpectedInput{InputFingerprint: hash64('9'), ProcessorVersion: base.ProcessorVersion}, artifact: artifact},
		"processor version changed":       {cp: &base, expected: ExpectedInput{InputFingerprint: base.InputFingerprint, ProcessorVersion: "rust-render/v4"}, artifact: artifact},
		"artifact missing":                {cp: &base, expected: expected, artifact: ArtifactStatus{Exists: false, SHA256Matches: true}},
		"artifact hash mismatch":          {cp: &base, expected: expected, artifact: ArtifactStatus{Exists: true, SHA256Matches: false}},
		"artifact missing and mismatched": {cp: &base, expected: expected, artifact: ArtifactStatus{Exists: false, SHA256Matches: false}},
	}
	for name, tc := range cases {
		want, reason := CanResume(tc.cp, tc.expected, tc.artifact)
		if name == "valid checkpoint" {
			if !want {
				t.Fatalf("valid checkpoint must resume: %s", reason)
			}
			continue
		}
		if want {
			t.Errorf("%s must NOT resume", name)
		}
	}
}

func TestCanResumeSkipsArtifactChecksForArtifactlessCheckpoints(t *testing.T) {
	cp := validCheckpoint()
	cp.ArtifactSHA256 = ""
	cp.ArtifactURI = ""
	expected := ExpectedInput{InputFingerprint: cp.InputFingerprint, ProcessorVersion: cp.ProcessorVersion}
	ok, reason := CanResume(&cp, expected, ArtifactStatus{Exists: false, SHA256Matches: false})
	if !ok {
		t.Fatalf("artifactless checkpoint must resume without artifact checks: %s", reason)
	}
}

// ── Resolver.Decide ───────────────────────────────────────────────────

// memStore is an in-memory Store for resolver tests.
type memStore struct {
	mu   sync.Mutex
	rows map[string]Checkpoint
}

func newMemStore() *memStore { return &memStore{rows: map[string]Checkpoint{}} }

func key(jobID, stage, unitID string) string { return jobID + "|" + stage + "|" + unitID }

func (m *memStore) Get(_ context.Context, jobID, stage, unitID string) (*Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.rows[key(jobID, stage, unitID)]; ok {
		copy := c
		return &copy, nil
	}
	return nil, nil
}

func (m *memStore) Complete(_ context.Context, c Checkpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[key(c.JobID, c.Stage, c.UnitID)] = c
	return nil
}

func (m *memStore) Invalidate(_ context.Context, jobID, stage, unitID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, key(jobID, stage, unitID))
	return nil
}

func (m *memStore) get(jobID, stage, unitID string) *Checkpoint {
	c, _ := m.Get(context.Background(), jobID, stage, unitID)
	return c
}

var _ Store = (*memStore)(nil)

// stubVerifier returns the configured status or error.
type stubVerifier struct {
	status ArtifactStatus
	err    error
}

func (v stubVerifier) VerifyArtifact(context.Context, string, string) (ArtifactStatus, error) {
	return v.status, v.err
}

var _ ArtifactVerifier = stubVerifier{}

func expectedFor(c Checkpoint) ExpectedInput {
	return ExpectedInput{InputFingerprint: c.InputFingerprint, ProcessorVersion: c.ProcessorVersion}
}

func TestResolverDecideSkipsValidCheckpoint(t *testing.T) {
	store := newMemStore()
	cp := validCheckpoint()
	if err := store.Complete(context.Background(), cp); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(store, stubVerifier{status: ArtifactStatus{Exists: true, SHA256Matches: true}})
	decision, _, err := resolver.Decide(context.Background(), cp.JobID, cp.Stage, cp.UnitID, expectedFor(cp))
	if err != nil {
		t.Fatal(err)
	}
	if decision != DecisionSkip {
		t.Fatalf("valid checkpoint must SKIP, got %s", decision)
	}
	if store.get(cp.JobID, cp.Stage, cp.UnitID) == nil {
		t.Fatal("SKIP must not remove the checkpoint")
	}
}

func TestResolverDecideExecuteAndInvalidateOnStaleCheckpoint(t *testing.T) {
	cases := map[string]struct {
		mutate       func(*Checkpoint)
		expected     func(Checkpoint) ExpectedInput
		verifier     ArtifactVerifier
		skipComplete bool
	}{
		"no checkpoint":          {expected: func(c Checkpoint) ExpectedInput { return expectedFor(c) }, skipComplete: true},
		"fingerprint changed":    {expected: func(c Checkpoint) ExpectedInput { e := expectedFor(c); e.InputFingerprint = hash64('9'); return e }},
		"processor changed":      {expected: func(c Checkpoint) ExpectedInput { e := expectedFor(c); e.ProcessorVersion = "v9"; return e }},
		"artifact missing":       {verifier: stubVerifier{status: ArtifactStatus{Exists: false, SHA256Matches: true}}},
		"artifact hash mismatch": {verifier: stubVerifier{status: ArtifactStatus{Exists: true, SHA256Matches: false}}},
	}
	for name, tc := range cases {
		store := newMemStore()
		cp := validCheckpoint()
		if tc.mutate != nil {
			tc.mutate(&cp)
		}
		if !tc.skipComplete {
			if err := store.Complete(context.Background(), cp); err != nil {
				t.Fatal(err)
			}
		}
		verifier := tc.verifier
		if verifier == nil {
			verifier = stubVerifier{status: ArtifactStatus{Exists: true, SHA256Matches: true}}
		}
		resolver := NewResolver(store, verifier)
		expected := expectedFor(cp)
		if tc.expected != nil {
			expected = tc.expected(cp)
		}
		decision, _, err := resolver.Decide(context.Background(), cp.JobID, cp.Stage, cp.UnitID, expected)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if decision != DecisionExecute {
			t.Fatalf("%s: stale checkpoint must EXECUTE, got %s", name, decision)
		}
		if name != "no checkpoint" && store.get(cp.JobID, cp.Stage, cp.UnitID) != nil {
			t.Fatalf("%s: stale checkpoint must be invalidated", name)
		}
	}
}

func TestResolverDecideNeverSkipsOnUnverifiableArtifact(t *testing.T) {
	store := newMemStore()
	cp := validCheckpoint()
	if err := store.Complete(context.Background(), cp); err != nil {
		t.Fatal(err)
	}
	// Verifier outage: EXECUTE but the record is preserved (cannot judge it).
	resolver := NewResolver(store, stubVerifier{err: errors.New("cas down")})
	decision, _, err := resolver.Decide(context.Background(), cp.JobID, cp.Stage, cp.UnitID, expectedFor(cp))
	if err != nil {
		t.Fatal(err)
	}
	if decision == DecisionSkip {
		t.Fatal("must never SKIP when the artifact cannot be verified")
	}
	if store.get(cp.JobID, cp.Stage, cp.UnitID) == nil {
		t.Fatal("unverifiable checkpoint must not be invalidated")
	}
	// Verifier not configured: same behavior.
	resolver = NewResolver(store, nil)
	decision, _, err = resolver.Decide(context.Background(), cp.JobID, cp.Stage, cp.UnitID, expectedFor(cp))
	if err != nil {
		t.Fatal(err)
	}
	if decision == DecisionSkip {
		t.Fatal("must never SKIP without an artifact verifier")
	}
}

func TestResolverDecideUnwiredStoreAlwaysExecutes(t *testing.T) {
	resolver := NewResolver(nil, nil)
	decision, _, err := resolver.Decide(context.Background(), "job", StageAudio, UnitGlobal, ExpectedInput{InputFingerprint: hash64('1'), ProcessorVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if decision != DecisionExecute {
		t.Fatalf("unwired store must EXECUTE, got %s", decision)
	}
	if err := resolver.Complete(context.Background(), validCheckpoint()); err == nil {
		t.Fatal("Complete on an unwired store must fail")
	}
}

func TestResolverDecideStoreFailureIsHardError(t *testing.T) {
	failing := &memStore{}
	store := failingStore{failing}
	resolver := NewResolver(store, nil)
	if _, _, err := resolver.Decide(context.Background(), "job", StageAudio, UnitGlobal, ExpectedInput{InputFingerprint: hash64('1'), ProcessorVersion: "v1"}); err == nil {
		t.Fatal("store failure must be a hard error, never a silent decision")
	}
}

type failingStore struct{ *memStore }

func (failingStore) Get(context.Context, string, string, string) (*Checkpoint, error) {
	return nil, errors.New("db down")
}
func (failingStore) Complete(context.Context, Checkpoint) error { return errors.New("db down") }
func (failingStore) Invalidate(context.Context, string, string, string) error {
	return errors.New("db down")
}

var _ Store = failingStore{}

func TestResolverCompleteRecordsUnit(t *testing.T) {
	store := newMemStore()
	resolver := NewResolver(store, nil)
	cp := validCheckpoint()
	if err := resolver.Complete(context.Background(), cp); err != nil {
		t.Fatal(err)
	}
	got := store.get(cp.JobID, cp.Stage, cp.UnitID)
	if got == nil || got.InputFingerprint != cp.InputFingerprint || got.ArtifactSHA256 != cp.ArtifactSHA256 {
		t.Fatalf("complete must record the unit: %+v", got)
	}
}

func TestResolverCompleteRejectsInvalidCheckpoint(t *testing.T) {
	store := newMemStore()
	resolver := NewResolver(store, nil)
	cp := validCheckpoint()
	cp.CompletedAt = time.Time{}
	if err := resolver.Complete(context.Background(), cp); err == nil {
		t.Fatal("invalid checkpoint must not be recorded")
	}
}
