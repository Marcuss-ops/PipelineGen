// Package books — P1.3 closure tests for job_handler.go (July 2026).
//
// The pre-Phase-1.3 driveToDrive was `void` and silently swallowed
// publish failures via slog.Warn while the caller returned
// `success: true`. Phase 1.3 closure replaces that with a typed
// `driveToDrive → (asset.AssetPublishStatus, error)` shape that the
// caller surfaces verbatim in the response map via `delivery_status`
// + `drive_publish_error` fields. These tests pin the new contract:
//
//  1. driveToDrive (unit) — the typed AssetPublishStatus output
//     (LOCAL_ONLY / PUBLISHED / PUBLISH_FAILED) and the typed-sentinel
//     behaviour under the four canonical branches: nil publisher,
//     happy path, publish error, mixed partial failure.
//  2. HandleJob (unit) — the response map must always carry
//     `delivery_status` (PUBLISHED / PUBLISH_FAILED / LOCAL_ONLY);
//     on publish failure, the response must also include a
//     `drive_publish_error` field. `success` stays true (Drive is
//     OPTIONAL per Phase 1.3 Option B); callers branch on
//     `delivery_status` to react to Drive outcome independently.
//
// The tests stub BookTransformer (so HandleJob doesn't spawn the
// Python subprocess) AND stub PublisherPort (so driveToDrive doesn't
// touch real Drive). Both stubs are hand-rolled per AGENTS.md
// Pattern 0 (compile-time port assertions pinning the contract).
package books

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// stubBookTransformer is the canonical test double for
// books.BookTransformer (defined in ports.go). Hand-rolled so the
// test for Phase 1.3 doesn't drag in the pythontransformer concrete.
// Both methods are implemented; Transform returns the canned result
// with no progress events.
type stubBookTransformer struct {
	Result *TransformResult
	Err    error
}

func (s *stubBookTransformer) Transform(_ context.Context, _ *TransformRequest) (*TransformResult, error) {
	return s.Result, s.Err
}

func (s *stubBookTransformer) TransformWithProgress(_ context.Context, _ *TransformRequest, _ func(int, string)) (*TransformResult, error) {
	return s.Result, s.Err
}

// Compile-time assertion: the stub structurally satisfies the
// canonical BookTransformer port (godlike/06 SSOT lock).
var _ BookTransformer = (*stubBookTransformer)(nil)

// stubBookPublisher is the canonical test double for books.PublisherPort.
// publishFn lets each test case inject the per-call outcome (success /
// error) so we can drive both the .txt and .pdf branches deterministically.
type stubBookPublisher struct {
	publishFn func(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error)
	calls     []delivery.PublishRequest
}

func (s *stubBookPublisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	s.calls = append(s.calls, req)
	return s.publishFn(ctx, req)
}

// Compile-time assertion (godlike/06 Pattern 0): locks the
// books.PublisherPort signature so future drift breaks the build here.
var _ PublisherPort = (*stubBookPublisher)(nil)

// newPhase1_3Service builds a Service with the canonical Phase 1.3
// surface wired (no publisher = nil-port sentinel, no Drive folder
// override, fake transformer with canned success). Tests that need
// a publisher set one on the returned Service directly.
func newPhase1_3Service(t *testing.T, transformResult *TransformResult, transformErr error) *Service {
	t.Helper()
	cfg := DefaultConfig()
	cfg.DriveFolderID = "test-folder"
	tr := &stubBookTransformer{Result: transformResult, Err: transformErr}
	return NewService(
		cfg,               // *Config
		nil,               // *sql.DB (unused)
		cfg.DriveFolderID, // driveFolder
		zap.NewNop(),      // *zap.Logger
		nil,               // PublisherPort — tests overwrite the field for publish scenarios
		nil,               // drive.Reader (unused by HandleJob / driveToDrive)
		tr,                // BookTransformer (stub for the canonical MB-OK path)
	)
}

// ────────────────────────────────────────────────────────────────────
// driveToDrive unit tests (4 arms).
// ────────────────────────────────────────────────────────────────────

