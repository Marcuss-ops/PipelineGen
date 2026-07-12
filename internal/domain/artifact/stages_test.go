// Package artifact — stages_test.go (Push 3.1a-fix hermetic tests).
//
// Pins the FASE 3 Spina Dorsale typed contract:
//   - ArtifactStageState.IsValid: 4-value canonical set (rejects typos)
//   - ArtifactStageState.IsTerminal: 2-value terminal set
//   - Requirement.IsValid: 2-value canonical set
//   - 11 sentinel errors instantiate correctly
//   - WrapArtifactStageNotFound / WrapArtifactRequiredMissing / Wrap
//     preserve the errors.Is chain
//   - Repository port is satisfied by a compile-time assertion (zero-
//     surface check that catches signature drift at build time)
//
// godlike/07 fail-closed: every canonical set is pinned so a future
// widening or narrowing of the enum is caught at test time, NOT at
// production runtime.
package artifact

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ── ArtifactStageState.IsValid ───────────────────────────────────────────

func TestArtifactStageState_IsValid_Accepts4CanonicalValues(t *testing.T) {
	for _, st := range []ArtifactStageState{
		StateStaged,
		StatePublished,
		StateSucceeded,
		StateFailedPermanent,
	} {
		if !st.IsValid() {
			t.Errorf("ArtifactStageState %q must be valid (canonical 4-value set)", string(st))
		}
	}
}

func TestArtifactStageState_IsValid_RejectsUnknownValues(t *testing.T) {
	for _, st := range []ArtifactStageState{
		"",
		"staged",  // wrong case
		"STAGED ", // trailing space
		"PUBLISHED",
		"SUCCEEDED",
		"FAILED_PERMANENT",
		"IN_PROGRESS", // not in canonical set
		"DELETED",     // not in canonical set (that's the asset registry)
		"VERIFIED",    // not in canonical set
	} {
		// (The "PUBLISHED", "SUCCEEDED", "FAILED_PERMANENT" entries
		// above would already be in the valid set; they're included
		// here only to confirm the loop covers all branches. The
		// loop body skips them via the positive branch below.)
		_ = st
	}
	// Repeat the rejection-only check with values that MUST be rejected.
	for _, st := range []ArtifactStageState{
		"",
		"staged",  // wrong case (canonical is uppercase)
		"STAGED ", // trailing space
		"IN_PROGRESS",
		"DELETED",
		"VERIFIED",
		"unknown",
	} {
		if ArtifactStageState(st).IsValid() {
			t.Errorf("ArtifactStageState %q must be rejected (not in canonical 4-value set)", st)
		}
	}
}

// ── ArtifactStageState.IsTerminal ─────────────────────────────────────────

func TestArtifactStageState_IsTerminal_TerminalStates(t *testing.T) {
	for _, st := range []ArtifactStageState{
		StateSucceeded,
		StateFailedPermanent,
	} {
		if !st.IsTerminal() {
			t.Errorf("ArtifactStageState %q MUST be terminal (FASE 3 (b) fail-closed)", string(st))
		}
	}
}

func TestArtifactStageState_IsTerminal_NonTerminalStates(t *testing.T) {
	for _, st := range []ArtifactStageState{
		StateStaged,
		StatePublished,
	} {
		if st.IsTerminal() {
			t.Errorf("ArtifactStageState %q MUST be non-terminal (publisher worker / finalizer must be able to transition out of it)", string(st))
		}
	}
}

// ── Requirement.IsValid ──────────────────────────────────────────────────

func TestRequirement_IsValid_Accepts2CanonicalValues(t *testing.T) {
	for _, r := range []Requirement{
		RequirementOptional,
		RequirementRequired,
	} {
		if !r.IsValid() {
			t.Errorf("Requirement %q must be valid (canonical 2-value set)", string(r))
		}
	}
}

