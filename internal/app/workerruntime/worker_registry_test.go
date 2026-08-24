package workerruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/worker"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// noopHandler is a shared stub used by registry tests.
func noopHandler(_ context.Context, _ *job.Job, _ *appjobs.JobTools) (map[string]any, error) {
	return nil, nil
}

// errContains reports whether err.Error() contains the given substring.
func errContains(err error, sub string) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), sub)
}

func TestBuildWorkerRegistry_EmptyDispatcher(t *testing.T) {
	root := &wiring.ComposeRoot{
		Jobs: &wiring.JobsBundle{
			Dispatcher: appjobs.NewDispatcher(),
		},
	}
	_, _, err := BuildWorkerRegistry(root)
	if !errors.Is(err, worker.ErrNoHandlers) {
		t.Fatalf("expected ErrNoHandlers, got %v", err)
	}
}

func TestBuildWorkerRegistry_WithHandlers(t *testing.T) {
	dispatcher := appjobs.NewDispatcher()
	_ = dispatcher.Register("test.job", func(_ context.Context, _ *job.Job, _ *appjobs.JobTools) (map[string]any, error) {
		return nil, nil
	})

	root := &wiring.ComposeRoot{
		Jobs: &wiring.JobsBundle{
			Dispatcher: dispatcher,
		},
	}
	reg, caps, err := BuildWorkerRegistry(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.Len() != 1 {
		t.Fatalf("expected 1 handler, got %d", reg.Len())
	}
	if len(caps) != 1 || caps[0] != "test.job" {
		t.Fatalf("expected caps [test.job], got %v", caps)
	}
}

func TestBuildWorkerRegistry_NilRoot(t *testing.T) {
	_, _, err := BuildWorkerRegistry(nil)
	if err == nil {
		t.Fatal("expected error for nil root")
	}
}

func TestBuildWorkerRegistry_NilJobs(t *testing.T) {
	_, _, err := BuildWorkerRegistry(&wiring.ComposeRoot{})
	if err == nil {
		t.Fatal("expected error for nil jobs")
	}
}

// ── BuildProfileWorkerRegistry (Creator Blocco 1.3) ──────────────────

func TestBuildProfileWorkerRegistry_FiltersByAllowedTypes(t *testing.T) {
	dispatcher := appjobs.NewDispatcher()
	_ = dispatcher.Register("script.generate", noopHandler)
	_ = dispatcher.Register("voiceover.generate_item", noopHandler)
	_ = dispatcher.Register("youtube.upload", noopHandler)
	_ = dispatcher.Register("render.video", noopHandler)

	root := &wiring.ComposeRoot{Jobs: &wiring.JobsBundle{Dispatcher: dispatcher}}
	allowed := []string{"script.generate", "voiceover.generate_item"}
	reg, caps, err := BuildProfileWorkerRegistry(root, allowed)
	if err != nil {
		t.Fatalf("BuildProfileWorkerRegistry: %v", err)
	}
	if reg.Len() != 2 {
		t.Fatalf("expected 2 handlers, got %d", reg.Len())
	}
	if !reg.Has("script.generate") {
		t.Error("registry missing script.generate")
	}
	if !reg.Has("voiceover.generate_item") {
		t.Error("registry missing voiceover.generate_item")
	}
	if reg.Has("youtube.upload") {
		t.Error("registry should NOT have youtube.upload")
	}
	if reg.Has("render.video") {
		t.Error("registry should NOT have render.video")
	}
	// Caps must be derived from the registered handlers only.
	if len(caps) != 2 {
		t.Fatalf("expected 2 caps, got %d: %v", len(caps), caps)
	}
}

func TestBuildProfileWorkerRegistry_MissingScriptGenerate_Error(t *testing.T) {
	dispatcher := appjobs.NewDispatcher()
	_ = dispatcher.Register("voiceover.generate_item", noopHandler)

	root := &wiring.ComposeRoot{Jobs: &wiring.JobsBundle{Dispatcher: dispatcher}}
	allowed := []string{"script.generate", "voiceover.generate_item"}
	_, _, err := BuildProfileWorkerRegistry(root, allowed)
	if err == nil {
		t.Fatal("expected error when profile requires script.generate but no handler registered")
	}
	// The error comes from the pre-registration gate (missing handler),
	// not from the post-registration script.generate check.
	if !errContains(err, "no registered handler") {
		t.Errorf("error should mention missing handler, got: %v", err)
	}
}

func TestBuildProfileWorkerRegistry_AllowedTypeHasNoDispatcherHandler_Error(t *testing.T) {
	dispatcher := appjobs.NewDispatcher()
	_ = dispatcher.Register("script.generate", noopHandler)

	root := &wiring.ComposeRoot{Jobs: &wiring.JobsBundle{Dispatcher: dispatcher}}
	// "image.generate.google" is in allowedTypes but NOT in dispatcher.
	allowed := []string{"script.generate", "image.generate.google"}
	_, _, err := BuildProfileWorkerRegistry(root, allowed)
	if err == nil {
		t.Fatal("expected error when allowed type has no dispatcher handler")
	}
	if !errContains(err, "no registered handler") {
		t.Errorf("error should mention no registered handler, got: %v", err)
	}
	if !errContains(err, "image.generate.google") {
		t.Errorf("error should name the missing type, got: %v", err)
	}
}

func TestBuildProfileWorkerRegistry_EmptyAllowedTypes_Error(t *testing.T) {
	dispatcher := appjobs.NewDispatcher()
	_ = dispatcher.Register("script.generate", noopHandler)

	root := &wiring.ComposeRoot{Jobs: &wiring.JobsBundle{Dispatcher: dispatcher}}
	_, _, err := BuildProfileWorkerRegistry(root, []string{})
	if err == nil {
		t.Fatal("expected error for empty allowedTypes")
	}
	if !errContains(err, "allowedTypes is empty") {
		t.Errorf("error should mention empty allowedTypes, got: %v", err)
	}
}

func TestBuildProfileWorkerRegistry_NilRoot(t *testing.T) {
	_, _, err := BuildProfileWorkerRegistry(nil, []string{"script.generate"})
	if err == nil {
		t.Fatal("expected error for nil root")
	}
}

func TestBuildProfileWorkerRegistry_ScriptGenerateNotInAllowed_Error(t *testing.T) {
	// script.generate IS in the dispatcher but NOT in allowedTypes.
	// The post-registration gate must catch this.
	dispatcher := appjobs.NewDispatcher()
	_ = dispatcher.Register("script.generate", noopHandler)
	_ = dispatcher.Register("voiceover.generate_item", noopHandler)

	root := &wiring.ComposeRoot{Jobs: &wiring.JobsBundle{Dispatcher: dispatcher}}
	allowed := []string{"voiceover.generate_item"} // script.generate not included
	_, _, err := BuildProfileWorkerRegistry(root, allowed)
	if err == nil {
		t.Fatal("expected error when script.generate not in allowedTypes")
	}
	if !errContains(err, "script.generate") {
		t.Errorf("error should mention script.generate, got: %v", err)
	}
}

func TestBuildProfileWorkerRegistry_NilDispatcher(t *testing.T) {
	root := &wiring.ComposeRoot{Jobs: &wiring.JobsBundle{Dispatcher: nil}}
	_, _, err := BuildProfileWorkerRegistry(root, []string{"script.generate"})
	if err == nil {
		t.Fatal("expected error for nil dispatcher")
	}
}

func TestBuildProfileWorkerRegistry_DerivedCapsSorted(t *testing.T) {
	dispatcher := appjobs.NewDispatcher()
	for _, jt := range []string{"z.job", "a.job", "m.job"} {
		jt := jt
		_ = dispatcher.Register(jt, func(_ context.Context, _ *job.Job, _ *appjobs.JobTools) (map[string]any, error) {
			return nil, nil
		})
	}
	root := &wiring.ComposeRoot{
		Jobs: &wiring.JobsBundle{
			Dispatcher: dispatcher,
		},
	}
	_, caps, err := BuildWorkerRegistry(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(caps) != 3 {
		t.Fatalf("expected 3 caps, got %d", len(caps))
	}
	for i := 1; i < len(caps); i++ {
		if caps[i-1] > caps[i] {
			t.Fatalf("caps not sorted: %v", caps)
		}
	}
}