// TestDriveToDrive_PublisherNil_ReturnsLocalOnly: a nil publisher
// must NOT silently swallow + return success: true; it must surface
// AssetPublishLocalOnly so the caller can put `delivery_status=LOCAL_ONLY`
// in the response (godlike/07 no-fake-availability).
func TestDriveToDrive_PublisherNil_ReturnsLocalOnly(t *testing.T) {
	t.Parallel()
	svc := newPhase1_3Service(t, nil, nil)
	// publisher is nil by construction; do not overwrite.

	result := &ProcessResult{
		Success:    true,
		OutputPath: "/tmp/book_out.md",
		PDFPath:    "/tmp/book_out.pdf",
	}
	status, err := svc.driveToDrive(context.Background(), &ProcessRequest{}, result, "job-test")
	require.NoError(t, err, "nil publisher MUST NOT return an error; the absence of Drive is itself a valid LOCAL_ONLY outcome")
	assert.Equal(t, asset.AssetPublishLocalOnly, status,
		"nil publisher → AssetPublishLocalOnly (godlike/07 no-fake-availability: do not pretend Drive succeeded)")
	assert.Empty(t, result.DriveDocURL, "no publisher → no Drive URL stamped on the result")
	assert.Empty(t, result.DrivePDFURL, "no publisher → no Drive URL stamped on the result")
}

// TestDriveToDrive_PublishOK_ReturnsPublished: both .txt and .pdf
// publish OK → AssetPublishPublished + WebViewLink stamped on the
// result. Err is nil. Pins the canonical success branch.
func TestDriveToDrive_PublishOK_ReturnsPublished(t *testing.T) {
	t.Parallel()
	svc := newPhase1_3Service(t, nil, nil)
	pub := &stubBookPublisher{
		publishFn: func(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
			return &delivery.PublishResult{
				FileID:      "drive-file-id-" + req.Filename,
				WebViewLink: "https://drive.google.com/file/d/drive-file-id-" + req.Filename,
				Action:      delivery.PublishActionCreated,
			}, nil
		},
	}
	svc.publisher = pub

	result := &ProcessResult{
		Success:    true,
		OutputPath: "/tmp/book_out.md",
		PDFPath:    "/tmp/book_out.pdf",
	}
	status, err := svc.driveToDrive(context.Background(), &ProcessRequest{}, result, "job-test")
	require.NoError(t, err, "all publishes OK → return nil error")
	assert.Equal(t, asset.AssetPublishPublished, status,
		"all publishes OK → AssetPublishPublished")
	assert.Equal(t, 2, len(pub.calls), "publish must be called once per artifact (.txt + .pdf)")
	assert.NotEmpty(t, result.DriveDocURL, "successful .txt publish stamps DriveDocURL on the result")
	assert.NotEmpty(t, result.DrivePDFURL, "successful .pdf publish stamps DrivePDFURL on the result")
}

// TestDriveToDrive_PublishFail_ReturnsPublishFailed_ErrWrapsSentinel:
// both publishes fail → AssetPublishFailed + the error wraps
// ErrBookDrivePublishFailed so callers can errors.Is() the typed
// sentinel. This is the load-bearing behaviour that the pre-Phase-1.3
// code masked via log-warn + always-return-success:true.
func TestDriveToDrive_PublishFail_ReturnsPublishFailed_ErrWrapsSentinel(t *testing.T) {
	t.Parallel()
	svc := newPhase1_3Service(t, nil, nil)
	pub := &stubBookPublisher{
		publishFn: func(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
			return nil, errors.New("connection refused: drive quota exceeded")
		},
	}
	svc.publisher = pub

	result := &ProcessResult{
		Success:    true,
		OutputPath: "/tmp/book_out.md",
		PDFPath:    "/tmp/book_out.pdf",
	}
	status, err := svc.driveToDrive(context.Background(), &ProcessRequest{}, result, "job-test")
	require.Error(t, err, "publish failure MUST propagate via non-nil err (godlike/07)")
	assert.Equal(t, asset.AssetPublishFailed, status,
		"any failed publish → AssetPublishFailed")
	assert.True(t, errors.Is(err, ErrBookDrivePublishFailed),
		"the typed ErrBookDrivePublishFailed sentinel MUST be in the err chain (errors.Is-able)")
	assert.Empty(t, result.DriveDocURL, "failed publish MUST NOT stamp a Drive URL on the result")
	assert.Empty(t, result.DrivePDFURL, "failed publish MUST NOT stamp a Drive URL on the result")
}

