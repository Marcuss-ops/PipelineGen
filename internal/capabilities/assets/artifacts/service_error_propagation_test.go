package assets

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"go.uber.org/zap"
)

type serviceErrorRepo struct {
	createErr       error
	updateErrByStat map[Status]error
	updates         []Status
}

func (r *serviceErrorRepo) Create(_ context.Context, _ *Artifact) error            { return r.createErr }
func (r *serviceErrorRepo) Get(context.Context, string) (*Artifact, error)         { return nil, nil }
func (r *serviceErrorRepo) GetBySHA256(context.Context, string) (*Artifact, error) { return nil, nil }
func (r *serviceErrorRepo) UpdateStatus(_ context.Context, _ string, status Status, _ string, _ int64) error {
	r.updates = append(r.updates, status)
	return r.updateErrByStat[status]
}
func (r *serviceErrorRepo) ListByJob(context.Context, string) ([]Artifact, error) { return nil, nil }
func (r *serviceErrorRepo) CreateSource(context.Context, *ArtifactSource) error   { return nil }
func (r *serviceErrorRepo) UpsertJobArtifact(context.Context, *JobArtifact) error { return nil }
func (r *serviceErrorRepo) ListJobArtifacts(context.Context, string) ([]JobArtifact, error) {
	return nil, nil
}
func (r *serviceErrorRepo) GetJobArtifact(context.Context, string, string) (*JobArtifact, error) {
	return nil, nil
}
func (r *serviceErrorRepo) TouchAccess(context.Context, string) error { return nil }

var _ Repository = (*serviceErrorRepo)(nil)

type serviceStageWriter struct {
	bytes.Buffer
	closeErr error
	writeErr error
}

func (w *serviceStageWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.Buffer.Write(p)
}

func (w *serviceStageWriter) Key() string  { return "stage-1" }
func (w *serviceStageWriter) Close() error { return w.closeErr }

var _ StagingWriter = (*serviceStageWriter)(nil)

type serviceBlobStore struct {
	writer       *serviceStageWriter
	stageErr     error
	promoteErr   error
	promoteCalls int
}

func (b *serviceBlobStore) Stage(context.Context, string) (StagingWriter, error) {
	if b.stageErr != nil {
		return nil, b.stageErr
	}
	return b.writer, nil
}
func (b *serviceBlobStore) VerifyAndPromote(context.Context, string, string) (PromoteResult, error) {
	b.promoteCalls++
	return PromoteResult{StorageKey: "sha256/ab/hash", SHA256: "sha256", SizeBytes: 4}, b.promoteErr
}
func (b *serviceBlobStore) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (b *serviceBlobStore) Delete(context.Context, string) error           { return nil }
func (b *serviceBlobStore) Stat(context.Context, string) (BlobInfo, error) { return BlobInfo{}, nil }

var _ BlobStore = (*serviceBlobStore)(nil)

func newServiceErrorTest(repo Repository, blobs BlobStore) *Service {
	return NewService(blobs, repo, zap.NewNop())
}

func hasStatus(statuses []Status, want Status) bool {
	for _, status := range statuses {
		if status == want {
			return true
		}
	}
	return false
}

func TestCreateAndVerifyFailsClosedWhenStageCannotStart(t *testing.T) {
	stageErr := errors.New("stage unavailable")
	repo := &serviceErrorRepo{updateErrByStat: map[Status]error{}}
	blobs := &serviceBlobStore{stageErr: stageErr}

	_, err := newServiceErrorTest(repo, blobs).CreateAndVerify(context.Background(), CreateInput{
		ID: "artifact-stage", Reader: bytes.NewBufferString("data"),
	})

	if !errors.Is(err, stageErr) {
		t.Fatalf("error = %v, want stage cause", err)
	}
	if !hasStatus(repo.updates, StatusFailed) {
		t.Fatalf("status updates = %v, want FAILED after stage failure", repo.updates)
	}
}

