package entitycatalog

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type recertificationTestRepository struct {
	mu          sync.Mutex
	candidates  []RecertificationCandidate
	validations []ValidationResult
}

func (r *recertificationTestRepository) UpsertEntity(context.Context, Entity) error { return nil }
func (r *recertificationTestRepository) GetEntity(context.Context, string) (*Entity, error) {
	return nil, nil
}
func (r *recertificationTestRepository) SetRefreshState(context.Context, string, string, time.Time, string) error {
	return nil
}
func (r *recertificationTestRepository) UpsertCandidate(context.Context, Candidate) (int64, error) {
	return 1, nil
}
func (r *recertificationTestRepository) SetCandidateStatus(context.Context, int64, string) error {
	return nil
}
func (r *recertificationTestRepository) ListCandidates(context.Context, string, int) ([]Candidate, error) {
	return nil, nil
}
func (r *recertificationTestRepository) UpsertMaterialization(context.Context, Materialization) error {
	return nil
}
func (r *recertificationTestRepository) GetMaterialization(context.Context, int64) (*Materialization, error) {
	return nil, nil
}
func (r *recertificationTestRepository) ListCandidatesForRecertification(_ context.Context, _ time.Time, limit, _ int) ([]RecertificationCandidate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit > len(r.candidates) {
		limit = len(r.candidates)
	}
	return append([]RecertificationCandidate(nil), r.candidates[:limit]...), nil
}
func (r *recertificationTestRepository) RecordCandidateValidation(_ context.Context, _ int64, result ValidationResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.validations = append(r.validations, result)
	return nil
}

func TestRecertificationRunOnceRefreshesStaleCandidate(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	repo := &recertificationTestRepository{candidates: []RecertificationCandidate{{
		Candidate: Candidate{ID: 7, CanonicalEntityID: "person:michael-jordan", Provider: "duckduckgo", Rank: 1, SourceURL: "https://images.example/mj.png", Status: CandidateStatusStale, SemanticStatus: CandidateSemanticAccepted},
	}}}
	validator := imageCandidateValidatorFunc(func(context.Context, string) error { return nil })
	service := NewRecertificationService(repo, validator, RecertificationConfig{BatchSize: 10})
	service.now = func() time.Time { return now }

	report, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Selected != 1 || report.Succeeded != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(repo.validations) != 1 || !repo.validations[0].Success || repo.validations[0].FailureCount != 0 {
		t.Fatalf("validation = %+v", repo.validations)
	}
}

func TestRecertificationBrokenCandidateUsesExponentialRetryAndStopsAtMaxAttempts(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	repo := &recertificationTestRepository{candidates: []RecertificationCandidate{{
		Candidate:    Candidate{ID: 8, SourceURL: "https://images.example/broken.png", Status: CandidateStatusBroken, SemanticStatus: CandidateSemanticAccepted},
		FailureCount: 1,
	}}}
	service := NewRecertificationService(repo, imageCandidateValidatorFunc(func(context.Context, string) error { return errors.New("HTTP 503") }), RecertificationConfig{MaxAttempts: 3, InitialBackoff: time.Hour, MaxBackoff: 4 * time.Hour})
	service.now = func() time.Time { return now }

	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := repo.validations[0]
	if result.Success || result.FailureCount != 2 || !result.NextRetryAt.Equal(now.Add(2*time.Hour)) || !strings.Contains(result.Error, "HTTP 503") {
		t.Fatalf("retry result = %+v", result)
	}

	repo.validations = nil
	repo.candidates[0].FailureCount = 2
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	result = repo.validations[0]
	if result.Success || result.FailureCount != 3 || !result.NextRetryAt.IsZero() {
		t.Fatalf("terminal retry result = %+v", result)
	}
}

type imageCandidateValidatorFunc func(context.Context, string) error

func (f imageCandidateValidatorFunc) Validate(ctx context.Context, url string) error {
	return f(ctx, url)
}