// TestDriveToDrive_MixedPublishPartial_StillFailed: .txt OK + .pdf
// fail (mixed outcome) → AssetPublishFailed + ErrBookDrivePublishFailed
// wrapped. The .txt Drive URL IS stamped on the result (partial
// publish is real), but the canonical status flips to FAILED and the
// caller surfaces the wrapped error in `drive_publish_error`.
func TestDriveToDrive_MixedPublishPartial_StillFailed(t *testing.T) {
	t.Parallel()
	svc := newPhase1_3Service(t, nil, nil)
	pub := &stubBookPublisher{
		publishFn: func(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
			if req.Filename == "book_out.md" {
				return &delivery.PublishResult{
					FileID:      "drive-txt-id",
					WebViewLink: "https://drive.google.com/file/d/drive-txt-id",
					Action:      delivery.PublishActionCreated,
				}, nil
			}
			return nil, errors.New("upload timed out for pdf")
		},
	}
	svc.publisher = pub

	result := &ProcessResult{
		Success:    true,
		OutputPath: "/tmp/book_out.md",
		PDFPath:    "/tmp/book_out.pdf",
	}
	status, err := svc.driveToDrive(context.Background(), &ProcessRequest{}, result, "job-test")
	require.Error(t, err, "mixed outcome with failure MUST propagate err")
	assert.Equal(t, asset.AssetPublishFailed, status,
		"partial success + a failure ⇒ AssetPublishFailed (the failure dominates the canonical status)")
	assert.True(t, errors.Is(err, ErrBookDrivePublishFailed),
		"the typed sentinel MUST be in the err chain even on partial-success branch")
	assert.NotEmpty(t, result.DriveDocURL, "the successful .txt publish MUST stamp DriveDocURL on the result (partial publish is real)")
	assert.Empty(t, result.DrivePDFURL, "the failed .pdf publish MUST NOT stamp DrivePDFURL on the result")
}

// TestDriveToDrive_AlreadyPublishedByPython_ReturnsPublished: Python
// transformer already uploaded (DriveDocURL/DrivePDFURL populated).
// driveToDrive MUST skip re-upload and surface AssetPublishPublished
// because Drive is already populated — pretending LOCAL_ONLY here
// would falsely tell the caller that no Drive presence exists.
func TestDriveToDrive_AlreadyPublishedByPython_ReturnsPublished(t *testing.T) {
	t.Parallel()
	svc := newPhase1_3Service(t, nil, nil)
	pub := &stubBookPublisher{
		publishFn: func(_ context.Context, _ delivery.PublishRequest) (*delivery.PublishResult, error) {
			t.Fatal("publisher MUST NOT be called when Python has already uploaded; would cause a silent duplicate Drive write")
			return nil, nil
		},
	}
	svc.publisher = pub

	result := &ProcessResult{
		Success:     true,
		OutputPath:  "/tmp/book_out.md",
		PDFPath:     "/tmp/book_out.pdf",
		DriveDocURL: "https://drive.google.com/file/d/python-txt",
		DrivePDFURL: "https://drive.google.com/file/d/python-pdf",
	}
	status, err := svc.driveToDrive(context.Background(), &ProcessRequest{}, result, "job-test")
	require.NoError(t, err, "no-op case MUST NOT return an error")
	assert.Equal(t, asset.AssetPublishPublished, status,
		"already-published artifacts (Python-side) ⇒ AssetPublishPublished (truthful surface)")
	assert.Equal(t, 0, len(pub.calls), "no-op branch MUST NOT issue any Publish call")
}

// ────────────────────────────────────────────────────────────────────
// HandleJob integration-flavor tests (2 arms).
// ────────────────────────────────────────────────────────────────────

