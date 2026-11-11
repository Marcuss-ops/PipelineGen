package wiring

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

type remoteFinalJobTestDriveReader struct{ drive.Reader }

// TestRemoteFinalJobAdapterFailsClosedWithoutCredentials pins the FIRST link of
// the no-credentials deployment: on a host with neither
// VELOX_FINAL_JOB_M2M_SECRET nor the credentials file, building the hand-off
// must fail with an error naming what to provision.
//
// This is the behaviour that decides whether an unprovisioned host can ever
// ship a final_job: the wiring caller logs this error and leaves the submitter
// unwired (script_generation_runtime.go), and the runner then fails a
// final_job=true run closed at the publishing stage — pinned by
// TestFinalJobWithoutAWiredSubmitterFailsTheRunClosed in the scripts package.
// Together they are the whole chain: no credentials → no adapter → no submitter
// → the request fails instead of silently producing something else.
func TestRemoteFinalJobAdapterFailsClosedWithoutCredentials(t *testing.T) {
	// Point the lookup at a path that cannot exist, so the test never depends on
	// (or reads) the operator's real credentials file.
	missing := filepath.Join(t.TempDir(), "creator-77-master.env")
	t.Setenv("VELOX_FINAL_JOB_M2M_SECRET", "")
	t.Setenv("VELOX_FINAL_JOB_CREDENTIALS_FILE", missing)
	t.Setenv("OBJECTSTORE_PUBLIC_URL", "https://objects.example.test")

	// A non-nil *sql.DB is enough: the credentials are resolved before the media
	// searcher is built, so an unconfigured host never reaches the database.
	var db sql.DB
	root := &ComposeRoot{
		Drive:         &DriveBundle{Reader: &remoteFinalJobTestDriveReader{}},
		MediaPostgres: &db,
	}

	adapter, err := newRemoteFinalJobAdapter(root)
	if err == nil {
		t.Fatalf("adapter = %+v, want a fail-closed error when no credentials are configured", adapter)
	}
	if !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("error = %q, want it to name the credentials that must be provisioned", err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error = %q, want it to name the looked-up path %q so an operator can act on it", err, missing)
	}

}
