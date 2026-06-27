package generation

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// stubJobs is a minimal JobService used by the module_test path.
// Real jobs.Service is exercised end-to-end via the composition
// root's integration tests.
type stubJobs struct{}

func (stubJobs) Enqueue(context.Context, *job.EnqueueRequest) (*job.Job, error) {
	return nil, nil
}
func (stubJobs) Get(context.Context, string) (*job.Job, error) { return nil, nil }
func (stubJobs) Cancel(context.Context, string) error          { return nil }
func (stubJobs) ListEvents(context.Context, string) ([]job.Event, error) {
	return nil, nil
}

// stubAssets is the minimal AssetStore used by the module_test path.
type stubAssets struct{}

func (stubAssets) Get(context.Context, string) (*asset.Details, error) {
	return nil, nil
}

func TestBuild_RequiresJobs(t *testing.T) {
	_, err := Build(Dependencies{
		BooksEnabled:   true,
		LessonsEnabled: true,
		ScriptEnabled:  true,
		Logger:         zap.NewNop(),
	})
	if err == nil {
		t.Fatal("expected error when Jobs is nil, got nil")
	}
}

func TestBuild_ReturnsDescriptorWithJobHandlerSlot(t *testing.T) {
	d, err := Build(Dependencies{
		Jobs:           stubJobs{},
		Assets:         stubAssets{},
		BooksEnabled:   true,
		LessonsEnabled: true,
		ScriptEnabled:  true,
		Logger:         zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if d == nil {
		t.Fatal("Build returned nil descriptor")
	}
	if d.Name() != "generation" {
		t.Fatalf("Name = %q, want generation", d.Name())
	}
	if !d.Enabled() {
		t.Fatalf("Enabled = false, want true")
	}

	// DescriptorJobs slot MUST be present — that's the whole point
	// of generation's participation in the capability standard.
	if _, ok := d.(api.DescriptorJobs); !ok {
		t.Fatalf("descriptor type %T does not implement api.DescriptorJobs", d)
	}

	// Concrete type MUST carry Service + JobHandlers for non-HTTP
	// callers (admin tooling, future internal callers).
	gd, ok := d.(*GenerationDescriptor)
	if !ok {
		t.Fatalf("Descriptor type = %T, want *GenerationDescriptor", d)
	}
	if gd.Service == nil {
		t.Fatal("GenerationDescriptor.Service is nil, want non-nil")
	}
	if gd.JobHandlers.Books != nil {
		t.Fatalf("JobHandlers.Books = %v, want nil (no Books deps supplied)", gd.JobHandlers.Books)
	}
	if gd.JobHandlers.Lessons != nil {
		t.Fatalf("JobHandlers.Lessons = %v, want nil (no Lessons deps supplied)", gd.JobHandlers.Lessons)
	}
}

func TestBuild_SubstitutesNopLoggerWhenNil(t *testing.T) {
	d, err := Build(Dependencies{Jobs: stubJobs{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if d.Name() != "generation" {
		t.Fatalf("Name = %q, want generation", d.Name())
	}
}

func TestBuild_HandlerRoutesAreRegistered(t *testing.T) {
	d, err := Build(Dependencies{
		Jobs:          stubJobs{},
		ScriptEnabled: true,
		Logger:        zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	rg := engine.Group("/api")
	d.RegisterRoutes(rg)

	wantRoutes := map[string]bool{
		"POST /api/generations":            false,
		"GET /api/generations/:id":         false,
		"POST /api/generations/:id/cancel": false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := wantRoutes[key]; ok {
			wantRoutes[key] = true
		}
	}
	for path, found := range wantRoutes {
		if !found {
			t.Errorf("missing route: %s", path)
		}
	}
}

// TestBuild_BooksAndLessonsHandlersFlowThrough confirms HandlerFunc
// values supplied in Dependencies are carried into the descriptor's
// JobHandlers slot — the import-cycle fix depends on this.
func TestBuild_BooksAndLessonsHandlersFlowThrough(t *testing.T) {
	noopBooks := HandlerFunc(func(_ context.Context, _ *job.Job, _ *appjobs.JobTools) (map[string]any, error) {
		return map[string]any{"via": "books"}, nil
	})
	noopLessons := HandlerFunc(func(_ context.Context, _ *job.Job, _ *appjobs.JobTools) (map[string]any, error) {
		return map[string]any{"via": "lessons"}, nil
	})
	d, err := Build(Dependencies{
		Jobs:           stubJobs{},
		Books:          noopBooks,
		Lessons:        noopLessons,
		BooksEnabled:   true,
		LessonsEnabled: true,
		ScriptEnabled:  true,
		Logger:         zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	gd := d.(*GenerationDescriptor)
	if gd.JobHandlers.Books == nil {
		t.Fatal("JobHandlers.Books is nil after Build; want the supplied function value")
	}
	if gd.JobHandlers.Lessons == nil {
		t.Fatal("JobHandlers.Lessons is nil after Build; want the supplied function value")
	}
}

func TestJobHandlers_RegisterJobHandlersRequiresJobsService(t *testing.T) {
	jh := JobHandlers{Log: zap.NewNop()}
	if err := jh.RegisterJobHandlers(nil); err == nil {
		t.Fatal("expected error when jobs registrar is nil")
	}
}

// TestJobHandlers_RegisterJobHandlersSkipsNilHandlers confirms the
// nil-handler tolerance: nil Books + nil Lessons + non-nil registrar
// succeeds (registration is silently skipped — no RegisterHandler
// fires).
func TestJobHandlers_RegisterJobHandlersSkipsNilHandlers(t *testing.T) {
	jh := JobHandlers{Log: zap.NewNop()}
	rec := &recordingRegistrar{}
	if err := jh.RegisterJobHandlers(rec); err != nil {
		t.Fatalf("RegisterJobHandlers without Books/Lessons: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("expected zero RegisterHandler calls when no handlers, got %d (%v)",
			len(rec.calls), rec.calls)
	}
}

// TestJobHandlers_RegisterJobHandlersFiresForNonNilHandlers confirms
// both worker types register when their handlers are non-nil.
func TestJobHandlers_RegisterJobHandlersFiresForNonNilHandlers(t *testing.T) {
	noopBooks := HandlerFunc(func(_ context.Context, _ *job.Job, _ *appjobs.JobTools) (map[string]any, error) {
		return nil, nil
	})
	noopLessons := HandlerFunc(func(_ context.Context, _ *job.Job, _ *appjobs.JobTools) (map[string]any, error) {
		return nil, nil
	})
	jh := JobHandlers{Books: noopBooks, Lessons: noopLessons, Log: zap.NewNop()}
	rec := &recordingRegistrar{}
	if err := jh.RegisterJobHandlers(rec); err != nil {
		t.Fatalf("RegisterJobHandlers: %v", err)
	}
	got := map[string]bool{}
	for _, c := range rec.calls {
		got[c] = true
	}
	if !got[JobTypeBooksProcess] {
		t.Errorf("expected RegisterHandler(%s, ...) to fire; got calls=%v", JobTypeBooksProcess, rec.calls)
	}
	if !got[JobTypeLessonsProcess] {
		t.Errorf("expected RegisterHandler(%s, ...) to fire; got calls=%v", JobTypeLessonsProcess, rec.calls)
	}
}

// recordingRegistrar is the JobRegistrar test stub. Satisfies
// api.JobRegistrar (compile-time via duck typing on the single
// RegisterHandler method). Confirmed: api.JobRegistrar is
// `interface { RegisterHandler(string, any) error }` so the
// matching method on this struct satisfies the port.
type recordingRegistrar struct {
	calls []string
}

func (r *recordingRegistrar) RegisterHandler(jobType string, _ any) error {
	r.calls = append(r.calls, jobType)
	return nil
}

// Compile-time assertion that recordingRegistrar satisfies the port.
var _ api.JobRegistrar = (*recordingRegistrar)(nil)