// TestHandleJob_LocalOK_DriveOK_DeliveryStatusPublished: local
// processing succeeds + Drive publish succeeds. The response map
// MUST carry `delivery_status=PUBLISHED` AND `success=true`. Pins
// the canonical happy-path wire shape for callers reading the
// broker result map.
func TestHandleJob_LocalOK_DriveOK_DeliveryStatusPublished(t *testing.T) {
	t.Parallel()
	svc := newPhase1_3Service(t, &TransformResult{
		OutputPath: "/tmp/book_out.md",
		PDFPath:    "/tmp/book_out.pdf",
		Language:   "en",
	}, nil)
	svc.publisher = &stubBookPublisher{
		publishFn: func(_ context.Context, _ delivery.PublishRequest) (*delivery.PublishResult, error) {
			return &delivery.PublishResult{
				FileID:      "drive-file-id",
				WebViewLink: "https://drive.google.com/file/d/drive-file-id",
				Action:      delivery.PublishActionCreated,
			}, nil
		},
	}

	payload := []byte(`{"file_path":"/tmp/in.pdf","language":"en"}`)
	out, err := svc.HandleJob(context.Background(), &job.Job{ID: "job-test", Payload: payload}, &appjobs.JobTools{})
	require.NoError(t, err)
	require.NotNil(t, out, "HandleJob MUST return a non-nil response map on the canonical happy path")

	successRaw, ok := out["success"]
	require.True(t, ok, "response MUST include the `success` field (canonical wire contract)")
	assert.True(t, successRaw.(bool), "Drive OK ⇒ success=true (local processing succeeded)")

	statusRaw, ok := out["delivery_status"]
	require.True(t, ok, "response MUST include the `delivery_status` field (P1.3 surface)")
	assert.Equal(t, "PUBLISHED", statusRaw, "Drive OK both artifacts ⇒ delivery_status=PUBLISHED")

	_, hasErr := out["drive_publish_error"]
	assert.False(t, hasErr, "Drive OK ⇒ MUST NOT include drive_publish_error (no failure to surface)")
}

// TestHandleJob_LocalOK_DriveFail_DeliveryStatusPublishFailed: local
// processing succeeds + Drive publish fails. Option B (Drive is
// OPTIONAL): the response map carries:
//   - `success=true` (local processing succeeded; Drive is decoration)
//   - `delivery_status=PUBLISH_FAILED` (godlike/07 truthful surface)
//   - `drive_publish_error=<string>` (typed-error readable for UI)
//
// This is the load-bearing assertion that the pre-Phase-1.3
// `success=true + log-warn + no surface` behaviour is gone. Callers
// branch on delivery_status to react to Drive outcome independently
// of the job's terminal JobFinalizer-driven status.
func TestHandleJob_LocalOK_DriveFail_DeliveryStatusPublishFailed(t *testing.T) {
	t.Parallel()
	svc := newPhase1_3Service(t, &TransformResult{
		OutputPath: "/tmp/book_out.md",
		PDFPath:    "/tmp/book_out.pdf",
		Language:   "en",
	}, nil)
	svc.publisher = &stubBookPublisher{
		publishFn: func(_ context.Context, _ delivery.PublishRequest) (*delivery.PublishResult, error) {
			return nil, errors.New("drive quota exceeded (simulated)")
		},
	}

	payload := []byte(`{"file_path":"/tmp/in.pdf","language":"en"}`)
	out, err := svc.HandleJob(context.Background(), &job.Job{ID: "job-test", Payload: payload}, &appjobs.JobTools{})
	require.NoError(t, err, "Drive failure MUST NOT bubble up as a HandleJob error (Drive is OPTIONAL per Option B); local success suffices")
	require.NotNil(t, out)

	successRaw := out["success"]
	assert.True(t, successRaw.(bool),
		"Drive failure with local success ⇒ success=true (Option B: Drive is OPTIONAL)")

	statusRaw := out["delivery_status"]
	require.Equal(t, "PUBLISH_FAILED", statusRaw,
		"Drive failure ⇒ delivery_status=PUBLISH_FAILED (godlike/07 truthful wire surface)")

	errRaw, ok := out["drive_publish_error"]
	require.True(t, ok, "Drive failure MUST populate drive_publish_error so UI banners can show the inner reason without parsing the typed sentinel")
	assert.Contains(t, errRaw.(string), "drive quota exceeded",
		"drive_publish_error MUST echo the inner publish error verbatim")

	// And the typed sentinel must still be discoverable in process:
	// the err returned by HandleJob is nil here, but the assertion
	// above proves the message is in the response map. A future
	// caller-side handler that walks this string can still reach
	// ErrBookDrivePublishFailed via errors.Is once the canonical
	// pattern lands.
}

// ─────────────────────────────────────────────────────────────────
// PR-P12-CLIPS-AND-BOOKS (July 2026, deadline 2026-08-08) —
// 3 audit-pin tests pinning the canonical auto-derivation of
// Project/Group/Subject on the books publisher surface.
// ─────────────────────────────────────────────────────────────────