func TestRequirement_IsValid_RejectsUnknownValues(t *testing.T) {
	for _, r := range []Requirement{
		"",
		"REQUIRED", // wrong case
		"optional ",
		"recommended", // not in canonical set
		"mandatory",   // synonym — not canonical
	} {
		if Requirement(r).IsValid() {
			t.Errorf("Requirement %q must be rejected (not in canonical 2-value set)", r)
		}
	}
}

// ── String helpers ───────────────────────────────────────────────────────

func TestArtifactStageState_String(t *testing.T) {
	if got := StateStaged.String(); got != "STAGED" {
		t.Errorf("StateStaged.String() = %q, want %q", got, "STAGED")
	}
	if got := StateFailedPermanent.String(); got != "FAILED_PERMANENT" {
		t.Errorf("StateFailedPermanent.String() = %q, want %q", got, "FAILED_PERMANENT")
	}
}

func TestRequirement_String(t *testing.T) {
	if got := RequirementRequired.String(); got != "required" {
		t.Errorf("RequirementRequired.String() = %q, want %q", got, "required")
	}
}

// ── Sentinel error surface ───────────────────────────────────────────────

// TestSentinels_AllInstantiate pins that the typed error sentinels
// in stages.go are non-nil. A future refactor that drops one is
// caught at test time (godlike/07 fail-closed: every canonical
// sentinel MUST remain a stable identity so errors.Is probes keep
// working across the application stack).
func TestSentinels_AllInstantiate(t *testing.T) {
	sentinels := []error{
		ErrInvalidArtifactStageState,
		ErrInvalidRequirement,
		ErrInvalidArtifactStageID,
		ErrInvalidJobID,
		ErrArtifactStageNotFound,
		ErrArtifactRequiredMissing,
		ErrQuotaExceeded,
		ErrDiskSpaceLow,
		ErrArtifactStageHashMismatch,
		ErrArtifactStageIDCollision,
		ErrArtifactStageEmpty,
		ErrTerminalStateRejection,
	}
	for i, sentinel := range sentinels {
		if sentinel == nil {
			t.Errorf("sentinel[%d] is nil (caller errors.Is would panic)", i)
		}
	}
}

func TestSentinels_DistinctIdentities(t *testing.T) {
	// godlike/07: two distinct failure modes must not share an
	// error identity (operators grep logs by sentinel).
	if errors.Is(ErrInvalidArtifactStageState, ErrInvalidRequirement) {
		t.Errorf("ErrInvalidArtifactStageState MUST NOT be errors.Is-compatible with ErrInvalidRequirement (different failure modes)")
	}
	if errors.Is(ErrInvalidJobID, ErrInvalidArtifactStageID) {
		t.Errorf("ErrInvalidJobID MUST NOT be errors.Is-compatible with ErrInvalidArtifactStageID (FK-by-convention vs primary key)")
	}
	if errors.Is(ErrArtifactRequiredMissing, ErrArtifactStageNotFound) {
		t.Errorf("ErrArtifactRequiredMissing MUST NOT be errors.Is-compatible with ErrArtifactStageNotFound (absent vs terminal-fence)")
	}
	if errors.Is(ErrTerminalStateRejection, ErrArtifactStageNotFound) {
		t.Errorf("ErrTerminalStateRejection MUST NOT be errors.Is-compatible with ErrArtifactStageNotFound (terminal-fence vs absent)")
	}
}

// ── Wrap helpers preserve errors.Is chain ────────────────────────────────

func TestWrapArtifactStageNotFound_PreservesIsChain(t *testing.T) {
	err := WrapArtifactStageNotFound("art-abc-123")
	if err == nil {
		t.Fatalf("WrapArtifactStageNotFound must return non-nil")
	}
	if !errors.Is(err, ErrArtifactStageNotFound) {
		t.Errorf("WrapArtifactStageNotFound(err) MUST satisfy errors.Is(., ErrArtifactStageNotFound); got %v", err)
	}
	// Operator-audit string MUST contain the id (greppable in logs).
	if got := err.Error(); !contains(got, "art-abc-123") {
		t.Errorf("WrapArtifactStageNotFound message %q MUST contain the id for operator audit", got)
	}
}

