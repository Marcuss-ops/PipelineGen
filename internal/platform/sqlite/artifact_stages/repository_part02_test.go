package artifactstages

import (
	"context"
	"errors"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	_ "github.com/mattn/go-sqlite3"
	"testing"
	"time"
)

func TestRepository_ListByJob_OrderedByCreatedAt(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	// Seed 3 rows for the same job in non-monotonic order; the
	// repository MUST return them in created_at ASC.
	seeds := []*artifact.ArtifactStage{
		func() *artifact.ArtifactStage {
			s := validStage()
			s.ID = "art-3"
			return s
		}(),
		func() *artifact.ArtifactStage {
			s := validStage()
			s.ID = "art-1"
			return s
		}(),
		func() *artifact.ArtifactStage {
			s := validStage()
			s.ID = "art-2"
			return s
		}(),
	}
	for i, s := range seeds {
		// Stagger created_at so ordering is observable.
		s.CreatedAt = nowFixed.Add(time.Duration(i) * time.Second)
		if err := repo.Insert(ctx, s); err != nil {
			t.Fatalf("seed[%d] insert: %v", i, err)
		}
	}
	got, err := repo.ListByJob(ctx, "job-test-1")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListByJob: got %d rows, want 3", len(got))
	}
	// Order is created_at ASC → art-3 (0s), art-1 (1s), art-2 (2s).
	// The seed loop sets CreatedAt = nowFixed + i*Second for
	// seeds[0]=art-3, seeds[1]=art-1, seeds[2]=art-2; SQL ORDER BY
	// created_at ASC therefore returns them in seed-array order,
	// NOT in lexicographic ID order.
	wantOrder := []string{"art-3", "art-1", "art-2"}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("ListByJob[%d].ID = %q, want %q", i, got[i].ID, want)
		}
	}
}

// ── ListByState ────────────────────────────────────────────────────────

func TestRepository_ListByState_RejectsInvalidState(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	_, err := repo.ListByState(context.Background(), "IN_PROGRESS", 10)
	if !errors.Is(err, artifact.ErrInvalidArtifactStageState) {
		t.Errorf("ListByState bogus: err = %v, want ErrInvalidArtifactStageState", err)
	}
}

func TestRepository_ListByState_ReturnsOnlyMatchingState(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	// Seed 2 STAGED + 1 PUBLISHED rows.
	for _, id := range []string{"art-s1", "art-s2"} {
		s := validStage()
		s.ID = id
		if err := repo.Insert(ctx, s); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	pub := validStage()
	pub.ID = "art-p1"
	if err := repo.Insert(ctx, pub); err != nil {
		t.Fatalf("seed pub: %v", err)
	}
	if err := repo.MarkPublished(ctx, pub.ID, `{"kind":"drive","uri":"file-1"}`, nowFixed); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}
	got, err := repo.ListByState(ctx, artifact.ArtifactStageStateStaged, 10)
	if err != nil {
		t.Fatalf("ListByState STAGED: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListByState STAGED: got %d rows, want 2", len(got))
	}
	for _, s := range got {
		if s.State != artifact.ArtifactStageStateStaged {
			t.Errorf("ListByState STAGED: row %q has state %q", s.ID, s.State)
		}
	}
}

// ── MarkPublished ─────────────────────────────────────────────────────

func TestRepository_MarkPublished_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	pubAt := nowFixed.Add(time.Minute)
	if err := repo.MarkPublished(ctx, s.ID, `{"kind":"drive","uri":"f-1"}`, pubAt); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.State != artifact.ArtifactStageStatePublished {
		t.Errorf("State = %q, want PUBLISHED", got.State)
	}
	if got.PublishedLocation != `{"kind":"drive","uri":"f-1"}` {
		t.Errorf("PublishedLocation = %q, want canonical", got.PublishedLocation)
	}
	if got.PublishedAt == nil || !got.PublishedAt.Equal(pubAt) {
		t.Errorf("PublishedAt = %v, want %v", got.PublishedAt, pubAt)
	}
}

func TestRepository_MarkPublished_RejectsOnTerminalState(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, s.ID); err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	// Now MarkPublished MUST be rejected by the terminal-state fence.
	if err := repo.MarkPublished(ctx, s.ID, `{"x":1}`, nowFixed); !errors.Is(err, artifact.ErrTerminalStateRejection) {
		t.Errorf("MarkPublished on terminal: err = %v, want ErrTerminalStateRejection", err)
	}
}

