// Package jobs — service_has_handler_test.go (Issue 7 / P1, June 2026).
//
// Direct unit test for the new *Service.HasHandler method. Lives in
// the jobs package (where the build is green) so it runs in the
// canonical `go test ./internal/application/jobs/...` surface,
// independently of the voiceover-blocked scripts/jobs package.
//
// Coverage matrix (table-driven, 5 sub-cases):
//
//   - nil receiver           -> false (defensive guard)
//   - nil dispatcher         -> false (composition bug surface)
//   - empty jobType          -> false (input validation)
//   - registered type        -> true  (canonical happy path)
//   - unregistered type      -> false (the wire_script helper depends on this)
//
// The test does NOT touch the database or any external service; it
// constructs an in-memory *Dispatcher via NewDispatcher() and calls
// Dispatcher.Register() directly. The Dispatcher.AllHandlers() map
// is the canonical record the Service.HasHandler() query reads.
package queue

import (
	"context"
	"testing"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestService_HasHandler pins the new *Service.HasHandler contract.
// Each row of the table exercises one of the documented nil-tolerance /
// happy-path / negative-path semantics. The fakeHandler is a no-op
// HandlerFunc; we never invoke it (HasHandler is a pure read query).
func TestService_HasHandler(t *testing.T) {
	t.Parallel()

	// Canonical fake handler — never invoked. The Dispatcher only
	// stores the function pointer for type->handler lookup.
	fakeHandler := HandlerFunc(func(ctx context.Context, j *job.Job, tools *JobTools) (map[string]any, error) {
		return nil, nil
	})

	// Build a wired Service: a real Dispatcher with a registered
	// handler for `script.generate`. The remaining test cases
	// derive from this base (nil receiver, nil dispatcher, etc.).
	dispatcher := NewDispatcher()
	if err := dispatcher.Register(TypeScriptGenerate, fakeHandler); err != nil {
		t.Fatalf("register fake handler: %v", err)
	}
	svc, err := NewService(nakedJobBroker{}, dispatcher, zap.NewNop(), Compose())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Test cases.
	tests := []struct {
		name     string
		svc      *Service // override svc (e.g. nil receiver) per row
		jobType  string
		wantHave bool
	}{
		{
			name:     "nil receiver returns false (defensive guard)",
			svc:      nil,
			jobType:  TypeScriptGenerate,
			wantHave: false,
		},
		{
			name: "nil dispatcher returns false (composition bug)",
			svc: &Service{
				repo:       nil,
				dispatcher: nil,
				log:        nil,
				registry:   nil,
			},
			jobType:  TypeScriptGenerate,
			wantHave: false,
		},
		{
			name:     "empty jobType returns false (input validation)",
			svc:      svc,
			jobType:  "",
			wantHave: false,
		},
		{
			name:     "registered type returns true (canonical happy path)",
			svc:      svc,
			jobType:  TypeScriptGenerate,
			wantHave: true,
		},
		{
			name:     "unregistered type returns false (negative path)",
			svc:      svc,
			jobType:  TypeMediaExtract, // not registered in this test's Dispatcher
			wantHave: false,
		},
	}

	for _, tt := range tests {
		tt := tt // capture range var
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.svc.HasHandler(tt.jobType)
			if got != tt.wantHave {
				t.Errorf("HasHandler(%q) = %v, want %v", tt.jobType, got, tt.wantHave)
			}
		})
	}
}

// TestService_HasHandler_AfterRegister_ReflectsNewState confirms the
// query is live: registering a handler after Service construction
// makes HasHandler return true for that type. Pins the canonical
// composition-time pattern (Service is built early; RegisterHandler
// is called by handler.RegisterJobs later in composition).
func TestService_HasHandler_AfterRegister_ReflectsNewState(t *testing.T) {
	t.Parallel()

	dispatcher2 := NewDispatcher()
	svc, err := NewService(nakedJobBroker{}, dispatcher2, zap.NewNop(), Compose())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Pre-condition: unregistered.
	if svc.HasHandler(TypeMediaExtract) {
		t.Fatal("pre-condition violated: HasHandler should be false before Register")
	}

	// Register a handler for media.extract.
	fakeHandler := HandlerFunc(func(ctx context.Context, j *job.Job, tools *JobTools) (map[string]any, error) {
		return nil, nil
	})
	if err := dispatcher2.Register(TypeMediaExtract, fakeHandler); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Post-condition: registered.
	if !svc.HasHandler(TypeMediaExtract) {
		t.Error("HasHandler should be true after Register")
	}
}
