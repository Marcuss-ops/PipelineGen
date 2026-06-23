package health

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// service_test.go: existing legacy tests are above (preserved). This block
// adds the table-driven close-out coverage required by fix(health).

// scenarioMock is a checker that returns a scripted CheckResult. It
// implements DBChecker, DriveChecker, QdrantChecker, JobsChecker so the
// same concrete type can be plugged into any port field of ServiceDeps.
type scenarioMock struct {
	name       string
	ok         bool
	applicable bool
	mandatory  bool
}

func (s *scenarioMock) result() CheckResult {
	r := CheckResult{"ok": s.ok, "duration_ms": int64(0), "name": s.name}
	if !s.mandatory {
		r["applicable"] = s.applicable
	}
	return r
}

func (s *scenarioMock) CheckDB(_ context.Context) CheckResult    { return s.result() }
func (s *scenarioMock) CheckDrive(_ context.Context) CheckResult  { return s.result() }
func (s *scenarioMock) CheckQdrant(_ context.Context) CheckResult { return s.result() }
func (s *scenarioMock) CheckJobs(_ context.Context) CheckResult   { return s.result() }

// TestService_Check_Scenarios is the close-out table-driven smoke for the
// aggregation policy that ReadyChecker inherits verbatim.
func TestService_Check_Scenarios(t *testing.T) {
	dbOK := &scenarioMock{name: "db", mandatory: true, ok: true}
	dbFail := &scenarioMock{name: "db", mandatory: true, ok: false}
	jobsOK := &scenarioMock{name: "jobs", mandatory: true, ok: true}
	driveOptOut := &scenarioMock{name: "drive", mandatory: false, applicable: false}
	driveWired := &scenarioMock{name: "drive", mandatory: false, applicable: true, ok: true}
	qdrantOptOut := &scenarioMock{name: "qdrant", mandatory: false, applicable: false}

	type tc struct {
		name    string
		names   []string
		db, drive, qdrant, jobs interface{}
		wantOK  bool
	}

	cases := []tc{
		{
			name: "empty_names_returns_healthy_no_op",
			names: nil,
			db: DBChecker(dbOK), drive: DriveChecker(driveOptOut),
			qdrant: QdrantChecker(qdrantOptOut), jobs: JobsChecker(jobsOK),
			wantOK: true,
		},
		{
			name: "single_mandatory_db_ok",
			names: []string{"db"},
			db: DBChecker(dbOK), drive: DriveChecker(driveOptOut),
			qdrant: QdrantChecker(qdrantOptOut), jobs: JobsChecker(jobsOK),
			wantOK: true,
		},
		{
			name: "db_fails_aggregate_unhealthy",
			names: []string{"db"},
			db: DBChecker(dbFail), drive: DriveChecker(driveOptOut),
			qdrant: QdrantChecker(qdrantOptOut), jobs: JobsChecker(jobsOK),
			wantOK: false,
		},
		{
			name: "drive_and_qdrant_opted_out_healthy_via_db_jobs",
			names: []string{"db", "drive", "qdrant", "jobs"},
			db: DBChecker(dbOK), drive: DriveChecker(driveOptOut),
			qdrant: QdrantChecker(qdrantOptOut), jobs: JobsChecker(jobsOK),
			wantOK: true,
		},
		{
			name: "drive_fails_with_db_ok_is_unhealthy",
			names: []string{"db", "drive"},
			db: DBChecker(dbOK), drive: DriveChecker(driveWired),
			qdrant: QdrantChecker(qdrantOptOut), jobs: JobsChecker(jobsOK),
			wantOK: false,
		},
		{
			name: "unknown_check_is_defensively_unhealthy",
			names: []string{"db", "whatisthis"},
			db: DBChecker(dbOK), drive: DriveChecker(driveOptOut),
			qdrant: QdrantChecker(qdrantOptOut), jobs: JobsChecker(jobsOK),
			wantOK: false,
		},
		{
			name: "nil_jobs_checker_misconfig_is_loud_unhealthy",
			names: []string{"jobs"},
			db: DBChecker(dbOK), drive: DriveChecker(driveOptOut),
			qdrant: QdrantChecker(qdrantOptOut), jobs: JobsChecker(nil),
			wantOK: false,
		},
	}

	for _, sc := range cases {
		t.Run(sc.name, func(t *testing.T) {
			svc := NewService(ServiceDeps{
				DB:     sc.db.(DBChecker),
				Drive:  sc.drive.(DriveChecker),
				Qdrant: sc.qdrant.(QdrantChecker),
				Jobs:   sc.jobs.(JobsChecker),
			})
			resp := svc.Check(context.Background(), sc.names)
			require.Equal(t, sc.wantOK, resp.OK,
				"case %q wanted OK=%v, body=%v", sc.name, sc.wantOK, resp)
			if len(sc.names) > 0 {
				require.Equal(t, len(sc.names), len(resp.Checks),
					"case %q expected %d checks, got %d", sc.name, len(sc.names), len(resp.Checks))
			}
		})
	}
}