// TestRepository_MarkPublished_RejectsOnPublishedState pins the
// Path-B invariant: MarkPublished on a row already in PUBLISHED
// state MUST return ErrTerminalStateRejection (not silently
// overwrite published_location/published_at). The fence is the
// broadest of the Mark* methods (state NOT IN
// (`PUBLISHED','SUCCEEDED','FAILED_PERMANENT')) because re-publishing
// from PUBLISHED would silently duplicate-upload to Drive (the
// Publisher worker has the cross-session dedup IdempotencyKey,
// but a duplicate CAS still costs a Drive-side PutFile call before
// the fence fires). Pre-Path-B (Push 3.1a baseline), this test
// would have FAILED — the baseline fence was missing 'PUBLISHED'
// and a second drain on a PUBLISHED row silently overwrote.
func TestRepository_MarkPublished_RejectsOnPublishedState(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()

	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	// First MarkPublished: STAGED → PUBLISHED (happy path).
	if err := repo.MarkPublished(ctx, s.ID, `{"kind":"drive","uri":"first"}`, nowFixed); err != nil {
		t.Fatalf("first MarkPublished: %v", err)
	}
	// Second MarkPublished on the now-PUBLISHED row: MUST be rejected.
	if err := repo.MarkPublished(ctx, s.ID, `{"kind":"drive","uri":"second"}`, nowFixed); !errors.Is(err, artifact.ErrTerminalStateRejection) {
		t.Errorf("MarkPublished on PUBLISHED: err = %v, want ErrTerminalStateRejection (Path-B invariant: re-deliveries on PUBLISHED state are typed no-ops, not silent overwrites)", err)
	}
	// PublishedLocation MUST NOT have been overwritten by the
	// rejected second call (the canonical 'first' uri remains).
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetByID post-rejection: %v", err)
	}
	if got.PublishedLocation != `{"kind":"drive","uri":"first"}` {
		t.Errorf("PublishedLocation = %q, want canonical first-write value (rejected Call must NOT have overwritten)", got.PublishedLocation)
	}
	if got.State != artifact.ArtifactStageStatePublished {
		t.Errorf("State = %q, want PUBLISHED (terminal-state rejection must preserve the existing row state)", got.State)
	}
}

func TestRepository_MarkPublished_NotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	err := repo.MarkPublished(context.Background(), "art-missing", `{}`, nowFixed)
	if !errors.Is(err, artifact.ErrArtifactStageNotFound) {
		t.Errorf("MarkPublished on missing row: err = %v, want ErrArtifactStageNotFound", err)
	}
}

// ── MarkSucceeded ─────────────────────────────────────────────────────

func TestRepository_MarkSucceeded_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, s.ID); err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	got, _ := repo.GetByID(ctx, s.ID)
	if got.State != artifact.ArtifactStageStateSucceeded {
		t.Errorf("State = %q, want SUCCEEDED", got.State)
	}
}

func TestRepository_MarkSucceeded_RejectsAlreadyTerminal(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.MarkFailedPermanent(ctx, s.ID, "drive 5xx"); err != nil {
		t.Fatalf("MarkFailedPermanent: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, s.ID); !errors.Is(err, artifact.ErrTerminalStateRejection) {
		t.Errorf("MarkSucceeded on FAILED_PERMANENT: err = %v, want ErrTerminalStateRejection", err)
	}
}

// ── MarkFailedPermanent ───────────────────────────────────────────────

func TestRepository_MarkFailedPermanent_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.MarkFailedPermanent(ctx, s.ID, "hash mismatch on re-read"); err != nil {
		t.Fatalf("MarkFailedPermanent: %v", err)
	}
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.State != artifact.ArtifactStageStateFailedPermanent {
		t.Errorf("State = %q, want FAILED_PERMANENT", got.State)
	}
	if got.LastError != "hash mismatch on re-read" {
		t.Errorf("LastError = %q, want canonical", got.LastError)
	}
}

// ── IncrementAttemptCount ─────────────────────────────────────────────

func TestRepository_IncrementAttemptCount_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	for i, want := range []int{1, 2, 3} {
		if err := repo.IncrementAttemptCount(ctx, s.ID); err != nil {
			t.Fatalf("IncrementAttemptCount[%d]: %v", i, err)
		}
		got, _ := repo.GetByID(ctx, s.ID)
		if got.AttemptCount != want {
			t.Errorf("IncrementAttemptCount[%d]: attempt_count = %d, want %d", i, got.AttemptCount, want)
		}
	}
}