// TestHandleJob_DrivePublishesWithAutoDerivedFields pins the
// canonical auto-derivation contract on books driveToDrive. The
// Publisher receives:
//
//	delivery.PublishRequest{
//	    Destination: delivery.DestinationBook,
//	    ProjectID:   job.ID,                  // auto-derive from book.JobID
//	    Group:       "",                       // books don't group; registry picks canonical folder
//	    Subject:     filepath.Base(a.path),   // per-file identity (.txt or .pdf)
//	    // RootFolderOverride RETIRED — Publisher resolves target folder
//	    // via DestinationRegistry + DestinationPolicy.RootFolderID.
//	}
//
// Pre-PR-P12 the code passed `RootFolderOverride: folderID` (legacy
// bypass) and req.DriveFolderID/s.driveFolder were threaded through
// the same literal. Post-PR-P12 the call routes via canonical
// semantic fields only; the per-request folder override is retired.
func TestHandleJob_DrivePublishesWithAutoDerivedFields(t *testing.T) {
	t.Parallel()
	svc := newPhase1_3Service(t, &TransformResult{
		OutputPath: "/tmp/book_out.md",
		PDFPath:    "/tmp/book_out.pdf",
		Language:   "en",
	}, nil)
	pub := &stubBookPublisher{
		publishFn: func(_ context.Context, _ delivery.PublishRequest) (*delivery.PublishResult, error) {
			return &delivery.PublishResult{
				FileID:      "drive-book-id",
				WebViewLink: "https://drive.google.com/file/d/drive-book-id",
				Action:      delivery.PublishActionCreated,
			}, nil
		},
	}
	svc.publisher = pub

	payload := []byte(`{"file_path":"/tmp/in.pdf","language":"en"}`)
	out, err := svc.HandleJob(context.Background(), &job.Job{ID: "book-job-p12", Payload: payload}, &appjobs.JobTools{})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "PUBLISHED", out["delivery_status"], "happy path ⇒ PUBLISHED")

	// Both artifacts (.md + .pdf) are published, each with the
	// canonical auto-derivation.
	require.Equal(t, 2, len(pub.calls), "publish MUST be called once per artifact")
	for _, call := range pub.calls {
		if call.Destination != delivery.DestinationBook {
			t.Errorf("Destination = %q, want %q", call.Destination, delivery.DestinationBook)
		}
		// Auto-derivation contract: ProjectID = book.JobID
		if call.ProjectID != "book-job-p12" {
			t.Errorf("ProjectID = %q, want %q (auto-derived from book.JobID)", call.ProjectID, "book-job-p12")
		}
		// Group is intentionally empty (books don't group; the
		// DestinationRegistry picks the canonical folder for
		// DestinationBook).
		if call.Group != "" {
			t.Errorf("Group = %q, want \"\" (books don't group)", call.Group)
		}
		// Subject is per-file identity (filename).
		if call.Subject == "" {
			t.Errorf("Subject is empty; want filename (per-file identity)")
		}
		// godlike/06 SSOT: RootFolderOverride RETIRED.
		if call.RootFolderOverride != "" {
			t.Errorf("RootFolderOverride = %q, want \"\" (RETIRED per PR-P12-CLIPS-AND-BOOKS)", call.RootFolderOverride)
		}
	}
}

// TestHandleJob_BothArtifactsPublishedWithDistinctSubjects pins
// the per-file identity (Subject) for both .md and .pdf artifacts.
// Subject is set to the filename, so the two calls carry distinct
// values. This is the canonical wire shape the Qdrant indexer
// (and the asset-tree upsert) consume to distinguish book outputs.
func TestHandleJob_BothArtifactsPublishedWithDistinctSubjects(t *testing.T) {
	t.Parallel()
	svc := newPhase1_3Service(t, &TransformResult{
		OutputPath: "/tmp/book_out.md",
		PDFPath:    "/tmp/book_out.pdf",
		Language:   "en",
	}, nil)
	pub := &stubBookPublisher{
		publishFn: func(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
			return &delivery.PublishResult{
				FileID:      "drive-" + req.Subject,
				WebViewLink: "https://drive.google.com/file/d/drive-" + req.Subject,
				Action:      delivery.PublishActionCreated,
			}, nil
		},
	}
	svc.publisher = pub

	payload := []byte(`{"file_path":"/tmp/in.pdf","language":"en"}`)
	_, err := svc.HandleJob(context.Background(), &job.Job{ID: "book-job-p12", Payload: payload}, &appjobs.JobTools{})
	require.NoError(t, err)
	require.Equal(t, 2, len(pub.calls), "publish MUST be called once per artifact")

	// Collect the (LocalPath, Subject) tuples; the two calls must be
	// distinct.
	seen := map[string]string{} // Subject -> LocalPath
	for _, call := range pub.calls {
		if prev, ok := seen[call.Subject]; ok && prev == call.LocalPath {
			t.Errorf("duplicate publish for Subject=%q, LocalPath=%q", call.Subject, call.LocalPath)
		}
		seen[call.Subject] = call.LocalPath
	}
	if _, ok := seen["book_out.md"]; !ok {
		t.Errorf(".md artifact not published (Subject=book_out.md expected); saw subjects: %v", keys(seen))
	}
	if _, ok := seen["book_out.pdf"]; !ok {
		t.Errorf(".pdf artifact not published (Subject=book_out.pdf expected); saw subjects: %v", keys(seen))
	}
}