func TestRecertificationRunOnceRejectsConcurrentRun(t *testing.T) {
	repo := &recertificationTestRepository{candidates: []RecertificationCandidate{{
		Candidate: Candidate{ID: 11, SourceURL: "https://images.example/concurrent.png", Status: CandidateStatusStale, SemanticStatus: CandidateSemanticAccepted},
	}}}
	entered := make(chan struct{})
	release := make(chan struct{})
	validator := imageCandidateValidatorFunc(func(context.Context, string) error {
		close(entered)
		<-release
		return nil
	})
	service := NewRecertificationService(repo, validator, RecertificationConfig{BatchSize: 1, BatchTimeout: time.Minute})
	done := make(chan error, 1)
	go func() { _, err := service.RunOnce(context.Background()); done <- err }()
	<-entered

	if _, err := service.RunOnce(context.Background()); !errors.Is(err, ErrRecertificationAlreadyRunning) {
		t.Fatalf("overlapping run error = %v, want %v", err, ErrRecertificationAlreadyRunning)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first run: %v", err)
	}
}

func TestRecertificationRunOnceHonorsBatchTimeout(t *testing.T) {
	repo := &recertificationTestRepository{candidates: []RecertificationCandidate{{
		Candidate: Candidate{ID: 12, SourceURL: "https://images.example/slow.png", Status: CandidateStatusStale, SemanticStatus: CandidateSemanticAccepted},
	}}}
	validator := imageCandidateValidatorFunc(func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	})
	service := NewRecertificationService(repo, validator, RecertificationConfig{BatchSize: 1, BatchTimeout: 20 * time.Millisecond})

	if _, err := service.RunOnce(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("batch timeout error = %v, want %v", err, context.DeadlineExceeded)
	}
	if len(repo.validations) != 0 {
		t.Fatalf("timeout recorded %d candidate failures, want 0", len(repo.validations))
	}
}

func TestHTTPImageCandidateValidatorChecksDownloadDecodeAndDimensions(t *testing.T) {
	httpImageCandidateAllowLoopbackForTest = true
	defer func() { httpImageCandidateAllowLoopbackForTest = false }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/small.png" {
			img := image.NewRGBA(image.Rect(0, 0, 100, 100))
			_ = png.Encode(w, img)
			return
		}
		img := image.NewRGBA(image.Rect(0, 0, 1000, 500))
		img.Set(0, 0, color.RGBA{R: 255, A: 255})
		_ = png.Encode(w, img)
	}))
	defer server.Close()
	validator := NewHTTPImageCandidateValidator(server.Client())
	if err := validator.Validate(context.Background(), server.URL+"/valid.png"); err != nil {
		t.Fatalf("valid image: %v", err)
	}
	if err := validator.Validate(context.Background(), server.URL+"/small.png"); err == nil {
		t.Fatal("small image unexpectedly accepted")
	}
}

// TestRecertificationBrokenCandidateRecoversOnSuccessfulProbe pins the
// service half of the recovery contract: a successful probe must not spend the
// candidate's entire retry budget just because the previous Drive asset was
// broken. The URL is healthy again; the finalizer owns re-materialization.
func TestRecertificationBrokenCandidateRecoversOnSuccessfulProbe(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	repo := &recertificationTestRepository{candidates: []RecertificationCandidate{{
		Candidate:    Candidate{ID: 9, SourceURL: "https://images.example/recovered.png", Status: CandidateStatusBroken, SemanticStatus: CandidateSemanticAccepted},
		FailureCount: 2,
	}}}
	service := NewRecertificationService(repo, imageCandidateValidatorFunc(func(context.Context, string) error { return nil }), RecertificationConfig{MaxAttempts: 5})
	service.now = func() time.Time { return now }

	report, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Succeeded != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(repo.validations) != 1 || !repo.validations[0].Success || repo.validations[0].FailureCount != 0 {
		t.Fatalf("validation = %+v, want success with an empty retry budget", repo.validations)
	}
}