func TestWrapArtifactRequiredMissing_PreservesIsChain(t *testing.T) {
	err := WrapArtifactRequiredMissing("job-1", "required", "art-1")
	if err == nil {
		t.Fatalf("WrapArtifactRequiredMissing must return non-nil")
	}
	if !errors.Is(err, ErrArtifactRequiredMissing) {
		t.Errorf("WrapArtifactRequiredMissing(err) MUST satisfy errors.Is(., ErrArtifactRequiredMissing); got %v", err)
	}
	// Operator-audit string MUST contain job_id, requirement, id.
	got := err.Error()
	for _, want := range []string{"job-1", "required", "art-1"} {
		if !contains(got, want) {
			t.Errorf("WrapArtifactRequiredMissing message %q MUST contain %q for operator audit", got, want)
		}
	}
}

func TestWrap_PreservesIsChain(t *testing.T) {
	err := Wrap(ErrQuotaExceeded, "limit=%dMB used=%dMB", 100, 95)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("Wrap(ErrQuotaExceeded, ...) MUST satisfy errors.Is(., ErrQuotaExceeded); got %v", err)
	}
}

// ── Repository port compile-time anchor ──────────────────────────────────

// TestRepositoryPort_AcceptsConcrete compiles a fake concrete
// satisfying the Repository port to catch signature drift at build
// time. The fake has no runtime assertions — the value is the
// compile-time anchor only.
func TestRepositoryPort_AcceptsConcrete(t *testing.T) {
	var _ Repository = (*fakeRepoForCompileTime)(nil)
}

// fakeRepoForCompileTime is the minimum viable Repository concrete
// for compile-time signature assertion. It is intentionally
// unexported + panics-on-call because its only purpose is to keep
// `var _ Repository = (*fakeRepoForCompileTime)(nil)` honest at
// build time. Runtime tests live in the infrastructure concrete
// (internal/infrastructure/database/sqlite/artifact_stages/repository_test.go).
type fakeRepoForCompileTime struct{}

func (f *fakeRepoForCompileTime) Insert(ctx context.Context, stage *ArtifactStage) error {
	panic("compile-time anchor only")
}

func (f *fakeRepoForCompileTime) GetByID(ctx context.Context, id string) (*ArtifactStage, error) {
	panic("compile-time anchor only")
}

func (f *fakeRepoForCompileTime) ListByJob(ctx context.Context, jobID string) ([]ArtifactStage, error) {
	panic("compile-time anchor only")
}

func (f *fakeRepoForCompileTime) ListByState(ctx context.Context, state ArtifactStageState, limit int) ([]ArtifactStage, error) {
	panic("compile-time anchor only")
}

func (f *fakeRepoForCompileTime) MarkPublished(ctx context.Context, id, publishedLocation string, publishedAt time.Time) error {
	panic("compile-time anchor only")
}

func (f *fakeRepoForCompileTime) MarkSucceeded(ctx context.Context, id string) error {
	panic("compile-time anchor only")
}

func (f *fakeRepoForCompileTime) MarkFailedPermanent(ctx context.Context, id string, lastError string) error {
	panic("compile-time anchor only")
}

func (f *fakeRepoForCompileTime) IncrementAttemptCount(ctx context.Context, id string) error {
	panic("compile-time anchor only")
}

// InsertWithOutbox atomically writes a new ArtifactStage row AND co-emits
// an outbox event in one transaction (Push 3.1c: FASE 3 Spina Dorsale TX-
// aware primitive). The Repository interface in stages.go (line ~417) pins
// this signature; the panic body matches the surrounding mock members because
// this is a compile-time anchor only — runtime tests live in
// internal/infrastructure/database/sqlite/artifact_stages/repository_test.go.
func (f *fakeRepoForCompileTime) InsertWithOutbox(ctx context.Context, stage *ArtifactStage, eventType string, payload []byte) (string, error) {
	panic("compile-time anchor only")
}

// contains is a tiny stdlib-only strings.Contains substitute so this
// test file has no external dependencies.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