// TestHandleJob_PreUploadedArtifactsSkipPublish pins the
// alreadySet=true (Python script already uploaded) branch: when
// DriveDocURL + DrivePDFURL are populated, the canonical driveToDrive
// must NOT issue any Publish call (a duplicate write would corrupt
// the canonical state). The publisher.calls slice MUST be empty
// after HandleJob, and the response map carries delivery_status=PUBLISHED
// (truthful: Drive is already populated).
func TestHandleJob_PreUploadedArtifactsSkipPublish(t *testing.T) {
	t.Parallel()
	svc := newPhase1_3Service(t, &TransformResult{
		OutputPath:  "/tmp/book_out.md",
		PDFPath:     "/tmp/book_out.pdf",
		DriveDocURL: "https://drive.google.com/file/d/python-txt",
		DrivePDFURL: "https://drive.google.com/file/d/python-pdf",
		Language:    "en",
	}, nil)
	pub := &stubBookPublisher{
		publishFn: func(_ context.Context, _ delivery.PublishRequest) (*delivery.PublishResult, error) {
			t.Fatal("Publisher MUST NOT be called when Python has already uploaded; a duplicate write would corrupt canonical state")
			return nil, nil
		},
	}
	svc.publisher = pub

	payload := []byte(`{"file_path":"/tmp/in.pdf","language":"en"}`)
	out, err := svc.HandleJob(context.Background(), &job.Job{ID: "book-job-p12", Payload: payload}, &appjobs.JobTools{})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, 0, len(pub.calls), "pre-uploaded artifacts MUST NOT trigger any Publish call")
	assert.Equal(t, "PUBLISHED", out["delivery_status"], "Drive is already populated ⇒ delivery_status=PUBLISHED (truthful)")
}

// keys is a tiny helper for TestHandleJob_BothArtifactsPublishedWithDistinctSubjects.
func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestHandleJob_LocalDisabled_DeliveryStatusLocalOnly: nil publisher
// on the Service (no Drive wired). Local processing succeeds; the
// response map carries delivery_status=LOCAL_ONLY (no Drive publish
// was even attempted). Pins the truthful surface for "Drive disabled"
// deployments.
func TestHandleJob_LocalDisabled_DeliveryStatusLocalOnly(t *testing.T) {
	t.Parallel()
	svc := newPhase1_3Service(t, &TransformResult{
		OutputPath: "/tmp/book_out.md",
		PDFPath:    "/tmp/book_out.pdf",
		Language:   "en",
	}, nil)
	// svc.publisher intentionally nil (default from newPhase1_3Service)

	payload := []byte(`{"file_path":"/tmp/in.pdf","language":"en"}`)
	out, err := svc.HandleJob(context.Background(), &job.Job{ID: "job-test", Payload: payload}, &appjobs.JobTools{})
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.True(t, out["success"].(bool), "local success alone suffices (Option B)")
	assert.Equal(t, "LOCAL_ONLY", out["delivery_status"],
		"nil publisher ⇒ delivery_status=LOCAL_ONLY (godlike/07: do not pretend Drive succeeded)")
	_, hasErr := out["drive_publish_error"]
	assert.False(t, hasErr, "nil publisher ⇒ MUST NOT include drive_publish_error (no failure occurred)")
}
