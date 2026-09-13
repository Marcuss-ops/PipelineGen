package wiring

import (
	"os"
	"strings"
	"testing"
)

// TestNoCrossEngineJobFinalizer pins MEDIA-SSOT P0-2: wiring must never build a
// JobFinalizer with the PostgreSQL assetTx and a SQLite job DB (the SQLite job
// transaction cannot carry PostgreSQL SQL — `unrecognized token: ":"`). The
// two-phase splitPlaneFinalizer is the only allowed shape once the canonical
// media writer is PostgreSQL.
func TestNoCrossEngineJobFinalizer(t *testing.T) {
	files := []string{
		"wire_stock_pipeline.go",
		"wire_services_composition.go",
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		body := string(src)
		if strings.Contains(body, "jobsfinalizer.New(jobDB, jobOutbox, assetTx") {
			t.Errorf("%s: cross-engine jobsfinalizer.New(jobDB, jobOutbox, assetTx) is banned; use the splitPlaneFinalizer two-phase boundary (MEDIA-SSOT P0-2)", file)
		}
	}
}

// TestArtlistPersistUsesCommitterEngineTx pins MEDIA-SSOT P0-3: the Artlist
// persist path must not open the asset finalizer transaction on the operational
// SQLite mainDB; it goes through Service.assetFinalizerDB() so the tx engine
// matches the wired committer.
func TestArtlistPersistUsesCommitterEngineTx(t *testing.T) {
	const file = "../../capabilities/assets/providers/artlist/run_orchestrator_persist.go"
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	body := string(src)
	if strings.Contains(body, "o.svc.mainDB.BeginTx") {
		t.Errorf("%s: asset finalizer tx must not be opened on mainDB; use assetFinalizerDB() so the engine matches the committer (MEDIA-SSOT P0-3)", file)
	}
	if !strings.Contains(body, "assetFinalizerDB()") {
		t.Errorf("%s: expected the persist path to select the committer-engine tx via assetFinalizerDB()", file)
	}
}