func TestCreateAndVerifyFailsClosedWhenWriterCannotWrite(t *testing.T) {
	writeErr := errors.New("write failed")
	repo := &serviceErrorRepo{updateErrByStat: map[Status]error{}}
	blobs := &serviceBlobStore{writer: &serviceStageWriter{writeErr: writeErr}}

	_, err := newServiceErrorTest(repo, blobs).CreateAndVerify(context.Background(), CreateInput{
		ID: "artifact-write", Reader: bytes.NewBufferString("data"),
	})

	if !errors.Is(err, writeErr) {
		t.Fatalf("error = %v, want writer write cause", err)
	}
	if blobs.promoteCalls != 0 {
		t.Fatalf("VerifyAndPromote calls = %d, want 0 after write failure", blobs.promoteCalls)
	}
	if !hasStatus(repo.updates, StatusFailed) {
		t.Fatalf("status updates = %v, want FAILED after write failure", repo.updates)
	}
}

func TestCreateAndVerifyFailsClosedWhenVerifyingStatusCannotPersist(t *testing.T) {
	statusErr := errors.New("verifying status write failed")
	repo := &serviceErrorRepo{updateErrByStat: map[Status]error{StatusVerifying: statusErr}}
	blobs := &serviceBlobStore{writer: &serviceStageWriter{}}

	_, err := newServiceErrorTest(repo, blobs).CreateAndVerify(context.Background(), CreateInput{
		ID: "artifact-1", Reader: bytes.NewBufferString("data"),
	})

	if !errors.Is(err, statusErr) {
		t.Fatalf("error = %v, want verifying persistence cause", err)
	}
	if blobs.promoteCalls != 0 {
		t.Fatalf("VerifyAndPromote calls = %d, want 0 after failed verifying transition", blobs.promoteCalls)
	}
	if !hasStatus(repo.updates, StatusFailed) {
		t.Fatalf("status updates = %v, want a FAILED fallback transition", repo.updates)
	}
}

func TestCreateAndVerifyPropagatesWriterCloseError(t *testing.T) {
	closeErr := errors.New("close failed")
	repo := &serviceErrorRepo{updateErrByStat: map[Status]error{}}
	blobs := &serviceBlobStore{writer: &serviceStageWriter{closeErr: closeErr}}

	_, err := newServiceErrorTest(repo, blobs).CreateAndVerify(context.Background(), CreateInput{
		ID: "artifact-close", Reader: bytes.NewBufferString("data"),
	})

	if !errors.Is(err, closeErr) {
		t.Fatalf("error = %v, want writer close cause", err)
	}
	if blobs.promoteCalls != 0 {
		t.Fatalf("VerifyAndPromote calls = %d, want 0 after close failure", blobs.promoteCalls)
	}
	if !hasStatus(repo.updates, StatusFailed) {
		t.Fatalf("status updates = %v, want FAILED after close failure", repo.updates)
	}
}

func TestCreateAndVerifyPreservesVerifyAndFailedStatusErrors(t *testing.T) {
	verifyErr := errors.New("verification failed")
	failedStatusErr := errors.New("failed status write failed")
	repo := &serviceErrorRepo{updateErrByStat: map[Status]error{StatusFailed: failedStatusErr}}
	blobs := &serviceBlobStore{writer: &serviceStageWriter{}, promoteErr: verifyErr}

	_, err := newServiceErrorTest(repo, blobs).CreateAndVerify(context.Background(), CreateInput{
		ID: "artifact-2", Reader: bytes.NewBufferString("data"),
	})

	if !errors.Is(err, verifyErr) {
		t.Fatalf("error = %v, want verification cause", err)
	}
	if !errors.Is(err, failedStatusErr) {
		t.Fatalf("error = %v, want failed-status persistence cause", err)
	}
	if !hasStatus(repo.updates, StatusFailed) {
		t.Fatalf("status updates = %v, want FAILED attempt", repo.updates)
	}
}
