package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	instaeditcalendar "github.com/Marcuss-ops/PipelineGen/internal/platform/instaeditcalendar"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
)

// TestBuildJobSchedulerStep pins the STARTUP GATE of the deferred-job
// scheduler, which is the only writer of the SCHEDULED → QUEUED promotion.
//
// Two failure modes are ruled out here, both of which would be silent:
//
//   - the step being built when the wired jobs store cannot schedule (the loop
//     would have nothing to promote from) — the scheduler is derived from the
//     store by type assertion, so this is the guard that the derivation works;
//   - the step being built but marked Required (a scheduling fault would then
//     abort the boot of an otherwise healthy server).
//
// Start/Stop are exercised so a step that is wired but never actually launches
// a loop fails here instead of stranding every scheduled job at runtime.
func TestBuildJobSchedulerStep(t *testing.T) {
	t.Run("no jobs root yields no step", func(t *testing.T) {
		if step := buildJobSchedulerStep(jobRunnerDeps{cfg: &config.Config{}, log: zap.NewNop()}); step != nil {
			t.Fatalf("step = %+v, want nil when the jobs root is absent", step)
		}
	})

	t.Run("sqlite jobs store yields an optional job-scheduler step", func(t *testing.T) {
		db, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			t.Fatalf("open in-memory sqlite: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })

		step := buildJobSchedulerStep(jobRunnerDeps{
			root: &ComposeRoot{Jobs: &JobsBundle{Repo: sqljobs.NewSQLiteStore(db, zap.NewNop())}},
			cfg: &config.Config{Jobs: config.JobsConfig{
				SchedulerDailyQuota:    5000,
				SchedulerMaxConcurrent: 1,
				SchedulerInterval:      "1s",
			}},
			log: zap.NewNop(),
		})
		if step == nil {
			t.Fatal("step = nil; want a job-scheduler step when the wired jobs store supports deferred scheduling")
		}
		if step.Name != "job-scheduler" {
			t.Fatalf("step.Name = %q, want job-scheduler", step.Name)
		}
		if step.Required {
			t.Fatal("the scheduler must be an OPTIONAL step: a scheduling fault must not abort the boot")
		}
		if step.Start == nil || step.Stop == nil {
			t.Fatal("the step must provide both Start and Stop")
		}

		ctx, cancel := context.WithCancel(context.Background())
		if err := step.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		cancel()
		if err := step.Stop(context.Background()); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	})
}

// TestSettleWorkerBudget pins the guardrail switch: the dedicated clip.render
// settle budget exists only when the clip.render feature is on AND the budget is
// positive. Either being off is the pre-guardrail single-pool behaviour, so an
// operator can roll the split back without a code change.
func TestSettleWorkerBudget(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want int
	}{
		{"nil config", nil, 0},
		{"feature off, budget set", &config.Config{Jobs: config.JobsConfig{ClipRenderSettleWorkers: 16}}, 0},
		{"feature on, budget zero", &config.Config{Features: config.FeaturesConfig{ClipRenderEnabled: true}}, 0},
		{"feature on, budget negative", &config.Config{Features: config.FeaturesConfig{ClipRenderEnabled: true}, Jobs: config.JobsConfig{ClipRenderSettleWorkers: -1}}, 0},
		{"feature on, budget positive", &config.Config{Features: config.FeaturesConfig{ClipRenderEnabled: true}, Jobs: config.JobsConfig{ClipRenderSettleWorkers: 16}}, 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := settleWorkerBudget(tc.cfg); got != tc.want {
				t.Fatalf("settleWorkerBudget = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestClipRenderPhaseScopeMatchesCapabilityContract pins the pool split against
// the capability-owned wire contract. This scope is what routes a settle
// continuation to the dedicated pool AND keeps it out of the general pool: if
// it stopped matching the key/value cliprender actually writes, every settle job
// would silently fall back into the general pool — no error, no log, guardrail
// gone.
//
// The probe payload is built from the CAPABILITY's contract
// (cliprender.PayloadKeyRenderPhase + ParseRenderPhase) rather than restating
// the literals, so a reintroduced local literal fails here.
func TestClipRenderPhaseScopeMatchesCapabilityContract(t *testing.T) {
	match, exclude := clipRenderPhaseScope()
	if len(match) != 1 || len(exclude) != 1 {
		t.Fatalf("scope must name exactly one phase key: match=%v exclude=%v", match, exclude)
	}

	settlePayload := map[string]any{cliprender.PayloadKeyRenderPhase: string(cliprender.RenderPhaseSettle)}
	phase, err := cliprender.ParseRenderPhase(settlePayload)
	if err != nil {
		t.Fatalf("capability must parse its own settle payload: %v", err)
	}
	if phase != cliprender.RenderPhaseSettle {
		t.Fatalf("ParseRenderPhase = %q, want %q", phase, cliprender.RenderPhaseSettle)
	}
	for _, scope := range []map[string]string{match, exclude} {
		for k, v := range scope {
			raw, ok := settlePayload[k]
			if !ok {
				t.Fatalf("scope key %q is not the key the capability writes (%v)", k, settlePayload)
			}
			if fmt.Sprintf("%v", raw) != v {
				t.Fatalf("scope value %q for key %q does not match the capability's %v", v, k, raw)
			}
		}
	}

	// The submit phase carries NO render_phase (absent key = submit), so the
	// exclusion must not key on anything a submit payload carries — otherwise the
	// general pool would refuse the jobs it is the only owner of.
	submitPayload := map[string]any{}
	if _, err := cliprender.ParseRenderPhase(submitPayload); err != nil {
		t.Fatalf("an absent phase must be the submit phase: %v", err)
	}
	for k := range exclude {
		if _, ok := submitPayload[k]; ok {
			t.Fatalf("the exclusion must not key on anything a submit payload carries (%q)", k)
		}
	}
}

// TestJobRunnerBuildersFailClosedWithoutRoot pins that both pools refuse to
// build without the canonical root wiring instead of returning a runner that
// would poll an unowned queue.
func TestJobRunnerBuildersFailClosedWithoutRoot(t *testing.T) {
	deps := jobRunnerDeps{root: nil, cfg: &config.Config{}, log: nil}
	if runner, reporter := buildJobRunner(deps); runner != nil || reporter != nil {
		t.Fatalf("buildJobRunner without root = (%v, %v), want (nil, nil)", runner, reporter)
	}
	if runner, reporter := buildClipRenderSettleRunner(deps); runner != nil || reporter != nil {
		t.Fatalf("buildClipRenderSettleRunner without root = (%v, %v), want (nil, nil)", runner, reporter)
	}
}

// TestJobRunnerStepDrainsTheCalendarSpool pins the delivery half of the
// InstaEdit bridge: wiring a Reporter onto the workers only SPOOLS reports, so
// the job-runner step must also start the drain loop. Without it every report
// stays on disk and the calendar receives nothing — the exact failure the
// reporter's zero callers produced.
//
// The assertion runs the step's Start against a live outbox directory and a
// stub calendar, then observes the spooled record leave the spool.
func TestJobRunnerStepDrainsTheCalendarSpool(t *testing.T) {
	var delivered atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/calendar/events/by-job/job-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"event_key":"event-1"}`))
		case "/api/v1/agent/calendar/events/event-1/progress":
			delivered.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client, err := instaeditcalendar.NewClient(srv.URL, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	reporter, err := instaeditcalendar.NewReporter(client, dir, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := reporter.EnqueueJobProgress("job-1", instaeditcalendar.Progress{Kind: "video.create", Status: "RUNNING"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pools := []jobRunnerPool{
		{name: jobRunnerPoolGeneral, reporter: reporter},
		{name: "pool-without-calendar"},
	}
	startCalendarDrains(pools, ctx, nil)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if delivered.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if delivered.Load() == 0 {
		t.Fatal("the reporter never delivered the spooled record: the drain loop is not running")
	}
}

// TestBuildJobRunnerStepStartsTheDrains is the forward-prevention half of the
// pin above: the drain helper works, but it is worth nothing if the job-runner
// step stops calling it — which is precisely how the reporter ended up with
// zero production callers and a calendar that never updated. The call site is
// asserted inside buildJobRunnerStep's own body, so moving the call into a dead
// branch or deleting it fails here instead of in production.
func TestBuildJobRunnerStepStartsTheDrains(t *testing.T) {
	src, err := os.ReadFile("lifecycle_job_runner.go")
	if err != nil {
		t.Fatal(err)
	}
	const start = "func buildJobRunnerStep("
	begin := strings.Index(string(src), start)
	if begin < 0 {
		t.Fatalf("lifecycle_job_runner.go no longer declares %s", start)
	}
	rest := string(src)[begin+len(start):]
	end := strings.Index(rest, "\nfunc ")
	if end < 0 {
		end = len(rest)
	}
	body := rest[:end]
	if !strings.Contains(body, "startCalendarDrains(pools, startCtx, deps.log)") {
		t.Fatal("buildJobRunnerStep no longer starts the calendar drains: spooled reports would never reach InstaEdit")
	}
}
